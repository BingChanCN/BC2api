package admin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	dataType              = "sub2api-data"
	legacyDataType        = "sub2api-bundle"
	dataVersion           = 1
	managedDataType       = "sub2api-account-data"
	managedDataVersion    = 2
	managedImportParallel = 5
	dataPageCap           = 1000
)

type DataPayload struct {
	Type                  string                     `json:"type,omitempty"`
	Version               int                        `json:"version,omitempty"`
	ExportedAt            string                     `json:"exported_at"`
	Proxies               []DataProxy                `json:"proxies"`
	Accounts              []DataAccount              `json:"accounts"`
	ManagedProxyProviders []DataManagedProxyProvider `json:"managed_proxy_providers,omitempty"`
	// SkippedShadows 记录导出时被排除的 spark 影子账号数量(见 ExportData)。仅作可见性提示,
	// 导入侧忽略该字段;omitempty 保持向后兼容。
	SkippedShadows int `json:"skipped_shadows,omitempty"`
}

type DataProxy struct {
	ProxyKey        string `json:"proxy_key"`
	Name            string `json:"name"`
	Protocol        string `json:"protocol"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username,omitempty"`
	Password        string `json:"password,omitempty"`
	Status          string `json:"status"`
	ExpiresAt       *int64 `json:"expires_at,omitempty"`        // unix 秒，与 DataAccount.ExpiresAt 风格一致
	FallbackMode    string `json:"fallback_mode,omitempty"`     // none/direct/proxy
	BackupProxyName string `json:"backup_proxy_name,omitempty"` // 备用代理 name（跨实例按 name 反查）
	ExpiryWarnDays  int    `json:"expiry_warn_days,omitempty"`
}

// DataAccount 是管理员显式备份导出使用的账号结构，故意不走 dto.Account 的脱敏路径，
// Credentials 原文返回。这是"管理员备份"这一显式行为的一部分；如未来需要导出脱敏版本，
// 应新增独立结构而非修改这里。
// 注意:本结构不含 parent_account_id/quota_dimension——spark 影子账号在 ExportData 处被显式
// 排除(影子不持凭据、通用凭据型导入强制 credentials 非空无法重建父子链接),不在此表达。
// 影子的独立调度配置(priority/并发/分组/status 管理员可单独调)亦不在本备份范围,属已知局限
// (外审第6轮裁决:保持排除 + 前端警告,而非升级格式做完整往返)。
type DataManagedProxyProvider struct {
	ProviderKey     string  `json:"provider_key"`
	Name            string  `json:"name"`
	Protocol        string  `json:"protocol"`
	Host            string  `json:"host"`
	BaseUsername    string  `json:"base_username"`
	Password        string  `json:"password"`
	DefaultCountry  *string `json:"default_country,omitempty"`
	DefaultState    *string `json:"default_state,omitempty"`
	DefaultCity     *string `json:"default_city,omitempty"`
	LifetimeMinutes int     `json:"lifetime_minutes"`
	Strict          bool    `json:"strict"`
	Status          string  `json:"status"`
	IsDefault       bool    `json:"is_default,omitempty"`
}

type DataManagedProxyBinding struct {
	ProviderKey string  `json:"provider_key"`
	Country     *string `json:"country,omitempty"`
	State       *string `json:"state,omitempty"`
	City        *string `json:"city,omitempty"`
	Strict      *bool   `json:"strict,omitempty"`
}

type DataAccount struct {
	Name               string                   `json:"name"`
	Notes              *string                  `json:"notes,omitempty"`
	Platform           string                   `json:"platform"`
	Type               string                   `json:"type"`
	Credentials        map[string]any           `json:"credentials"`
	Extra              map[string]any           `json:"extra,omitempty"`
	ProxyKey           *string                  `json:"proxy_key,omitempty"`
	Concurrency        int                      `json:"concurrency"`
	Priority           int                      `json:"priority"`
	RateMultiplier     *float64                 `json:"rate_multiplier,omitempty"`
	ExpiresAt          *int64                   `json:"expires_at,omitempty"`
	AutoPauseOnExpired *bool                    `json:"auto_pause_on_expired,omitempty"`
	ManagedProxy       *DataManagedProxyBinding `json:"managed_proxy,omitempty"`
}

type DataManagedProxyAssignment struct {
	ProviderConfigID int64   `json:"provider_config_id"`
	Country          *string `json:"country,omitempty"`
	State            *string `json:"state,omitempty"`
	City             *string `json:"city,omitempty"`
	Strict           *bool   `json:"strict,omitempty"`
}

