package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dataResponse struct {
	Code int         `json:"code"`
	Data dataPayload `json:"data"`
}

type dataPayload struct {
	Type                  string                     `json:"type"`
	Version               int                        `json:"version"`
	Proxies               []dataProxy                `json:"proxies"`
	Accounts              []dataAccount              `json:"accounts"`
	ManagedProxyProviders []DataManagedProxyProvider `json:"managed_proxy_providers"`
	SkippedShadows        int                        `json:"skipped_shadows"`
}

type dataProxy struct {
	ProxyKey string `json:"proxy_key"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Status   string `json:"status"`
}

type dataAccount struct {
	Name         string                   `json:"name"`
	Platform     string                   `json:"platform"`
	Type         string                   `json:"type"`
	Credentials  map[string]any           `json:"credentials"`
	Extra        map[string]any           `json:"extra"`
	ProxyKey     *string                  `json:"proxy_key"`
	ManagedProxy *DataManagedProxyBinding `json:"managed_proxy"`
	Concurrency  int                      `json:"concurrency"`
	Priority     int                      `json:"priority"`
}

type managedAccountProvisionStub struct {
	managedByAccount  map[int64]*service.ManagedProxyAccountDTO
	providers         map[int64]*service.CatProxyProviderConfig
	candidate         service.ManagedProxyCandidate
	created           []*service.CreateAccountInput
	createdCandidates []service.ManagedProxyCandidate
	prepareErr        error
	createErr         error
	restoreErr        error
	restoredProvider  *service.CatProxyProviderConfigDTO
	restoredBackups   []service.CatProxyProviderBackup
	restoredStatuses  []string
	preparedProviders []int64
	preparedTargets   []service.CatProxiesProxyTarget
	prepareStarted    chan struct{}
	prepareRelease    <-chan struct{}
	mu                sync.Mutex
	currentPrepares   int
	maxPrepares       int
}

func (s *managedAccountProvisionStub) ValidateProviderForAssignment(context.Context, int64) error {
	return nil
}
func (s *managedAccountProvisionStub) Prepare(_ context.Context, providerID int64, target service.CatProxiesProxyTarget) (service.ManagedProxyCandidate, error) {
	s.mu.Lock()
	s.currentPrepares++
	s.preparedProviders = append(s.preparedProviders, providerID)
	s.preparedTargets = append(s.preparedTargets, target)
	if s.currentPrepares > s.maxPrepares {
		s.maxPrepares = s.currentPrepares
	}
	s.mu.Unlock()
	if s.prepareStarted != nil {
		s.prepareStarted <- struct{}{}
	}
	if s.prepareRelease != nil {
		<-s.prepareRelease
	}
	s.mu.Lock()
	s.currentPrepares--
	s.mu.Unlock()
	candidate := s.candidate
	if candidate.ProviderConfigID == 0 {
		candidate.ProviderConfigID = providerID
	}
	return candidate, s.prepareErr
}
func (s *managedAccountProvisionStub) Create(_ context.Context, input *service.CreateAccountInput, candidate service.ManagedProxyCandidate) (*service.Account, error) {
	s.created = append(s.created, input)
	s.createdCandidates = append(s.createdCandidates, candidate)
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &service.Account{ID: int64(len(s.created)), Name: input.Name}, nil
}
func (s *managedAccountProvisionStub) GetManagedAccount(_ context.Context, accountID int64) (*service.ManagedProxyAccountDTO, error) {
	managed := s.managedByAccount[accountID]
	if managed == nil {
		return nil, service.ErrManagedProxyLeaseNotFound
	}
	return managed, nil
}
func (s *managedAccountProvisionStub) ListManagedAccounts(context.Context) ([]service.ManagedProxyAccountDTO, error) {
	result := make([]service.ManagedProxyAccountDTO, 0, len(s.managedByAccount))
	for _, managed := range s.managedByAccount {
		if managed != nil {
			result = append(result, *managed)
		}
	}
	return result, nil
}
func (s *managedAccountProvisionStub) GetProviderForBackup(_ context.Context, id int64) (*service.CatProxyProviderConfig, error) {
	provider := s.providers[id]
	if provider == nil {
		return nil, service.ErrCatProxyProviderConfigNotFound
	}
	return provider, nil
}
func (s *managedAccountProvisionStub) RestoreProvider(_ context.Context, backup service.CatProxyProviderBackup) (*service.CatProxyProviderConfigDTO, error) {
	s.restoredBackups = append(s.restoredBackups, backup)
	if s.restoreErr != nil {
		return nil, s.restoreErr
	}
	if s.restoredProvider == nil {
		return nil, errors.New("restored provider is not configured")
	}
	return s.restoredProvider, nil
}
func (s *managedAccountProvisionStub) SetRestoredProviderStatus(_ context.Context, _ int64, status string) error {
	s.restoredStatuses = append(s.restoredStatuses, status)
	return nil
}

func setupAccountDataRouter() (*gin.Engine, *stubAdminService) {
	return setupAccountDataRouterWithManaged(nil)
}

func setupAccountDataRouterWithManaged(managed managedAccountProvisioner) (*gin.Engine, *stubAdminService) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminSvc := newStubAdminService()

	h := NewAccountHandler(
		adminSvc,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	h.SetManagedAccountProvisionService(managed)
	router.GET("/api/v1/admin/accounts/data", h.ExportData)
	router.POST("/api/v1/admin/accounts/data", h.ImportData)
	return router, adminSvc
}

func TestManagedProxyAccountResponseRedactsSessionUsername(t *testing.T) {
	secretSessionUsername := "customer-type-residential-lifetime-60-session-secret"
	proxyID := int64(3)
	provision := &managedAccountProvisionStub{managedByAccount: map[int64]*service.ManagedProxyAccountDTO{
		9: {Lease: service.ManagedProxyLease{AccountID: 9, ProviderConfigID: 7, ProxyID: proxyID}},
	}}
	handler := &AccountHandler{managedAccountProvision: provision}
	item := handler.buildAccountResponseWithRuntime(context.Background(), &service.Account{
		ID: 9, Name: "managed", ProxyID: &proxyID, Proxy: &service.Proxy{ID: proxyID, Username: secretSessionUsername},
	})
	encoded, err := json.Marshal(item)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secretSessionUsername)
	require.Contains(t, string(encoded), `"username":""`)

	staticProxyID := int64(4)
	static := handler.buildAccountResponseWithRuntime(context.Background(), &service.Account{
		ID: 10, Name: "static", ProxyID: &staticProxyID, Proxy: &service.Proxy{ID: staticProxyID, Username: "static-user"},
	})
	encoded, err = json.Marshal(static)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "static-user")
}

func TestExportDataIncludesSecrets(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	proxyID := int64(11)
	adminSvc.proxies = []service.Proxy{
		{
			ID:       proxyID,
			Name:     "proxy",
			Protocol: "http",
			Host:     "127.0.0.1",
			Port:     8080,
			Username: "user",
			Password: "pass",
			Status:   service.StatusActive,
		},
		{
			ID:       12,
			Name:     "orphan",
			Protocol: "https",
			Host:     "10.0.0.1",
			Port:     443,
			Username: "o",
			Password: "p",
			Status:   service.StatusActive,
		},
	}
	adminSvc.accounts = []service.Account{
		{
			ID:          21,
			Name:        "account",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Credentials: map[string]any{"token": "secret"},
			Extra:       map[string]any{"note": "x"},
			ProxyID:     &proxyID,
			Concurrency: 3,
			Priority:    50,
			Status:      service.StatusDisabled,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Empty(t, resp.Data.Type)
	require.Equal(t, 0, resp.Data.Version)
	require.Len(t, resp.Data.Proxies, 1)
	require.Equal(t, "pass", resp.Data.Proxies[0].Password)
	require.Len(t, resp.Data.Accounts, 1)
	require.Equal(t, "secret", resp.Data.Accounts[0].Credentials["token"])
}

func TestExportDataUsesV2AndOmitsDerivedProxyAndSession(t *testing.T) {
	country := "us"
	strict := true
	proxyID := int64(11)
	managed := &managedAccountProvisionStub{
		managedByAccount: map[int64]*service.ManagedProxyAccountDTO{
			21: {Lease: service.ManagedProxyLease{AccountID: 21, ProxyID: proxyID, ProviderConfigID: 7, SessionID: "never-export-session", TargetCountry: &country, Strict: strict}},
		},
		providers: map[int64]*service.CatProxyProviderConfig{
			7: {ID: 7, Name: "order-a", Protocol: service.CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer", Password: "backup-secret", LifetimeMinutes: 60, Strict: true, Status: service.CatProxiesStatusActive},
		},
	}
	router, adminSvc := setupAccountDataRouterWithManaged(managed)
	adminSvc.proxies = []service.Proxy{{ID: proxyID, Name: "derived", Protocol: "http", Host: "proxy.example.com", Port: 10000, Username: "derived-user", Password: "derived-password", Status: service.StatusActive}}
	adminSvc.accounts = []service.Account{{ID: 21, Name: "account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"token": "secret"}, ProxyID: &proxyID}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, managedDataType, resp.Data.Type)
	require.Equal(t, managedDataVersion, resp.Data.Version)
	require.Empty(t, resp.Data.Proxies)
	require.Len(t, resp.Data.ManagedProxyProviders, 1)
	require.Equal(t, "backup-secret", resp.Data.ManagedProxyProviders[0].Password)
	require.Len(t, resp.Data.Accounts, 1)
	require.NotNil(t, resp.Data.Accounts[0].ManagedProxy)
	require.Equal(t, "catproxies-7", resp.Data.Accounts[0].ManagedProxy.ProviderKey)
	require.NotContains(t, rec.Body.String(), "never-export-session")
	require.NotContains(t, rec.Body.String(), "derived-password")
}

func TestExportDataWithoutProxies(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	proxyID := int64(11)
	adminSvc.proxies = []service.Proxy{
		{
			ID:       proxyID,
			Name:     "proxy",
			Protocol: "http",
			Host:     "127.0.0.1",
			Port:     8080,
			Username: "user",
			Password: "pass",
			Status:   service.StatusActive,
		},
	}
	adminSvc.accounts = []service.Account{
		{
			ID:          21,
			Name:        "account",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Credentials: map[string]any{"token": "secret"},
			ProxyID:     &proxyID,
			Concurrency: 3,
			Priority:    50,
			Status:      service.StatusDisabled,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data?include_proxies=false", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Proxies, 0)
	require.Len(t, resp.Data.Accounts, 1)
	require.Nil(t, resp.Data.Accounts[0].ProxyKey)
}

// TestExportDataExcludesSparkShadow 验证外审第5轮 P1/P2:导出时排除 spark 影子账号
// (影子无凭据、导入侧强制 credentials 非空,混入会产出无法还原的坏备份),并透出跳过计数。
func TestExportDataExcludesSparkShadow(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	parentID := int64(21)
	adminSvc.accounts = []service.Account{
		{
			ID:          parentID,
			Name:        "mother",
			Platform:    service.PlatformOpenAI,
			Type:        service.AccountTypeOAuth,
			Credentials: map[string]any{"token": "secret"},
			Status:      service.StatusActive,
		},
		{
			ID:              22,
			Name:            "mother (Spark)",
			Platform:        service.PlatformOpenAI,
			Type:            service.AccountTypeOAuth,
			Credentials:     map[string]any{}, // 影子恒空凭据
			ParentAccountID: &parentID,        // 影子标记
			QuotaDimension:  service.QuotaDimensionSpark,
			Status:          service.StatusActive,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data?include_proxies=false", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Accounts, 1, "影子应被排除,仅导出母账号")
	require.Equal(t, "mother", resp.Data.Accounts[0].Name)
	require.Equal(t, 1, resp.Data.SkippedShadows, "跳过的影子数量应透出")
}

func TestExportDataPassesAccountFiltersAndSort(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()
	adminSvc.accounts = []service.Account{
		{ID: 1, Name: "acc-1", Status: service.StatusActive},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/accounts/data?platform=openai&type=oauth&status=active&group=12&privacy_mode=blocked&search=keyword&sort_by=priority&sort_order=desc",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, 1, adminSvc.lastListAccounts.calls)
	require.Equal(t, "openai", adminSvc.lastListAccounts.platform)
	require.Equal(t, "oauth", adminSvc.lastListAccounts.accountType)
	require.Equal(t, "active", adminSvc.lastListAccounts.status)
	require.Equal(t, int64(12), adminSvc.lastListAccounts.groupID)
	require.Equal(t, "blocked", adminSvc.lastListAccounts.privacyMode)
	require.Equal(t, "keyword", adminSvc.lastListAccounts.search)
	require.Equal(t, "priority", adminSvc.lastListAccounts.sortBy)
	require.Equal(t, "desc", adminSvc.lastListAccounts.sortOrder)
}

func TestExportDataSelectedIDsOverrideFilters(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/accounts/data?ids=1,2&platform=openai&search=keyword&sort_by=priority&sort_order=desc",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Accounts, 2)
	require.Equal(t, 0, adminSvc.lastListAccounts.calls)
}

func TestImportDataAssignsManagedProxyWithoutCreatingStaticProxy(t *testing.T) {
	managed := &managedAccountProvisionStub{candidate: service.ManagedProxyCandidate{ProviderConfigID: 7}}
	router, adminSvc := setupAccountDataRouterWithManaged(managed)
	dataPayload := map[string]any{
		"data": map[string]any{
			"type": dataType, "version": dataVersion,
			"proxies":  []any{},
			"accounts": []map[string]any{{"name": "acc", "platform": service.PlatformOpenAI, "type": service.AccountTypeOAuth, "credentials": map[string]any{"token": "x"}}},
		},
		"skip_default_group_bind":  true,
		"managed_proxy_assignment": map[string]any{"provider_config_id": 7, "country": "us", "strict": true},
	}
	body, _ := json.Marshal(dataPayload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, adminSvc.createdProxies)
	require.Empty(t, adminSvc.createdAccounts)
	require.Len(t, managed.created, 1)
	require.True(t, managed.created[0].SkipDefaultGroupBind)
	require.Equal(t, "acc", managed.created[0].Name)
}

func TestImportDataRestoresV2ManagedProviderAndAccountWithFreshCandidate(t *testing.T) {
	managed := &managedAccountProvisionStub{
		restoredProvider: &service.CatProxyProviderConfigDTO{ID: 91, Name: "restored-order"},
		candidate:        service.ManagedProxyCandidate{ProviderConfigID: 91},
	}
	router, adminSvc := setupAccountDataRouterWithManaged(managed)
	payload := map[string]any{
		"data": map[string]any{
			"type": managedDataType, "version": managedDataVersion,
			"proxies": []any{},
			"managed_proxy_providers": []map[string]any{{
				"provider_key": "catproxies-7", "name": "order-a", "status": service.CatProxiesStatusRetiring,
				"protocol": service.CatProxiesProtocolHTTP, "host": "proxy.example.com", "base_username": "customer-type-residential",
				"password": "backup-secret", "lifetime_minutes": 60, "strict": true,
			}},
			"accounts": []map[string]any{{
				"name": "restored", "platform": service.PlatformOpenAI, "type": service.AccountTypeOAuth,
				"credentials":   map[string]any{"token": "x"},
				"managed_proxy": map[string]any{"provider_key": "catproxies-7", "country": "us", "state": "california", "strict": true},
			}},
		},
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, adminSvc.createdProxies)
	require.Empty(t, adminSvc.createdAccounts)
	require.Len(t, managed.restoredBackups, 1)
	require.Equal(t, "backup-secret", managed.restoredBackups[0].Password)
	require.Equal(t, []int64{91}, managed.preparedProviders)
	require.Len(t, managed.preparedTargets, 1)
	require.Equal(t, "us", *managed.preparedTargets[0].Country)
	require.Equal(t, "california", *managed.preparedTargets[0].State)
	require.Len(t, managed.created, 1)
	require.Equal(t, []string{service.CatProxiesStatusRetiring}, managed.restoredStatuses)
	require.Contains(t, rec.Body.String(), `"status":"created"`)
	require.Contains(t, rec.Body.String(), `"managed":true`)
}

func TestImportDataKeepsRestoredDisabledProviderAccountsNotReadyFromFirstCommit(t *testing.T) {
	managed := &managedAccountProvisionStub{
		restoredProvider: &service.CatProxyProviderConfigDTO{ID: 91},
		candidate:        service.ManagedProxyCandidate{ProviderConfigID: 91},
	}
	router, _ := setupAccountDataRouterWithManaged(managed)
	payload := map[string]any{"data": map[string]any{
		"type": managedDataType, "version": managedDataVersion, "proxies": []any{},
		"managed_proxy_providers": []map[string]any{{
			"provider_key": "catproxies-7", "name": "order-a", "status": service.CatProxiesStatusDisabled,
			"protocol": "http", "host": "proxy.example.com", "base_username": "customer", "password": "secret", "lifetime_minutes": 60,
		}},
		"accounts": []map[string]any{{
			"name": "restored", "platform": service.PlatformOpenAI, "type": service.AccountTypeOAuth,
			"credentials": map[string]any{"token": "x"}, "managed_proxy": map[string]any{"provider_key": "catproxies-7"},
		}},
	}}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, managed.createdCandidates, 1)
	require.NotNil(t, managed.createdCandidates[0].InitialReady)
	require.False(t, *managed.createdCandidates[0].InitialReady)
	require.Equal(t, []string{service.CatProxiesStatusDisabled}, managed.restoredStatuses)
}

func TestImportDataRejectsConflictingManagedProviderKeysBeforeRestore(t *testing.T) {
	managed := &managedAccountProvisionStub{restoredProvider: &service.CatProxyProviderConfigDTO{ID: 91}}
	router, _ := setupAccountDataRouterWithManaged(managed)
	payload := map[string]any{
		"data": map[string]any{
			"type": managedDataType, "version": managedDataVersion,
			"proxies":  []any{},
			"accounts": []any{},
			"managed_proxy_providers": []map[string]any{
				{"provider_key": "catproxies-7", "name": "order-a", "protocol": "http", "host": "first.example.com", "base_username": "customer", "password": "secret", "lifetime_minutes": 60, "status": service.CatProxiesStatusActive},
				{"provider_key": "catproxies-7", "name": "order-a", "protocol": "http", "host": "second.example.com", "base_username": "customer", "password": "secret", "lifetime_minutes": 60, "status": service.CatProxiesStatusActive},
			},
		},
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Empty(t, managed.restoredBackups, "conflicting provider definitions must be rejected before side effects")
	require.Contains(t, rec.Body.String(), "MANAGED_PROVIDER_KEY_CONFLICT")
}

func TestImportDataRestoresIdenticalManagedProviderKeyOnlyOnce(t *testing.T) {
	managed := &managedAccountProvisionStub{restoredProvider: &service.CatProxyProviderConfigDTO{ID: 91}}
	router, _ := setupAccountDataRouterWithManaged(managed)
	provider := map[string]any{
		"provider_key": "catproxies-7", "name": "order-a", "protocol": "http", "host": "proxy.example.com",
		"base_username": "customer", "password": "secret", "lifetime_minutes": 60, "status": service.CatProxiesStatusActive,
	}
	payload := map[string]any{
		"data": map[string]any{
			"type": managedDataType, "version": managedDataVersion,
			"proxies": []any{}, "accounts": []any{},
			"managed_proxy_providers": []map[string]any{provider, provider},
		},
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, managed.restoredBackups, 1)
}

func TestImportDataLimitsManagedProxyProbesToFive(t *testing.T) {
	release := make(chan struct{})
	managed := &managedAccountProvisionStub{prepareStarted: make(chan struct{}, 11), prepareRelease: release}
	router, _ := setupAccountDataRouterWithManaged(managed)
	accounts := make([]map[string]any, 11)
	for i := range accounts {
		accounts[i] = map[string]any{"name": fmt.Sprintf("acc-%d", i), "platform": service.PlatformOpenAI, "type": service.AccountTypeOAuth, "credentials": map[string]any{"token": "x"}}
	}
	payload := map[string]any{
		"data":                     map[string]any{"type": dataType, "version": dataVersion, "proxies": []any{}, "accounts": accounts},
		"managed_proxy_assignment": map[string]any{"provider_config_id": 7},
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() { router.ServeHTTP(rec, req); close(done) }()

	for range 5 {
		<-managed.prepareStarted
	}
	select {
	case <-managed.prepareStarted:
		t.Fatal("more than five managed proxy probes started concurrently")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("managed import did not finish")
	}
	require.Equal(t, http.StatusOK, rec.Code)
	managed.mu.Lock()
	maxPrepares := managed.maxPrepares
	managed.mu.Unlock()
	require.Equal(t, 5, maxPrepares)
	require.Len(t, managed.created, 11)
}

func TestImportDataReusesProxyAndSkipsDefaultGroup(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	adminSvc.proxies = []service.Proxy{
		{
			ID:       1,
			Name:     "proxy",
			Protocol: "socks5",
			Host:     "1.2.3.4",
			Port:     1080,
			Username: "u",
			Password: "p",
			Status:   service.StatusActive,
		},
	}

	dataPayload := map[string]any{
		"data": map[string]any{
			"type":    dataType,
			"version": dataVersion,
			"proxies": []map[string]any{
				{
					"proxy_key": "socks5|1.2.3.4|1080|u|p",
					"name":      "proxy",
					"protocol":  "socks5",
					"host":      "1.2.3.4",
					"port":      1080,
					"username":  "u",
					"password":  "p",
					"status":    "active",
				},
			},
			"accounts": []map[string]any{
				{
					"name":        "acc",
					"platform":    service.PlatformOpenAI,
					"type":        service.AccountTypeOAuth,
					"credentials": map[string]any{"token": "x"},
					"proxy_key":   "socks5|1.2.3.4|1080|u|p",
					"concurrency": 3,
					"priority":    50,
				},
			},
		},
		"skip_default_group_bind": true,
	}

	body, _ := json.Marshal(dataPayload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, adminSvc.createdProxies, 0)
	require.Len(t, adminSvc.createdAccounts, 1)
	require.True(t, adminSvc.createdAccounts[0].SkipDefaultGroupBind)
}
