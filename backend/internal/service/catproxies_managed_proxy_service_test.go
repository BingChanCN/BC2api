package service

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func catProxiesString(value string) *string { return &value }
func catProxiesBool(value bool) *bool       { return &value }
func catProxiesTargetingForTest(t *testing.T) *CatProxiesTargetingService {
	t.Helper()
	targeting, err := NewCatProxiesTargetingService()
	require.NoError(t, err)
	return targeting
}

func TestBuildCatProxiesUsername_RealShape(t *testing.T) {
	target := CatProxiesProxyTarget{
		Country: catProxiesString("us"),
		State:   catProxiesString("california"),
		City:    catProxiesString("losangeles"),
	}
	username, err := BuildCatProxiesUsername("customer-type-residential", target, "opaque123", 60)
	require.NoError(t, err)
	require.Equal(t, "customer-type-residential-country-us-state-california-city-losangeles-lifetime-60-session-opaque123-strict-on", username)
}

func TestBuildCatProxiesUsername_DuplicateResidentialTypeNormalized(t *testing.T) {
	username, err := BuildCatProxiesUsername("customer-type-residential-type-residential", CatProxiesProxyTarget{}, "opaque123", 60)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(username, "-type-residential"))
	require.Equal(t, "customer-type-residential-lifetime-60-session-opaque123", username)
}

func TestBuildCatProxiesUsername_NormalizesRegionAndDropsEmptyOptionals(t *testing.T) {
	username, err := BuildCatProxiesUsername("customer", CatProxiesProxyTarget{
		Country: catProxiesString(" US "),
		State:   catProxiesString("  "),
		City:    catProxiesString(""),
	}, "opaque123", 60)
	require.NoError(t, err)
	require.Contains(t, username, "-country-us-")
	require.NotContains(t, username, "-state-")
	require.NotContains(t, username, "-city-")
	require.NotContains(t, username, "-country--")
}

func TestBuildCatProxiesUsername_ExplicitNonStrictTarget(t *testing.T) {
	username, err := BuildCatProxiesUsername("customer", CatProxiesProxyTarget{
		Country: catProxiesString("us"),
		Strict:  catProxiesBool(false),
	}, "opaque123", 60)
	require.NoError(t, err)
	require.Equal(t, "customer-type-residential-country-us-lifetime-60-session-opaque123-strict-off", username)
}

func TestDeriveCatProxiesProxy_ProtocolsPortsTimingAndFallback(t *testing.T) {
	now := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	for _, test := range []struct {
		protocol string
		port     int
	}{
		{protocol: CatProxiesProtocolHTTP, port: CatProxiesHTTPStickyPort},
		{protocol: CatProxiesProtocolSOCKS5H, port: CatProxiesSOCKS5HStickyPort},
	} {
		t.Run(test.protocol, func(t *testing.T) {
			derived, err := DeriveCatProxiesProxy(&CatProxyProviderConfig{
				ID:              9,
				Protocol:        test.protocol,
				Host:            "proxy.example.com",
				BaseUsername:    "customer",
				Password:        "secret",
				LifetimeMinutes: 60,
			}, CatProxiesProxyTarget{}, now)
			require.NoError(t, err)
			require.Equal(t, test.port, derived.Proxy.Port)
			require.Equal(t, test.protocol, derived.Proxy.Protocol)
			require.Equal(t, "catproxies-9-managed", derived.Proxy.Name)
			require.NotContains(t, derived.Proxy.Name, derived.SessionID)
			require.Equal(t, FallbackModeNone, derived.Proxy.FallbackMode)
			require.Equal(t, now.Add(55*time.Minute), derived.Timing.RotateAt)
			require.Equal(t, now.Add(60*time.Minute), derived.Timing.HardExpiresAt)
			require.NotNil(t, derived.Proxy.ExpiresAt)
			require.Equal(t, now.Add(60*time.Minute), *derived.Proxy.ExpiresAt)
		})
	}
}