type DataImportRequest struct {
	Data                   DataPayload                 `json:"data"`
	SkipDefaultGroupBind   *bool                       `json:"skip_default_group_bind"`
	ManagedProxyAssignment *DataManagedProxyAssignment `json:"managed_proxy_assignment,omitempty"`
}

type DataImportResult struct {
	ProxyCreated           int                       `json:"proxy_created"`
	ProxyReused            int                       `json:"proxy_reused"`
	ProxyFailed            int                       `json:"proxy_failed"`
	AccountCreated         int                       `json:"account_created"`
	AccountFailed          int                       `json:"account_failed"`
	ManagedProviderCreated int                       `json:"managed_provider_created,omitempty"`
	ManagedProviderFailed  int                       `json:"managed_provider_failed,omitempty"`
	AccountResults         []DataAccountImportResult `json:"account_results,omitempty"`
	Errors                 []DataImportError         `json:"errors,omitempty"`
}

type DataAccountImportResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Managed bool   `json:"managed"`
	Message string `json:"message,omitempty"`
}

type DataImportError struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	ProxyKey string `json:"proxy_key,omitempty"`
	Message  string `json:"message"`
}

func buildProxyKey(protocol, host string, port int, username, password string) string {
	return fmt.Sprintf("%s|%s|%d|%s|%s", strings.TrimSpace(protocol), strings.TrimSpace(host), port, strings.TrimSpace(username), strings.TrimSpace(password))
}

func managedProviderKey(id int64) string {
	return fmt.Sprintf("catproxies-%d", id)
}

func (h *AccountHandler) ExportData(c *gin.Context) {
	ctx := c.Request.Context()

	selectedIDs, err := parseAccountIDs(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	accounts, err := h.resolveExportAccounts(ctx, selectedIDs, c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// 排除 spark 影子账号:影子不持凭据,通用凭据型导出无法表达父子链接、导入侧又强制 credentials
	// 非空——若混入会产出无法还原的坏备份(导入即失败)。影子的独立调度配置(priority/并发/分组/
	// status,管理员可单独调)随之不进备份,还原后需在重建的影子上重新调优;前端按 skipped_shadows
	// 提示用户(外审第5轮发现、第6轮裁决:保持排除 + 警告,不做完整往返)。
	skippedShadows := 0
	exportable := make([]service.Account, 0, len(accounts))
	for i := range accounts {
		if accounts[i].IsCredentialShadow() {
			skippedShadows++
			continue
		}
		exportable = append(exportable, accounts[i])
	}
	accounts = exportable
	if skippedShadows > 0 {
		slog.Info("export_skipped_spark_shadows", "count", skippedShadows)
	}

	includeProxies, err := parseIncludeProxies(c)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	managedByAccount := make(map[int64]*service.ManagedProxyAccountDTO)
	managedProxyIDs := make(map[int64]struct{})
	managedProviderIDs := make(map[int64]struct{})
	if h.managedAccountProvision != nil {
		for i := range accounts {
			managed, managedErr := h.managedAccountProvision.GetManagedAccount(ctx, accounts[i].ID)
			if managedErr != nil {
				if errors.Is(managedErr, service.ErrManagedProxyLeaseNotFound) {
					continue
				}
				response.ErrorFrom(c, managedErr)
				return
			}
			managedByAccount[accounts[i].ID] = managed
			managedProxyIDs[managed.Lease.ProxyID] = struct{}{}
			managedProviderIDs[managed.Lease.ProviderConfigID] = struct{}{}
		}
	}

	var proxies []service.Proxy
	if includeProxies {
		proxies, err = h.resolveExportProxies(ctx, accounts)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		staticProxies := proxies[:0]
		for i := range proxies {
			if _, managed := managedProxyIDs[proxies[i].ID]; managed {
				continue
			}
			staticProxies = append(staticProxies, proxies[i])
		}
		proxies = staticProxies
	} else {
		proxies = []service.Proxy{}
	}

	// 构建 id→name 映射，用于导出备用代理 name
	proxyNameByID := make(map[int64]string, len(proxies))
	for i := range proxies {
		proxyNameByID[proxies[i].ID] = proxies[i].Name
	}

	proxyKeyByID := make(map[int64]string, len(proxies))
	dataProxies := make([]DataProxy, 0, len(proxies))
	for i := range proxies {
		p := proxies[i]
		key := buildProxyKey(p.Protocol, p.Host, p.Port, p.Username, p.Password)
		proxyKeyByID[p.ID] = key

		var expiresAt *int64
		if p.ExpiresAt != nil {
			v := p.ExpiresAt.Unix()
			expiresAt = &v
		}
		var backupProxyName string
		if p.BackupProxyID != nil {
			backupProxyName = proxyNameByID[*p.BackupProxyID]
		}
		dataProxies = append(dataProxies, DataProxy{
			ProxyKey:        key,
			Name:            p.Name,
			Protocol:        p.Protocol,
			Host:            p.Host,
			Port:            p.Port,
			Username:        p.Username,
			Password:        p.Password,
			Status:          p.Status,
			ExpiresAt:       expiresAt,
			FallbackMode:    p.FallbackMode,
			BackupProxyName: backupProxyName,
			ExpiryWarnDays:  p.ExpiryWarnDays,
		})
	}

	dataAccounts := make([]DataAccount, 0, len(accounts))
	for i := range accounts {
		acc := accounts[i]
		var proxyKey *string
		if acc.ProxyID != nil {
			if key, ok := proxyKeyByID[*acc.ProxyID]; ok {
				proxyKey = &key
			}
		}
		var managedBinding *DataManagedProxyBinding
		if managed := managedByAccount[acc.ID]; managed != nil {
			strict := managed.Lease.Strict
			managedBinding = &DataManagedProxyBinding{
				ProviderKey: managedProviderKey(managed.Lease.ProviderConfigID),
				Country:     managed.Lease.TargetCountry, State: managed.Lease.TargetState,
				City: managed.Lease.TargetCity, Strict: &strict,
			}
		}
		var expiresAt *int64
		if acc.ExpiresAt != nil {
			v := acc.ExpiresAt.Unix()
			expiresAt = &v
		}
		dataAccounts = append(dataAccounts, DataAccount{
			Name:               acc.Name,
			Notes:              acc.Notes,
			Platform:           acc.Platform,
			Type:               acc.Type,
			Credentials:        acc.Credentials,
			Extra:              acc.Extra,
			ProxyKey:           proxyKey,
			Concurrency:        acc.Concurrency,
			Priority:           acc.Priority,
			RateMultiplier:     acc.RateMultiplier,
			ExpiresAt:          expiresAt,
			AutoPauseOnExpired: &acc.AutoPauseOnExpired,
			ManagedProxy:       managedBinding,
		})
	}

	managedProviders := make([]DataManagedProxyProvider, 0, len(managedProviderIDs))
	for providerID := range managedProviderIDs {
		provider, providerErr := h.managedAccountProvision.GetProviderForBackup(ctx, providerID)
		if providerErr != nil {
			response.ErrorFrom(c, providerErr)
			return
		}
		managedProviders = append(managedProviders, DataManagedProxyProvider{
			ProviderKey: managedProviderKey(provider.ID), Name: provider.Name,
			Protocol: provider.Protocol, Host: provider.Host, BaseUsername: provider.BaseUsername,
			Password: provider.Password, DefaultCountry: provider.DefaultCountry,
			DefaultState: provider.DefaultState, DefaultCity: provider.DefaultCity,
			LifetimeMinutes: provider.LifetimeMinutes, Strict: provider.Strict,
			Status: provider.Status, IsDefault: provider.IsDefault,
		})
	}
	sort.Slice(managedProviders, func(i, j int) bool { return managedProviders[i].ProviderKey < managedProviders[j].ProviderKey })

	payload := DataPayload{
		ExportedAt:            time.Now().UTC().Format(time.RFC3339),
		Proxies:               dataProxies,
		Accounts:              dataAccounts,
		ManagedProxyProviders: managedProviders,
		SkippedShadows:        skippedShadows,
	}
	if len(managedProviders) > 0 {
		payload.Type = managedDataType
		payload.Version = managedDataVersion
	}

	response.Success(c, payload)
}

func (h *AccountHandler) ImportData(c *gin.Context) {
	var req DataImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := validateDataHeader(req.Data); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	executeAdminIdempotentJSON(c, "admin.accounts.import_data", req, service.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		return h.importData(ctx, req)
	})
}

func (h *AccountHandler) importData(ctx context.Context, req DataImportRequest) (DataImportResult, error) {
	skipDefaultGroupBind := true
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}

	dataPayload := req.Data
	result := DataImportResult{}
	providerKeyToID := make(map[string]int64, len(dataPayload.ManagedProxyProviders))
	restoredProviderStatus := make(map[int64]string, len(dataPayload.ManagedProxyProviders))

	managedAccountRequested := req.ManagedProxyAssignment != nil
	if !managedAccountRequested {
		for i := range dataPayload.Accounts {
			if dataPayload.Accounts[i].ManagedProxy != nil {
				managedAccountRequested = true
				break
			}
		}
	}
	needsManagedProvisioning := managedAccountRequested || len(dataPayload.ManagedProxyProviders) > 0
	if managedAccountRequested && len(dataPayload.Accounts) == 0 {
		return result, infraerrors.BadRequest("MANAGED_IMPORT_SIZE_INVALID", "managed proxy imports require at least one account")
	}
	if needsManagedProvisioning && h.managedAccountProvision == nil {
		return result, infraerrors.BadRequest("MANAGED_PROXY_NOT_CONFIGURED", "managed proxy provisioning is not configured")
	}
	if req.ManagedProxyAssignment != nil {
		if err := h.managedAccountProvision.ValidateProviderForAssignment(ctx, req.ManagedProxyAssignment.ProviderConfigID); err != nil {
			return result, err
		}
	}
	providerSpecs := make(map[string]DataManagedProxyProvider, len(dataPayload.ManagedProxyProviders))
	providerKeys := make([]string, 0, len(dataPayload.ManagedProxyProviders))
	for i := range dataPayload.ManagedProxyProviders {
		provider := dataPayload.ManagedProxyProviders[i]
		key := strings.TrimSpace(provider.ProviderKey)
		if key == "" {
			result.ManagedProviderFailed++
			result.Errors = append(result.Errors, DataImportError{Kind: "managed_provider", Name: provider.Name, Message: "provider_key is required"})
			continue
		}
		provider.ProviderKey = key
		if existing, ok := providerSpecs[key]; ok {
			if !reflect.DeepEqual(existing, provider) {
				return result, infraerrors.BadRequest("MANAGED_PROVIDER_KEY_CONFLICT", fmt.Sprintf("managed provider key %q has conflicting definitions", key))
			}
			continue
		}
		providerSpecs[key] = provider
		providerKeys = append(providerKeys, key)
	}
	for _, providerKey := range providerKeys {
		provider := providerSpecs[providerKey]
		created, restoreErr := h.managedAccountProvision.RestoreProvider(ctx, service.CatProxyProviderBackup{
			Name: provider.Name, Protocol: provider.Protocol, Host: provider.Host,
			BaseUsername: provider.BaseUsername, Password: provider.Password,
			DefaultCountry: provider.DefaultCountry, DefaultState: provider.DefaultState,
			DefaultCity: provider.DefaultCity, LifetimeMinutes: provider.LifetimeMinutes,
			Strict: provider.Strict, Status: provider.Status, IsDefault: provider.IsDefault,
		})
		if restoreErr != nil {
			result.ManagedProviderFailed++
			result.Errors = append(result.Errors, DataImportError{Kind: "managed_provider", Name: provider.Name, Message: restoreErr.Error()})
			continue
		}
		providerKeyToID[providerKey] = created.ID
		if provider.Status != "" && provider.Status != service.CatProxiesStatusActive {
			restoredProviderStatus[created.ID] = provider.Status
		}
		result.ManagedProviderCreated++
	}

	existingProxies, err := h.listAllProxies(ctx)
	if err != nil {
		return result, err
	}

	proxyKeyToID := make(map[string]int64, len(existingProxies))
	// proxyNameToID 用于 backup_proxy_name 反查：DB 已有 + 本批次新建均会写入
	proxyNameToID := make(map[string]int64, len(existingProxies))
	for i := range existingProxies {
		p := existingProxies[i]
		key := buildProxyKey(p.Protocol, p.Host, p.Port, p.Username, p.Password)
		proxyKeyToID[key] = p.ID
		if p.Name != "" {
			proxyNameToID[p.Name] = p.ID
		}
	}

	for i := range dataPayload.Proxies {
		item := dataPayload.Proxies[i]
		key := item.ProxyKey
		if key == "" {
			key = buildProxyKey(item.Protocol, item.Host, item.Port, item.Username, item.Password)
		}
		if err := validateDataProxy(item); err != nil {
			result.ProxyFailed++
			result.Errors = append(result.Errors, DataImportError{
				Kind:     "proxy",
				Name:     item.Name,
				ProxyKey: key,
				Message:  err.Error(),
			})
			continue
		}
		normalizedStatus := normalizeProxyStatus(item.Status)
		if existingID, ok := proxyKeyToID[key]; ok {
			proxyKeyToID[key] = existingID
			result.ProxyReused++
			if normalizedStatus != "" {
				if proxy, getErr := h.adminService.GetProxy(ctx, existingID); getErr == nil && proxy != nil && proxy.Status != normalizedStatus {
					// 同步 status 时传入完整字段，避免零值覆盖已存在代理的有效期/fallback 配置。
					var existingExpiresAt *time.Time
					if item.ExpiresAt != nil {
						t := time.Unix(*item.ExpiresAt, 0).UTC()
						existingExpiresAt = &t
					}
					existingFallbackMode := item.FallbackMode
					if existingFallbackMode == "" {
						existingFallbackMode = service.FallbackModeNone
					}
					var existingBackupProxyID *int64
					if item.BackupProxyName != "" {
						if bid, ok := proxyNameToID[item.BackupProxyName]; ok {
							existingBackupProxyID = &bid
						}
					}
					_, _ = h.adminService.UpdateProxy(ctx, existingID, &service.UpdateProxyInput{
						Status:         normalizedStatus,
						ExpiresAt:      existingExpiresAt,
						FallbackMode:   existingFallbackMode,
						BackupProxyID:  existingBackupProxyID,
						ExpiryWarnDays: item.ExpiryWarnDays,
						Name:           proxy.Name,
						Protocol:       proxy.Protocol,
						Host:           proxy.Host,
						Port:           proxy.Port,
						Username:       proxy.Username,
						Password:       proxy.Password,
					})
				}
			}
			continue
		}

		// 解析 expires_at（unix 秒 → *time.Time）
		var expiresAt *time.Time
		if item.ExpiresAt != nil {
			t := time.Unix(*item.ExpiresAt, 0).UTC()
			expiresAt = &t
		}

		// 解析 backup_proxy_name → backup_proxy_id
		fallbackMode := item.FallbackMode
		var backupProxyID *int64
		if item.BackupProxyName != "" {
			if bid, ok := proxyNameToID[item.BackupProxyName]; ok {
				backupProxyID = &bid
			} else {
				// 查不到备用代理：降级 fallback_mode=none，记录 warning
				fallbackMode = service.FallbackModeNone
				result.Errors = append(result.Errors, DataImportError{
					Kind:     "proxy",
					Name:     item.Name,
					ProxyKey: key,
					Message:  fmt.Sprintf("backup_proxy_name %q not found, fallback_mode downgraded to none", item.BackupProxyName),
				})
			}
		}

		created, createErr := h.adminService.CreateProxy(ctx, &service.CreateProxyInput{
			Name:           defaultProxyName(item.Name),
			Protocol:       item.Protocol,
			Host:           item.Host,
			Port:           item.Port,
			Username:       item.Username,
			Password:       item.Password,
			ExpiresAt:      expiresAt,
			FallbackMode:   fallbackMode,
			BackupProxyID:  backupProxyID,
			ExpiryWarnDays: item.ExpiryWarnDays,
		})
		if createErr != nil {
			result.ProxyFailed++
			result.Errors = append(result.Errors, DataImportError{
				Kind:     "proxy",
				Name:     item.Name,
				ProxyKey: key,
				Message:  createErr.Error(),
			})
			continue
		}
		proxyKeyToID[key] = created.ID
		// 把新建代理的 name 也加入反查表，供后续批内代理引用
		if created.Name != "" {
			proxyNameToID[created.Name] = created.ID
		}
		result.ProxyCreated++

		if normalizedStatus != "" && normalizedStatus != created.Status {
			// 新建后同步 status 时，传入完整字段，避免零值覆盖刚创建的有效期/fallback 配置。
			_, _ = h.adminService.UpdateProxy(ctx, created.ID, &service.UpdateProxyInput{
				Status:         normalizedStatus,
				ExpiresAt:      expiresAt,
				FallbackMode:   fallbackMode,
				BackupProxyID:  backupProxyID,
				ExpiryWarnDays: item.ExpiryWarnDays,
				Name:           created.Name,
				Protocol:       created.Protocol,
				Host:           created.Host,
				Port:           created.Port,
				Username:       created.Username,
				Password:       created.Password,
			})
		}
	}

	type managedPreparation struct {
		requested bool
		candidate service.ManagedProxyCandidate
		err       error
	}
	managedPreparations := make([]managedPreparation, len(dataPayload.Accounts))
	if managedAccountRequested {
		semaphore := make(chan struct{}, managedImportParallel)
		var prepareGroup sync.WaitGroup
		for i := range dataPayload.Accounts {
			item := dataPayload.Accounts[i]
			if validateDataAccount(item) != nil || (item.ProxyKey != nil && *item.ProxyKey != "") {
				continue
			}
			providerConfigID := int64(0)
			target := service.CatProxiesProxyTarget{}
			if item.ManagedProxy != nil {
				providerConfigID = providerKeyToID[item.ManagedProxy.ProviderKey]
				target = service.CatProxiesProxyTarget{Country: item.ManagedProxy.Country, State: item.ManagedProxy.State, City: item.ManagedProxy.City, Strict: item.ManagedProxy.Strict}
			} else if req.ManagedProxyAssignment != nil {
				providerConfigID = req.ManagedProxyAssignment.ProviderConfigID
				target = service.CatProxiesProxyTarget{Country: req.ManagedProxyAssignment.Country, State: req.ManagedProxyAssignment.State, City: req.ManagedProxyAssignment.City, Strict: req.ManagedProxyAssignment.Strict}
			} else {
				continue
			}
			managedPreparations[i].requested = true
			if providerConfigID == 0 {
				managedPreparations[i].err = errors.New("managed proxy provider_key not found")
				continue
			}
			prepareGroup.Add(1)
			go func(index int, configID int64, requestedTarget service.CatProxiesProxyTarget) {
				defer prepareGroup.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					managedPreparations[index].err = ctx.Err()
					return
				}
				managedPreparations[index].candidate, managedPreparations[index].err = h.managedAccountProvision.Prepare(ctx, configID, requestedTarget)
			}(i, providerConfigID, target)
		}
		prepareGroup.Wait()
	}

	// 收集需要异步设置隐私的 Antigravity OAuth 账号
	var privacyAccounts []*service.Account

	for i := range dataPayload.Accounts {
		item := dataPayload.Accounts[i]
		if err := validateDataAccount(item); err != nil {
			result.AccountFailed++
			result.AccountResults = append(result.AccountResults, DataAccountImportResult{Name: item.Name, Status: "invalid", Message: err.Error()})
			result.Errors = append(result.Errors, DataImportError{
				Kind:    "account",
				Name:    item.Name,
				Message: err.Error(),
			})
			continue
		}

		var proxyID *int64
		if item.ProxyKey != nil && *item.ProxyKey != "" {
			if id, ok := proxyKeyToID[*item.ProxyKey]; ok {
				proxyID = &id
			} else {
				result.AccountFailed++
				result.AccountResults = append(result.AccountResults, DataAccountImportResult{Name: item.Name, Status: "invalid", Message: "proxy_key not found"})
				result.Errors = append(result.Errors, DataImportError{
					Kind:     "account",
					Name:     item.Name,
					ProxyKey: *item.ProxyKey,
					Message:  "proxy_key not found",
				})
				continue
			}
		}

		enrichCredentialsFromIDToken(&item)

		accountInput := &service.CreateAccountInput{
			Name:                 item.Name,
			Notes:                item.Notes,
			Platform:             item.Platform,
			Type:                 item.Type,
			Credentials:          item.Credentials,
			Extra:                item.Extra,
			ProxyID:              proxyID,
			Concurrency:          item.Concurrency,
			Priority:             item.Priority,
			RateMultiplier:       item.RateMultiplier,
			GroupIDs:             nil,
			ExpiresAt:            item.ExpiresAt,
			AutoPauseOnExpired:   item.AutoPauseOnExpired,
			SkipDefaultGroupBind: skipDefaultGroupBind,
		}

		var created *service.Account
		var createErr error
		managedCreated := false
		preparation := managedPreparations[i]
		if proxyID == nil && preparation.requested {
			candidate := preparation.candidate
			createErr = preparation.err
			if createErr == nil {
				if desiredStatus := restoredProviderStatus[candidate.ProviderConfigID]; desiredStatus == service.CatProxiesStatusDisabled || desiredStatus == service.CatProxiesStatusCredentialError {
					ready := false
					candidate.InitialReady = &ready
				}
				created, createErr = h.managedAccountProvision.Create(ctx, accountInput, candidate)
				managedCreated = createErr == nil
			}
		} else {
			created, createErr = h.adminService.CreateAccount(ctx, accountInput)
		}
		if createErr != nil {
			result.AccountFailed++
			status := "failed"
			if preparation.requested && preparation.err != nil {
				status = "proxy_probe_failed"
			}
			result.AccountResults = append(result.AccountResults, DataAccountImportResult{Name: item.Name, Status: status, Managed: preparation.requested, Message: createErr.Error()})
			result.Errors = append(result.Errors, DataImportError{
				Kind:    "account",
				Name:    item.Name,
				Message: createErr.Error(),
			})
			continue
		}
		// Managed provisioning bypasses AdminService.CreateAccount, so run the same
		// post-commit privacy setup here for both supported OAuth platforms.
		if created.Type == service.AccountTypeOAuth && (created.Platform == service.PlatformAntigravity || (managedCreated && created.Platform == service.PlatformOpenAI)) {
			privacyAccounts = append(privacyAccounts, created)
		}
		h.scheduleGrokImportProbe(created)
		result.AccountCreated++
		result.AccountResults = append(result.AccountResults, DataAccountImportResult{Name: item.Name, Status: "created", Managed: managedCreated})
	}

	for providerID, status := range restoredProviderStatus {
		if err := h.managedAccountProvision.SetRestoredProviderStatus(ctx, providerID, status); err != nil {
			return result, err
		}
	}

	// 异步设置 Antigravity 隐私，避免大量导入时阻塞请求
	if len(privacyAccounts) > 0 {
		adminSvc := h.adminService
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("import_antigravity_privacy_panic", "recover", r)
				}
			}()
			bgCtx := context.Background()
			for _, acc := range privacyAccounts {
				switch acc.Platform {
				case service.PlatformOpenAI:
					adminSvc.EnsureOpenAIPrivacy(bgCtx, acc)
				case service.PlatformAntigravity:
					adminSvc.ForceAntigravityPrivacy(bgCtx, acc)
				}
			}
			slog.Info("import_antigravity_privacy_done", "count", len(privacyAccounts))
		}()
	}

	return result, nil
}