func TestCatProxiesManagedProxyService_ProbeErrorRedactsDerivedCredentials(t *testing.T) {
	cause := errors.New("dial http://customer-lifetime-60-session-sensitive:super-secret@proxy.example.com:10000 failed")
	prober := &catProxiesProbeStub{err: cause}
	service := NewCatProxiesManagedProxyService(nil, nil, prober, nil, nil)
	_, err := service.ProbeProxy(context.Background(), &Proxy{
		Protocol: "http", Host: "proxy.example.com", Port: 10000,
		Username: "customer-lifetime-60-session-sensitive", Password: "super-secret",
	}, CatProxiesProxyTarget{})
	require.Error(t, err)
	require.ErrorIs(t, err, cause)
	require.NotContains(t, err.Error(), "session-sensitive")
	require.NotContains(t, err.Error(), "super-secret")
}

func TestComputeCatProxiesLeaseTiming_LeadBounds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	minimum, err := ComputeCatProxiesLeaseTiming(now, 15)
	require.NoError(t, err)
	require.Equal(t, now.Add(14*time.Minute), minimum.RotateAt)

	maximum, err := ComputeCatProxiesLeaseTiming(now, 1440)
	require.NoError(t, err)
	require.Equal(t, now.Add(1435*time.Minute), maximum.RotateAt)
	require.Equal(t, now.Add(1440*time.Minute), maximum.HardExpiresAt)
}

func TestDeriveCatProxiesProxy_NoRegionDisablesStrict(t *testing.T) {
	derived, err := DeriveCatProxiesProxy(&CatProxyProviderConfig{
		ID:              1,
		Protocol:        CatProxiesProtocolHTTP,
		Host:            "proxy.example.com",
		BaseUsername:    "customer",
		Password:        "secret",
		LifetimeMinutes: 60,
		Strict:          true,
	}, CatProxiesProxyTarget{}, time.Now())
	require.NoError(t, err)
	require.NotNil(t, derived.Target.Strict)
	require.False(t, *derived.Target.Strict)
	require.NotContains(t, derived.Proxy.Username, "-country-")
	require.NotContains(t, derived.Proxy.Username, "-state-")
	require.NotContains(t, derived.Proxy.Username, "-city-")
}

func TestValidateCatProxiesRegion_RejectsInvalidTokens(t *testing.T) {
	for _, test := range []struct {
		name    string
		country *string
		state   *string
		city    *string
	}{
		{name: "uppercase country", country: catProxiesString("US")},
		{name: "non ISO2 country", country: catProxiesString("usa")},
		{name: "uppercase state", state: catProxiesString("California")},
		{name: "space in state", state: catProxiesString("new york")},
		{name: "hyphen in city", city: catProxiesString("los-angeles")},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, ValidateCatProxiesRegion(test.country, test.state, test.city))
		})
	}
	require.NoError(t, ValidateCatProxiesRegion(catProxiesString("us"), catProxiesString("california"), catProxiesString("losangeles")))
}