func (h *AccountHandler) listAllProxies(ctx context.Context) ([]service.Proxy, error) {
	page := 1
	pageSize := dataPageCap
	var out []service.Proxy
	for {
		items, total, err := h.adminService.ListProxies(ctx, page, pageSize, "", "", "", "created_at", "desc")
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if len(out) >= int(total) || len(items) == 0 {
			break
		}
		page++
	}
	return out, nil
}

func (h *AccountHandler) listAccountsFiltered(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode, sortBy, sortOrder string) ([]service.Account, error) {
	page := 1
	pageSize := dataPageCap
	var out []service.Account
	for {
		items, total, err := h.adminService.ListAccounts(ctx, page, pageSize, platform, accountType, status, search, groupID, privacyMode, sortBy, sortOrder)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if len(out) >= int(total) || len(items) == 0 {
			break
		}
		page++
	}
	return out, nil
}

func (h *AccountHandler) resolveExportAccounts(ctx context.Context, ids []int64, c *gin.Context) ([]service.Account, error) {
	if len(ids) > 0 {
		accounts, err := h.adminService.GetAccountsByIDs(ctx, ids)
		if err != nil {
			return nil, err
		}
		out := make([]service.Account, 0, len(accounts))
		for _, acc := range accounts {
			if acc == nil {
				continue
			}
			out = append(out, *acc)
		}
		return out, nil
	}

	platform := c.Query("platform")
	accountType := c.Query("type")
	status := c.Query("status")
	privacyMode := strings.TrimSpace(c.Query("privacy_mode"))
	search := strings.TrimSpace(c.Query("search"))
	sortBy := c.DefaultQuery("sort_by", "name")
	sortOrder := c.DefaultQuery("sort_order", "asc")
	if len(search) > 100 {
		search = search[:100]
	}

	groupID := int64(0)
	if groupIDStr := c.Query("group"); groupIDStr != "" {
		if groupIDStr == accountListGroupUngroupedQueryValue {
			groupID = service.AccountListGroupUngrouped
		} else {
			parsedGroupID, parseErr := strconv.ParseInt(groupIDStr, 10, 64)
			if parseErr != nil || parsedGroupID <= 0 {
				return nil, infraerrors.BadRequest("INVALID_GROUP_FILTER", "invalid group filter")
			}
			groupID = parsedGroupID
		}
	}

	return h.listAccountsFiltered(ctx, platform, accountType, status, search, groupID, privacyMode, sortBy, sortOrder)
}

func (h *AccountHandler) resolveExportProxies(ctx context.Context, accounts []service.Account) ([]service.Proxy, error) {
	if len(accounts) == 0 {
		return []service.Proxy{}, nil
	}

	seen := make(map[int64]struct{})
	ids := make([]int64, 0)
	for i := range accounts {
		if accounts[i].ProxyID == nil {
			continue
		}
		id := *accounts[i].ProxyID
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return []service.Proxy{}, nil
	}

	return h.adminService.GetProxiesByIDs(ctx, ids)
}

func parseAccountIDs(c *gin.Context) ([]int64, error) {
	values := c.QueryArray("ids")
	if len(values) == 0 {
		raw := strings.TrimSpace(c.Query("ids"))
		if raw != "" {
			values = []string{raw}
		}
	}
	if len(values) == 0 {
		return nil, nil
	}

	ids := make([]int64, 0, len(values))
	for _, item := range values {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("invalid account id: %s", part)
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func parseIncludeProxies(c *gin.Context) (bool, error) {
	raw := strings.TrimSpace(strings.ToLower(c.Query("include_proxies")))
	if raw == "" {
		return true, nil
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return true, fmt.Errorf("invalid include_proxies value: %s", raw)
	}
}

func validateDataHeader(payload DataPayload) error {
	if payload.Type == managedDataType {
		if payload.Version != managedDataVersion {
			return fmt.Errorf("unsupported managed data version: %d", payload.Version)
		}
	} else {
		if payload.Type != "" && payload.Type != dataType && payload.Type != legacyDataType {
			return fmt.Errorf("unsupported data type: %s", payload.Type)
		}
		if payload.Version != 0 && payload.Version != dataVersion {
			return fmt.Errorf("unsupported data version: %d", payload.Version)
		}
	}
	if payload.Proxies == nil {
		return errors.New("proxies is required")
	}
	if payload.Accounts == nil {
		return errors.New("accounts is required")
	}
	return nil
}

func validateDataProxy(item DataProxy) error {
	if strings.TrimSpace(item.Protocol) == "" {
		return errors.New("proxy protocol is required")
	}
	if strings.TrimSpace(item.Host) == "" {
		return errors.New("proxy host is required")
	}
	if item.Port <= 0 || item.Port > 65535 {
		return errors.New("proxy port is invalid")
	}
	switch item.Protocol {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("proxy protocol is invalid: %s", item.Protocol)
	}
	if item.Status != "" {
		normalizedStatus := normalizeProxyStatus(item.Status)
		if normalizedStatus != service.StatusActive && normalizedStatus != "inactive" {
			return fmt.Errorf("proxy status is invalid: %s", item.Status)
		}
	}
	return nil
}

func validateDataAccount(item DataAccount) error {
	if strings.TrimSpace(item.Name) == "" {
		return errors.New("account name is required")
	}
	if strings.TrimSpace(item.Platform) == "" {
		return errors.New("account platform is required")
	}
	if strings.TrimSpace(item.Type) == "" {
		return errors.New("account type is required")
	}
	if len(item.Credentials) == 0 {
		return errors.New("account credentials is required")
	}
	switch item.Type {
	case service.AccountTypeOAuth, service.AccountTypeSetupToken, service.AccountTypeAPIKey, service.AccountTypeUpstream:
	default:
		return fmt.Errorf("account type is invalid: %s", item.Type)
	}
	if item.RateMultiplier != nil && *item.RateMultiplier < 0 {
		return errors.New("rate_multiplier must be >= 0")
	}
	if item.Concurrency < 0 {
		return errors.New("concurrency must be >= 0")
	}
	if item.Priority < 0 {
		return errors.New("priority must be >= 0")
	}
	return nil
}

func defaultProxyName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "imported-proxy"
	}
	return name
}