func TestGenerateCatProxiesSessionID_IsOpaqueRandom(t *testing.T) {
	first, err := GenerateCatProxiesSessionID()
	require.NoError(t, err)
	second, err := GenerateCatProxiesSessionID()
	require.NoError(t, err)
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{32}$`), first)
	require.NotEqual(t, first, second)
}

func TestCatProxyProviderConfigDTO_RedactsPassword(t *testing.T) {
	dto := catProxyProviderConfigDTO(&CatProxyProviderConfig{ID: 1, Name: "provider", Password: "super-secret"})
	require.True(t, dto.PasswordConfigured)
	payload, err := json.Marshal(dto)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "super-secret")
	require.NotContains(t, string(payload), `"password":`)
}

func TestCatProxiesManagedProxyService_TestConfigPersistsProbeOutcome(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 1, Name: "provider", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusActive,
		Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer-type-residential",
		Password: "secret", LifetimeMinutes: 60,
	}}
	prober := &catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.8"}, latency: 37}
	svc := NewCatProxiesManagedProxyService(configRepo, &catProxiesLeaseRepoStub{}, prober, catProxiesTargetingForTest(t), nil)

	result, err := svc.TestConfig(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 37, result.LatencyMs)
	require.NotNil(t, configRepo.config.LastProbeAt)
	require.NotNil(t, configRepo.config.LastProbeLatencyMs)
	require.Equal(t, 37, *configRepo.config.LastProbeLatencyMs)
	require.Nil(t, configRepo.config.LastError)

	prober.err = errors.New("gateway unavailable")
	result, err = svc.TestConfig(context.Background(), 1)
	require.Error(t, err)
	require.Nil(t, result)
	require.NotNil(t, configRepo.config.LastProbeAt)
	require.Nil(t, configRepo.config.LastProbeLatencyMs)
	require.NotNil(t, configRepo.config.LastError)
	require.Contains(t, *configRepo.config.LastError, "gateway unavailable")
}

func TestCatProxiesManagedProxyService_BoundConfigCannotDeleteButCanChangeStatus(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID:              7,
		Name:            "bound",
		ProviderType:    CatProxiesProviderType,
		Status:          CatProxiesStatusActive,
		Protocol:        CatProxiesProtocolHTTP,
		Host:            "proxy.example.com",
		BaseUsername:    "customer-type-residential",
		Password:        "secret",
		LifetimeMinutes: 60,
	}}
	leaseRepo := &catProxiesLeaseRepoStub{bindingCount: 1}
	service := NewCatProxiesManagedProxyService(configRepo, leaseRepo, nil, catProxiesTargetingForTest(t), nil)

	err := service.DeleteConfig(context.Background(), 7)
	require.ErrorIs(t, err, ErrCatProxyProviderConfigInUse)
	require.False(t, configRepo.deleted)

	dto, err := service.UpdateConfigStatus(context.Background(), 7, CatProxiesStatusRetiring)
	require.NoError(t, err)
	require.Equal(t, CatProxiesStatusRetiring, dto.Status)
	require.Equal(t, CatProxiesStatusRetiring, configRepo.config.Status)
}

func TestCatProxiesManagedProxyServiceReactivationReplacesSessionsBeforeOpeningGates(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 7, Name: "provider", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusDisabled,
		Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer-type-residential",
		Password: "secret", LifetimeMinutes: 60,
	}}
	leaseRepo := &catProxiesLeaseRepoStub{bindingCount: 2, leases: []ManagedProxyLease{
		{AccountID: 10, ProviderConfigID: 7, TargetCountry: catProxiesString("us"), Strict: true},
		{AccountID: 11, ProviderConfigID: 7, TargetCountry: catProxiesString("us"), Strict: true},
	}}
	prober := &catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.4", CountryCode: "US"}}
	runtimeRepo := &managedProxyRuntimeRepoStub{}
	svc := NewCatProxiesManagedProxyService(configRepo, leaseRepo, prober, nil, runtimeRepo)

	dto, err := svc.UpdateConfigStatus(context.Background(), 7, CatProxiesStatusActive)
	require.NoError(t, err)
	require.Equal(t, CatProxiesStatusActive, dto.Status)
	require.Len(t, runtimeRepo.reactivations, 1)
	require.Len(t, runtimeRepo.reactivations[0], 2)
	require.Empty(t, runtimeRepo.reactivationFailures[0])
	require.Empty(t, runtimeRepo.readyCalls, "reactivation must not open every gate without replacing sessions")
}

func TestCatProxiesManagedProxyServiceReactivationKeepsProviderDisabledWhenAllProbesFail(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 7, Name: "provider", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusDisabled,
		Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer-type-residential",
		Password: "secret", LifetimeMinutes: 60,
	}}
	leaseRepo := &catProxiesLeaseRepoStub{bindingCount: 1, leases: []ManagedProxyLease{{AccountID: 10, ProviderConfigID: 7, Strict: true}}}
	runtimeRepo := &managedProxyRuntimeRepoStub{}
	svc := NewCatProxiesManagedProxyService(configRepo, leaseRepo, &catProxiesProbeStub{err: errors.New("unavailable")}, nil, runtimeRepo)

	_, err := svc.UpdateConfigStatus(context.Background(), 7, CatProxiesStatusActive)
	require.ErrorContains(t, err, "all 1 managed proxy probes failed")
	require.Equal(t, CatProxiesStatusDisabled, configRepo.config.Status)
	require.Empty(t, runtimeRepo.reactivations)
}

func TestCatProxiesManagedProxyServiceBoundSafeUpdateOnlyAffectsFutureRotation(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 7, Name: "old", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusActive,
		Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer-type-residential",
		Password: "secret", LifetimeMinutes: 60,
	}}
	leaseRepo := &catProxiesLeaseRepoStub{bindingCount: 1}
	prober := &catProxiesProbeStub{err: errors.New("safe update must not probe")}
	runtimeRepo := &managedProxyRuntimeRepoStub{}
	svc := NewCatProxiesManagedProxyService(configRepo, leaseRepo, prober, nil, runtimeRepo)

	dto, err := svc.UpdateConfig(context.Background(), 7, UpdateCatProxyProviderConfigInput{
		Name: "renamed", Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer",
		LifetimeMinutes: 120, Strict: catProxiesBool(false), DefaultCountry: catProxiesString(" CA "),
	})
	require.NoError(t, err)
	require.Equal(t, "renamed", dto.Name)
	require.Equal(t, 120, dto.LifetimeMinutes)
	require.Equal(t, "ca", *dto.DefaultCountry)
	require.Zero(t, prober.callCount)
	require.Zero(t, runtimeRepo.passwordUpdates)
	require.Empty(t, runtimeRepo.migrations)
}

func TestCatProxiesManagedProxyServiceBoundPasswordUpdateProbesThenUpdatesAllPasswords(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 7, Name: "provider", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusActive,
		Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer-type-residential",
		Password: "old-secret", LifetimeMinutes: 60,
	}}
	leaseRepo := &catProxiesLeaseRepoStub{bindingCount: 2}
	prober := &catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.4"}}
	runtimeRepo := &managedProxyRuntimeRepoStub{}
	svc := NewCatProxiesManagedProxyService(configRepo, leaseRepo, prober, nil, runtimeRepo)
	password := "new-secret"

	_, err := svc.UpdateConfig(context.Background(), 7, UpdateCatProxyProviderConfigInput{
		Name: "provider", Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer",
		Password: &password, LifetimeMinutes: 60,
	})
	require.NoError(t, err)
	require.Equal(t, 1, prober.callCount)
	require.Equal(t, 1, runtimeRepo.passwordUpdates)
	require.Equal(t, password, runtimeRepo.updatedConfig.Password)
}

func TestCatProxiesManagedProxyServiceBoundRoutingUpdateProbesAllBeforeAtomicMigration(t *testing.T) {
	newService := func(prober *catProxiesProbeStub) (*CatProxiesManagedProxyService, *catProxiesConfigRepoStub, *managedProxyRuntimeRepoStub) {
		configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
			ID: 7, Name: "provider", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusActive,
			Protocol: CatProxiesProtocolHTTP, Host: "old.example.com", BaseUsername: "customer-type-residential",
			Password: "secret", LifetimeMinutes: 60,
		}}
		leaseRepo := &catProxiesLeaseRepoStub{bindingCount: 2, leases: []ManagedProxyLease{
			{AccountID: 10, ProviderConfigID: 7, TargetCountry: catProxiesString("us"), Strict: true},
			{AccountID: 11, ProviderConfigID: 7, TargetCountry: catProxiesString("ca"), Strict: false},
		}}
		runtimeRepo := &managedProxyRuntimeRepoStub{}
		return NewCatProxiesManagedProxyService(configRepo, leaseRepo, prober, nil, runtimeRepo), configRepo, runtimeRepo
	}
	input := UpdateCatProxyProviderConfigInput{
		Name: "provider", Protocol: CatProxiesProtocolSOCKS5H, Host: "new.example.com", BaseUsername: "new-customer",
		LifetimeMinutes: 60,
	}

	failingProber := &catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.4", CountryCode: "US"}, errOnCall: 2}
	svc, configRepo, runtimeRepo := newService(failingProber)
	_, err := svc.UpdateConfig(context.Background(), 7, input)
	require.Error(t, err)
	require.Equal(t, 2, failingProber.callCount)
	require.Empty(t, runtimeRepo.migrations)
	require.Equal(t, "old.example.com", configRepo.config.Host)

	prober := &catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.4", CountryCode: "US"}}
	svc, _, runtimeRepo = newService(prober)
	_, err = svc.UpdateConfig(context.Background(), 7, input)
	require.NoError(t, err)
	require.Equal(t, 2, prober.callCount)
	require.Len(t, runtimeRepo.migrations, 1)
	require.Len(t, runtimeRepo.migrations[0], 2)
	require.NotEqual(t, runtimeRepo.migrations[0][0].Candidate.Derived.SessionID, runtimeRepo.migrations[0][1].Candidate.Derived.SessionID)
}

func TestCatProxiesManagedProxyService_CreateConfigReturnsRedactedDTO(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{}
	prober := &catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.4", CountryCode: "US"}}
	service := NewCatProxiesManagedProxyService(configRepo, &catProxiesLeaseRepoStub{}, prober, catProxiesTargetingForTest(t), nil)
	dto, err := service.CreateConfig(context.Background(), CreateCatProxyProviderConfigInput{
		Name:           "primary",
		Host:           "proxy.example.com",
		BaseUsername:   "customer-type-residential-type-residential",
		Password:       "secret",
		DefaultCountry: catProxiesString("us"),
	})
	require.NoError(t, err)
	require.True(t, dto.PasswordConfigured)
	require.Equal(t, CatProxiesProtocolHTTP, dto.Protocol)
	require.Equal(t, 60, dto.LifetimeMinutes)
	require.Equal(t, "customer-type-residential", dto.BaseUsername)
	require.True(t, dto.Strict)
	payload, err := json.Marshal(dto)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "secret")
}

func TestCatProxiesManagedProxyService_ProbeProxyStrictRegion(t *testing.T) {
	prober := &catProxiesProbeStub{info: &ProxyExitInfo{
		IP:          "203.0.113.4",
		CountryCode: "US",
		Region:      "California",
		City:        "Los Angeles",
	}, latency: 42}
	service := NewCatProxiesManagedProxyService(&catProxiesConfigRepoStub{}, &catProxiesLeaseRepoStub{}, prober, catProxiesTargetingForTest(t), nil)
	target := CatProxiesProxyTarget{
		Country: catProxiesString("us"),
		State:   catProxiesString("california"),
		City:    catProxiesString("losangeles"),
		Strict:  catProxiesBool(true),
	}
	result, err := service.ProbeProxy(context.Background(), &Proxy{
		Protocol: CatProxiesProtocolHTTP,
		Host:     "proxy.example.com",
		Port:     CatProxiesHTTPStickyPort,
		Username: "customer",
		Password: "secret",
	}, target)
	require.NoError(t, err)
	require.True(t, prober.called)
	require.True(t, result.StrictMatch)
	require.Equal(t, 42, result.LatencyMs)

	prober.info.City = "San Francisco"
	result, err = service.ProbeProxy(context.Background(), &Proxy{Protocol: "http", Host: "proxy.example.com", Port: 10000}, target)
	require.ErrorIs(t, err, ErrCatProxiesRegionMismatch)
	require.False(t, result.StrictMatch)
}

type catProxiesConfigRepoStub struct {
	config        *CatProxyProviderConfig
	configs       []CatProxyProviderConfig
	defaultExists bool
	deleted       bool
}

func (r *catProxiesConfigRepoStub) Create(_ context.Context, config *CatProxyProviderConfig) error {
	copy := *config
	if copy.ID == 0 {
		copy.ID = 1
	}
	r.config = &copy
	*config = copy
	return nil
}

func (r *catProxiesConfigRepoStub) GetByID(_ context.Context, _ int64) (*CatProxyProviderConfig, error) {
	if r.config == nil {
		return nil, ErrCatProxyProviderConfigNotFound
	}
	copy := *r.config
	return &copy, nil
}

func (r *catProxiesConfigRepoStub) List(context.Context) ([]CatProxyProviderConfig, error) {
	return r.configs, nil
}

func (r *catProxiesConfigRepoStub) Update(_ context.Context, config *CatProxyProviderConfig) error {
	copy := *config
	r.config = &copy
	*config = copy
	return nil
}

func (r *catProxiesConfigRepoStub) DefaultExists(context.Context, int64) (bool, error) {
	return r.defaultExists, nil
}

func (r *catProxiesConfigRepoStub) Delete(context.Context, int64) error {
	r.deleted = true
	return nil
}

type catProxiesLeaseRepoStub struct {
	bindingCount int64
	leases       []ManagedProxyLease
	proxyLease   *ManagedProxyLease
	accountLease *ManagedProxyLease
	proxyErr     error
}

func (*catProxiesLeaseRepoStub) Create(context.Context, *ManagedProxyLease) error { return nil }
func (r *catProxiesLeaseRepoStub) GetByAccountID(context.Context, int64) (*ManagedProxyLease, error) {
	if r.accountLease == nil {
		return nil, ErrManagedProxyLeaseNotFound
	}
	copy := *r.accountLease
	return &copy, nil
}
func (r *catProxiesLeaseRepoStub) GetByProxyID(context.Context, int64) (*ManagedProxyLease, error) {
	if r.proxyErr != nil {
		return nil, r.proxyErr
	}
	if r.proxyLease == nil {
		return nil, ErrManagedProxyLeaseNotFound
	}
	copy := *r.proxyLease
	return &copy, nil
}
func (r *catProxiesLeaseRepoStub) ListByProviderConfigID(context.Context, int64) ([]ManagedProxyLease, error) {
	return append([]ManagedProxyLease(nil), r.leases...), nil
}
func (*catProxiesLeaseRepoStub) ListDue(context.Context, time.Time, int) ([]ManagedProxyLease, error) {
	return nil, nil
}
func (r *catProxiesLeaseRepoStub) CountByProviderConfigID(context.Context, int64) (int64, error) {
	return r.bindingCount, nil
}
func (*catProxiesLeaseRepoStub) Update(context.Context, *ManagedProxyLease) error { return nil }
func (*catProxiesLeaseRepoStub) Delete(context.Context, int64) error              { return nil }

type catProxiesProbeStub struct {
	mu        sync.Mutex
	info      *ProxyExitInfo
	latency   int64
	err       error
	errOnCall int
	called    bool
	callCount int
}

func (p *catProxiesProbeStub) ProbeProxy(context.Context, string) (*ProxyExitInfo, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.called = true
	p.callCount++
	if p.errOnCall == p.callCount {
		return nil, p.latency, errors.New("probe failed")
	}
	return p.info, p.latency, p.err
}

var _ CatProxyProviderConfigRepository = (*catProxiesConfigRepoStub)(nil)
var _ ManagedProxyLeaseRepository = (*catProxiesLeaseRepoStub)(nil)
var _ ProxyExitInfoProber = (*catProxiesProbeStub)(nil)