// enrichCredentialsFromIDToken performs best-effort extraction of user info fields
// (email, plan_type, chatgpt_account_id, etc.) from id_token in credentials.
// Only applies to OpenAI OAuth accounts. Skips expired token errors silently.
// Existing credential values are never overwritten — only missing fields are filled.
func enrichCredentialsFromIDToken(item *DataAccount) {
	if item.Credentials == nil {
		return
	}
	// Only enrich OpenAI OAuth accounts
	platform := strings.ToLower(strings.TrimSpace(item.Platform))
	if platform != service.PlatformOpenAI {
		return
	}
	if strings.ToLower(strings.TrimSpace(item.Type)) != service.AccountTypeOAuth {
		return
	}

	idToken, _ := item.Credentials["id_token"].(string)
	if strings.TrimSpace(idToken) == "" {
		return
	}

	// DecodeIDToken skips expiry validation — safe for imported data
	claims, err := openai.DecodeIDToken(idToken)
	if err != nil {
		slog.Debug("import_enrich_id_token_decode_failed", "account", item.Name, "error", err)
		return
	}

	userInfo := claims.GetUserInfo()
	if userInfo == nil {
		return
	}

	// Fill missing fields only (never overwrite existing values)
	setIfMissing := func(key, value string) {
		if value == "" {
			return
		}
		if existing, _ := item.Credentials[key].(string); existing == "" {
			item.Credentials[key] = value
		}
	}

	setIfMissing("email", userInfo.Email)
	setIfMissing("plan_type", userInfo.PlanType)
	setIfMissing("chatgpt_account_id", userInfo.ChatGPTAccountID)
	setIfMissing("chatgpt_user_id", userInfo.ChatGPTUserID)
	setIfMissing("organization_id", userInfo.OrganizationID)
}

func normalizeProxyStatus(status string) string {
	normalized := strings.TrimSpace(strings.ToLower(status))
	switch normalized {
	case "":
		return ""
	case service.StatusActive:
		return service.StatusActive
	case "inactive", service.StatusDisabled:
		return "inactive"
	case "expired":
		// 导入 expired 代理按 inactive 处理，避免导入即触发到期改投逻辑
		return "inactive"
	default:
		return normalized
	}
}
