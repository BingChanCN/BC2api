package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const (
	catProxiesResidentialType = "-type-residential"
	catProxiesDefaultLifetime = 60
)

// CatProxyProviderConfigDTO is safe to return from public/admin APIs. It never carries the provider password.
type CatProxyProviderConfigDTO struct {
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	ProviderType       string     `json:"provider_type"`
	Status             string     `json:"status"`
	IsDefault          bool       `json:"is_default"`
	Protocol           string     `json:"protocol"`
	Host               string     `json:"host"`
	BaseUsername       string     `json:"base_username"`
	PasswordConfigured bool       `json:"password_configured"`
	DefaultCountry     *string    `json:"default_country,omitempty"`
	DefaultState       *string    `json:"default_state,omitempty"`
	DefaultCity        *string    `json:"default_city,omitempty"`
	LifetimeMinutes    int        `json:"lifetime_minutes"`
	Strict             bool       `json:"strict"`
	LastProbeAt        *time.Time `json:"last_probe_at,omitempty"`
	LastProbeLatencyMs *int       `json:"last_probe_latency_ms,omitempty"`
	LastError          *string    `json:"last_error,omitempty"`
	LastErrorAt        *time.Time `json:"last_error_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type CreateCatProxyProviderConfigInput struct {
	Name            string
	IsDefault       bool
	Protocol        string
	Host            string
	BaseUsername    string
	Password        string
	DefaultCountry  *string
	DefaultState    *string
	DefaultCity     *string
	LifetimeMinutes int
	Strict          *bool
}

type UpdateCatProxyProviderConfigInput struct {
	Name            string
	IsDefault       bool
	Protocol        string
	Host            string
	BaseUsername    string
	Password        *string
	DefaultCountry  *string
	DefaultState    *string
	DefaultCity     *string
	LifetimeMinutes int
	Strict          *bool
}

// CatProxiesProxyTarget overrides the provider defaults for one derived proxy.
// A nil Strict uses the provider default. Strict is disabled when no location is requested.
type CatProxiesProxyTarget struct {
	Country *string
	State   *string
	City    *string
	Strict  *bool
}

type CatProxiesLeaseTiming struct {
	RotateAt      time.Time
	HardExpiresAt time.Time
}

type CatProxiesDerivedProxy struct {
	Proxy     *Proxy
	SessionID string
	Target    CatProxiesProxyTarget
	Timing    CatProxiesLeaseTiming
}

type CatProxiesProbeResult struct {
	ExitIP      *string   `json:"exit_ip,omitempty"`
	Country     *string   `json:"country,omitempty"`
	State       *string   `json:"state,omitempty"`
	City        *string   `json:"city,omitempty"`
	LatencyMs   int       `json:"latency_ms"`
	CheckedAt   time.Time `json:"checked_at"`
	StrictMatch bool      `json:"strict_match"`
}

type CatProxiesManagedProxyService struct {
	configRepo CatProxyProviderConfigRepository
	leaseRepo  ManagedProxyLeaseRepository
	prober     ProxyExitInfoProber
	targeting  *CatProxiesTargetingService
	runtime    ManagedProxyRuntimeRepository
}

func NewCatProxiesManagedProxyService(
	configRepo CatProxyProviderConfigRepository,
	leaseRepo ManagedProxyLeaseRepository,
	prober ProxyExitInfoProber,
	targeting *CatProxiesTargetingService,
	runtime ManagedProxyRuntimeRepository,
) *CatProxiesManagedProxyService {
	return &CatProxiesManagedProxyService{configRepo: configRepo, leaseRepo: leaseRepo, prober: prober, targeting: targeting, runtime: runtime}
}

func (s *CatProxiesManagedProxyService) CreateConfig(ctx context.Context, input CreateCatProxyProviderConfigInput) (*CatProxyProviderConfigDTO, error) {
	defaultCountry, defaultState, defaultCity := normalizeCatProxiesRegion(input.DefaultCountry, input.DefaultState, input.DefaultCity)
	config := &CatProxyProviderConfig{
		Name:            strings.TrimSpace(input.Name),
		ProviderType:    CatProxiesProviderType,
		Status:          CatProxiesStatusActive,
		IsDefault:       input.IsDefault,
		Protocol:        input.Protocol,
		Host:            strings.TrimSpace(input.Host),
		BaseUsername:    NormalizeCatProxiesBaseUsername(input.BaseUsername),
		Password:        input.Password,
		DefaultCountry:  defaultCountry,
		DefaultState:    defaultState,
		DefaultCity:     defaultCity,
		LifetimeMinutes: input.LifetimeMinutes,
		Strict:          catProxiesStrictDefault(input.Strict, defaultCountry, defaultState, defaultCity),
	}
	applyCatProxiesConfigDefaults(config)
	if err := s.validateConfig(config); err != nil {
		return nil, catProxiesBadRequest(err)
	}
	if err := s.ensureDefaultAvailable(ctx, config.IsDefault, 0); err != nil {
		return nil, err
	}
	derived, err := DeriveCatProxiesProxy(config, CatProxiesProxyTarget{}, time.Now())
	if err != nil {
		return nil, catProxiesBadRequest(err)
	}
	probe, err := s.ProbeProxy(ctx, derived.Proxy, derived.Target)
	if err != nil {
		return nil, fmt.Errorf("probe catproxies config before create: %w", err)
	}
	config.LastProbeAt = &probe.CheckedAt
	config.LastProbeLatencyMs = &probe.LatencyMs
	if err := s.configRepo.Create(ctx, config); err != nil {
		return nil, err
	}
	return catProxyProviderConfigDTO(config), nil
}

func (s *CatProxiesManagedProxyService) GetConfig(ctx context.Context, id int64) (*CatProxyProviderConfigDTO, error) {
	config, err := s.configRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return catProxyProviderConfigDTO(config), nil
}

func (s *CatProxiesManagedProxyService) getConfigForBackup(ctx context.Context, id int64) (*CatProxyProviderConfig, error) {
	return s.configRepo.GetByID(ctx, id)
}

func (s *CatProxiesManagedProxyService) ListConfigs(ctx context.Context) ([]CatProxyProviderConfigDTO, error) {
	configs, err := s.configRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]CatProxyProviderConfigDTO, 0, len(configs))
	for i := range configs {
		result = append(result, *catProxyProviderConfigDTO(&configs[i]))
	}
	return result, nil
}

func (s *CatProxiesManagedProxyService) UpdateConfig(ctx context.Context, id int64, input UpdateCatProxyProviderConfigInput) (*CatProxyProviderConfigDTO, error) {
	current, err := s.configRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	config := *current
	defaultCountry, defaultState, defaultCity := normalizeCatProxiesRegion(input.DefaultCountry, input.DefaultState, input.DefaultCity)
	config.Name = strings.TrimSpace(input.Name)
	config.IsDefault = input.IsDefault
	config.Protocol = input.Protocol
	config.Host = strings.TrimSpace(input.Host)
	config.BaseUsername = NormalizeCatProxiesBaseUsername(input.BaseUsername)
	config.DefaultCountry = defaultCountry
	config.DefaultState = defaultState
	config.DefaultCity = defaultCity
	config.LifetimeMinutes = input.LifetimeMinutes
	if input.Strict != nil {
		config.Strict = *input.Strict
	}
	if input.Password != nil && *input.Password != "" {
		config.Password = *input.Password
	}
	applyCatProxiesConfigDefaults(&config)
	if config.Status != CatProxiesStatusActive {
		config.IsDefault = false
	}
	if err := s.validateConfig(&config); err != nil {
		return nil, catProxiesBadRequest(err)
	}
	if err := s.ensureDefaultAvailable(ctx, config.IsDefault, id); err != nil {
		return nil, err
	}

	routingChanged := current.Protocol != config.Protocol || current.Host != config.Host || current.BaseUsername != config.BaseUsername
	passwordChanged := current.Password != config.Password
	switch {
	case routingChanged:
		if err := s.updateBoundProviderRouting(ctx, &config); err != nil {
			return nil, err
		}
	case passwordChanged:
		if err := s.updateBoundProviderPassword(ctx, &config); err != nil {
			return nil, err
		}
	default:
		if err := s.configRepo.Update(ctx, &config); err != nil {
			return nil, err
		}
	}
	return catProxyProviderConfigDTO(&config), nil
}

func (s *CatProxiesManagedProxyService) updateBoundProviderPassword(ctx context.Context, config *CatProxyProviderConfig) error {
	derived, err := DeriveCatProxiesProxy(config, CatProxiesProxyTarget{}, time.Now())
	if err != nil {
		return catProxiesBadRequest(err)
	}
	if _, err := s.ProbeProxy(ctx, derived.Proxy, derived.Target); err != nil {
		return fmt.Errorf("probe catproxies password before update: %w", err)
	}
	bound, err := s.leaseRepo.CountByProviderConfigID(ctx, config.ID)
	if err != nil {
		return err
	}
	if bound == 0 {
		return s.configRepo.Update(ctx, config)
	}
	if s.runtime == nil {
		return fmt.Errorf("managed proxy runtime repository is not configured")
	}
	return s.runtime.UpdateProviderPassword(ctx, config)
}

func (s *CatProxiesManagedProxyService) updateBoundProviderRouting(ctx context.Context, config *CatProxyProviderConfig) error {
	leases, err := s.leaseRepo.ListByProviderConfigID(ctx, config.ID)
	if err != nil {
		return err
	}
	candidates := make([]ManagedProxyProviderCandidate, 0, len(leases))
	for i := range leases {
		lease := &leases[i]
		strict := lease.Strict
		target := CatProxiesProxyTarget{Country: lease.TargetCountry, State: lease.TargetState, City: lease.TargetCity, Strict: &strict}
		derived, err := DeriveCatProxiesProxy(config, target, time.Now())
		if err != nil {
			return catProxiesBadRequest(err)
		}
		probe, err := s.ProbeProxy(ctx, derived.Proxy, derived.Target)
		if err != nil {
			return fmt.Errorf("probe managed proxy candidate for account %d: %w", lease.AccountID, err)
		}
		candidates = append(candidates, ManagedProxyProviderCandidate{
			AccountID: lease.AccountID,
			Candidate: ManagedProxyCandidate{ProviderConfigID: config.ID, ProviderUpdatedAt: config.UpdatedAt, LifetimeMinutes: config.LifetimeMinutes, Derived: *derived, Probe: *probe},
		})
	}
	if len(candidates) == 0 {
		derived, err := DeriveCatProxiesProxy(config, CatProxiesProxyTarget{}, time.Now())
		if err != nil {
			return catProxiesBadRequest(err)
		}
		if _, err := s.ProbeProxy(ctx, derived.Proxy, derived.Target); err != nil {
			return fmt.Errorf("probe catproxies routing before update: %w", err)
		}
		return s.configRepo.Update(ctx, config)
	}
	if s.runtime == nil {
		return fmt.Errorf("managed proxy runtime repository is not configured")
	}
	return s.runtime.MigrateProvider(ctx, config, candidates)
}

// UpdateConfigStatus updates provider allocation and existing-lease readiness gates.
func (s *CatProxiesManagedProxyService) UpdateConfigStatus(ctx context.Context, id int64, status string) (*CatProxyProviderConfigDTO, error) {
	if !isCatProxiesStatus(status) {
		return nil, catProxiesBadRequest(fmt.Errorf("invalid catproxies provider status %q", status))
	}
	config, err := s.configRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	previousStatus := config.Status
	config.Status = status
	if status != CatProxiesStatusActive {
		config.IsDefault = false
	}
	if status == CatProxiesStatusActive && (previousStatus == CatProxiesStatusDisabled || previousStatus == CatProxiesStatusCredentialError) {
		if err := s.reactivateProvider(ctx, config); err != nil {
			return nil, err
		}
		return catProxyProviderConfigDTO(config), nil
	}
	if s.runtime != nil {
		updater, ok := s.runtime.(providerStatusUpdater)
		if !ok {
			return nil, fmt.Errorf("managed proxy runtime repository does not support atomic provider status updates")
		}
		updateReady := status == CatProxiesStatusDisabled || status == CatProxiesStatusCredentialError
		if err := updater.UpdateProviderStatus(ctx, config, false, updateReady); err != nil {
			return nil, fmt.Errorf("update managed proxy provider status: %w", err)
		}
	} else if err := s.configRepo.Update(ctx, config); err != nil {
		return nil, err
	}
	return catProxyProviderConfigDTO(config), nil
}

func (s *CatProxiesManagedProxyService) reactivateProvider(ctx context.Context, config *CatProxyProviderConfig) error {
	if s.runtime == nil {
		return fmt.Errorf("managed proxy runtime repository is not configured")
	}
	leases, err := s.leaseRepo.ListByProviderConfigID(ctx, config.ID)
	if err != nil {
		return err
	}
	if len(leases) == 0 {
		derived, deriveErr := DeriveCatProxiesProxy(config, CatProxiesProxyTarget{}, time.Now())
		if deriveErr != nil {
			return catProxiesBadRequest(deriveErr)
		}
		if _, probeErr := s.ProbeProxy(ctx, derived.Proxy, derived.Target); probeErr != nil {
			return fmt.Errorf("probe catproxies config before reactivation: %w", probeErr)
		}
		updater, ok := s.runtime.(providerStatusUpdater)
		if !ok {
			return fmt.Errorf("managed proxy runtime repository does not support atomic provider status updates")
		}
		return updater.UpdateProviderStatus(ctx, config, true, true)
	}

	type probeResult struct {
		candidate ManagedProxyProviderCandidate
		err       error
	}
	results := make([]probeResult, len(leases))
	semaphore := make(chan struct{}, catProxiesRotationWorkers)
	var group sync.WaitGroup
	for i := range leases {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			lease := leases[index]
			strict := lease.Strict
			target := CatProxiesProxyTarget{Country: lease.TargetCountry, State: lease.TargetState, City: lease.TargetCity, Strict: &strict}
			derived, deriveErr := DeriveCatProxiesProxy(config, target, time.Now())
			if deriveErr != nil {
				results[index].err = deriveErr
				return
			}
			probe, probeErr := s.ProbeProxy(ctx, derived.Proxy, derived.Target)
			if probeErr != nil {
				results[index].err = probeErr
				return
			}
			results[index].candidate = ManagedProxyProviderCandidate{AccountID: lease.AccountID, Candidate: ManagedProxyCandidate{ProviderConfigID: config.ID, ProviderUpdatedAt: config.UpdatedAt, LifetimeMinutes: config.LifetimeMinutes, Derived: *derived, Probe: *probe}}
		}(i)
	}
	group.Wait()

	candidates := make([]ManagedProxyProviderCandidate, 0, len(results))
	failures := make(map[int64]string)
	for i := range results {
		if results[i].err != nil {
			failures[leases[i].AccountID] = results[i].err.Error()
			continue
		}
		candidates = append(candidates, results[i].candidate)
	}
	if len(candidates) == 0 {
		return fmt.Errorf("reactivate catproxies provider: all %d managed proxy probes failed", len(leases))
	}
	now := time.Now()
	if len(failures) > 0 {
		message := fmt.Sprintf("%d managed proxy account(s) failed reactivation", len(failures))
		config.LastError = &message
		config.LastErrorAt = &now
	} else {
		config.LastError = nil
		config.LastErrorAt = nil
	}
	reactivator, ok := s.runtime.(providerReactivator)
	if !ok {
		return fmt.Errorf("managed proxy runtime repository does not support provider reactivation")
	}
	return reactivator.ReactivateProvider(ctx, config, candidates, failures, now)
}

func (s *CatProxiesManagedProxyService) TestConfig(ctx context.Context, id int64) (*CatProxiesProbeResult, error) {
	config, err := s.configRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	derived, err := DeriveCatProxiesProxy(config, CatProxiesProxyTarget{}, time.Now())
	if err != nil {
		return nil, err
	}
	probe, probeErr := s.ProbeProxy(ctx, derived.Proxy, derived.Target)
	now := time.Now()
	config.LastProbeAt = &now
	if probeErr != nil {
		message := probeErr.Error()
		config.LastProbeLatencyMs = nil
		config.LastError = &message
		config.LastErrorAt = &now
		if updateErr := s.configRepo.Update(ctx, config); updateErr != nil {
			return nil, fmt.Errorf("probe catproxies config: %v; persist probe failure: %w", probeErr, updateErr)
		}
		return nil, probeErr
	}
	config.LastProbeAt = &probe.CheckedAt
	config.LastProbeLatencyMs = &probe.LatencyMs
	config.LastError = nil
	config.LastErrorAt = nil
	if err := s.configRepo.Update(ctx, config); err != nil {
		return nil, fmt.Errorf("persist catproxies probe result: %w", err)
	}
	return probe, nil
}

func (s *CatProxiesManagedProxyService) validateConfig(config *CatProxyProviderConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if s.targeting != nil {
		return s.targeting.ValidateTarget(config.DefaultCountry, config.DefaultState, config.DefaultCity)
	}
	return nil
}

func (s *CatProxiesManagedProxyService) DeleteConfig(ctx context.Context, id int64) error {
	if _, err := s.configRepo.GetByID(ctx, id); err != nil {
		return err
	}
	if err := s.requireUnbound(ctx, id); err != nil {
		return err
	}
	return s.configRepo.Delete(ctx, id)
}

func (s *CatProxiesManagedProxyService) ensureDefaultAvailable(ctx context.Context, requested bool, excludeID int64) error {
	if !requested {
		return nil
	}
	exists, err := s.configRepo.DefaultExists(ctx, excludeID)
	if err != nil {
		return err
	}
	if exists {
		return ErrCatProxyProviderDefaultExists
	}
	return nil
}

func (s *CatProxiesManagedProxyService) requireUnbound(ctx context.Context, configID int64) error {
	count, err := s.leaseRepo.CountByProviderConfigID(ctx, configID)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrCatProxyProviderConfigInUse
	}
	return nil
}

func applyCatProxiesConfigDefaults(config *CatProxyProviderConfig) {
	if config.Protocol == "" {
		config.Protocol = CatProxiesProtocolHTTP
	}
	if config.LifetimeMinutes == 0 {
		config.LifetimeMinutes = catProxiesDefaultLifetime
	}
}

func catProxyProviderConfigDTO(config *CatProxyProviderConfig) *CatProxyProviderConfigDTO {
	if config == nil {
		return nil
	}
	return &CatProxyProviderConfigDTO{
		ID:                 config.ID,
		Name:               config.Name,
		ProviderType:       config.ProviderType,
		Status:             config.Status,
		IsDefault:          config.IsDefault,
		Protocol:           config.Protocol,
		Host:               config.Host,
		BaseUsername:       config.BaseUsername,
		PasswordConfigured: config.Password != "",
		DefaultCountry:     config.DefaultCountry,
		DefaultState:       config.DefaultState,
		DefaultCity:        config.DefaultCity,
		LifetimeMinutes:    config.LifetimeMinutes,
		Strict:             config.Strict,
		LastProbeAt:        config.LastProbeAt,
		LastProbeLatencyMs: config.LastProbeLatencyMs,
		LastError:          config.LastError,
		LastErrorAt:        config.LastErrorAt,
		CreatedAt:          config.CreatedAt,
		UpdatedAt:          config.UpdatedAt,
	}
}

func catProxiesBadRequest(err error) error {
	return infraerrors.BadRequest("CATPROXIES_INVALID", err.Error()).WithCause(err)
}

// NormalizeCatProxiesBaseUsername strips duplicate residential selectors and appends one canonical selector.
func NormalizeCatProxiesBaseUsername(base string) string {
	base = strings.TrimSpace(base)
	if selector := strings.Index(base, catProxiesResidentialType); selector >= 0 {
		base = base[:selector]
	}
	base = strings.Trim(base, "-")
	if base == "" {
		return ""
	}
	return base + catProxiesResidentialType
}

func GenerateCatProxiesSessionID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate catproxies session: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}

func ComputeCatProxiesLeaseTiming(now time.Time, lifetimeMinutes int) (CatProxiesLeaseTiming, error) {
	if err := ValidateCatProxiesLifetime(lifetimeMinutes); err != nil {
		return CatProxiesLeaseTiming{}, err
	}
	leadMinutes := lifetimeMinutes / 10
	if leadMinutes < 1 {
		leadMinutes = 1
	}
	if leadMinutes > 5 {
		leadMinutes = 5
	}
	return CatProxiesLeaseTiming{
		RotateAt:      now.Add(time.Duration(lifetimeMinutes-leadMinutes) * time.Minute),
		HardExpiresAt: now.Add(time.Duration(lifetimeMinutes) * time.Minute),
	}, nil
}

func BuildCatProxiesUsername(baseUsername string, target CatProxiesProxyTarget, sessionID string, lifetimeMinutes int) (string, error) {
	target = normalizeCatProxiesTarget(target)
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("catproxies session is required")
	}
	if err := ValidateCatProxiesRegion(target.Country, target.State, target.City); err != nil {
		return "", err
	}
	base := NormalizeCatProxiesBaseUsername(baseUsername)
	if base == "" {
		return "", fmt.Errorf("catproxies base username is required")
	}
	if err := ValidateCatProxiesLifetime(lifetimeMinutes); err != nil {
		return "", err
	}

	var username strings.Builder
	username.WriteString(base)
	for _, part := range []struct {
		name  string
		value *string
	}{
		{name: "country", value: target.Country},
		{name: "state", value: target.State},
		{name: "city", value: target.City},
	} {
		if part.value != nil {
			fmt.Fprintf(&username, "-%s-%s", part.name, *part.value)
		}
	}
	fmt.Fprintf(&username, "-lifetime-%d-session-%s", lifetimeMinutes, sessionID)
	if catProxiesTargetHasRegion(target.Country, target.State, target.City) {
		strict := true
		if target.Strict != nil {
			strict = *target.Strict
		}
		if strict {
			username.WriteString("-strict-on")
		} else {
			username.WriteString("-strict-off")
		}
	}
	result := username.String()
	if len(result) > 255 {
		return "", fmt.Errorf("catproxies derived username exceeds 255 characters")
	}
	return result, nil
}

func DeriveCatProxiesProxy(config *CatProxyProviderConfig, requested CatProxiesProxyTarget, now time.Time) (*CatProxiesDerivedProxy, error) {
	if config == nil {
		return nil, ErrCatProxyProviderConfigNotFound
	}
	resolved := normalizeCatProxiesTarget(resolveCatProxiesTarget(config, normalizeCatProxiesTarget(requested)))
	if err := ValidateCatProxiesRegion(resolved.Country, resolved.State, resolved.City); err != nil {
		return nil, err
	}
	sessionID, err := GenerateCatProxiesSessionID()
	if err != nil {
		return nil, err
	}
	username, err := BuildCatProxiesUsername(config.BaseUsername, resolved, sessionID, config.LifetimeMinutes)
	if err != nil {
		return nil, err
	}
	timing, err := ComputeCatProxiesLeaseTiming(now, config.LifetimeMinutes)
	if err != nil {
		return nil, err
	}
	port, err := catProxiesStickyPort(config.Protocol)
	if err != nil {
		return nil, err
	}
	hardExpiry := timing.HardExpiresAt
	return &CatProxiesDerivedProxy{
		Proxy: &Proxy{
			Name:           fmt.Sprintf("catproxies-%d-managed", config.ID),
			Protocol:       config.Protocol,
			Host:           config.Host,
			Port:           port,
			Username:       username,
			Password:       config.Password,
			Status:         StatusActive,
			ExpiresAt:      &hardExpiry,
			FallbackMode:   FallbackModeNone,
			ExpiryWarnDays: 0,
		},
		SessionID: sessionID,
		Target:    resolved,
		Timing:    timing,
	}, nil
}

func (s *CatProxiesManagedProxyService) DeriveProxy(ctx context.Context, configID int64, target CatProxiesProxyTarget, now time.Time) (*CatProxiesDerivedProxy, error) {
	config, err := s.configRepo.GetByID(ctx, configID)
	if err != nil {
		return nil, err
	}
	if config.Status != CatProxiesStatusActive {
		return nil, infraerrors.Conflict("CATPROXIES_PROVIDER_INACTIVE", "catproxies provider config is not active")
	}
	return DeriveCatProxiesProxy(config, target, now)
}

func normalizeCatProxiesTarget(target CatProxiesProxyTarget) CatProxiesProxyTarget {
	target.Country, target.State, target.City = normalizeCatProxiesRegion(target.Country, target.State, target.City)
	return target
}

func normalizeCatProxiesRegion(country, state, city *string) (*string, *string, *string) {
	return normalizeCatProxiesRegionToken(country), normalizeCatProxiesRegionToken(state), normalizeCatProxiesRegionToken(city)
}

func normalizeCatProxiesRegionToken(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(*value))
	if normalized == "" {
		return nil
	}
	return &normalized
}

func catProxiesStrictDefault(requested *bool, country, state, city *string) bool {
	if requested != nil {
		return *requested
	}
	return catProxiesTargetHasRegion(country, state, city)
}

func catProxiesTargetHasRegion(country, state, city *string) bool {
	return country != nil || state != nil || city != nil
}

func resolveCatProxiesTarget(config *CatProxyProviderConfig, requested CatProxiesProxyTarget) CatProxiesProxyTarget {
	resolved := requested
	if resolved.Country == nil {
		resolved.Country = config.DefaultCountry
	}
	if resolved.State == nil {
		resolved.State = config.DefaultState
	}
	if resolved.City == nil {
		resolved.City = config.DefaultCity
	}
	strict := config.Strict
	if requested.Strict != nil {
		strict = *requested.Strict
	}
	if resolved.Country == nil && resolved.State == nil && resolved.City == nil {
		strict = false
	}
	resolved.Strict = &strict
	return resolved
}

func catProxiesStickyPort(protocol string) (int, error) {
	switch protocol {
	case CatProxiesProtocolHTTP:
		return CatProxiesHTTPStickyPort, nil
	case CatProxiesProtocolSOCKS5H:
		return CatProxiesSOCKS5HStickyPort, nil
	default:
		return 0, fmt.Errorf("invalid catproxies protocol %q", protocol)
	}
}

// ProbeProxy synchronously probes the derived proxy and enforces configured strict location checks.
func (s *CatProxiesManagedProxyService) ProbeProxy(ctx context.Context, proxy *Proxy, target CatProxiesProxyTarget) (*CatProxiesProbeResult, error) {
	if proxy == nil {
		return nil, ErrProxyNotFound
	}
	if s.prober == nil {
		return nil, fmt.Errorf("catproxies proxy prober is not configured")
	}
	exitInfo, latency, err := s.prober.ProbeProxy(ctx, proxy.URL())
	if err != nil {
		return nil, redactCatProxiesProxyError(err, proxy)
	}
	result := &CatProxiesProbeResult{
		LatencyMs:   int(latency),
		CheckedAt:   time.Now(),
		StrictMatch: true,
	}
	if exitInfo != nil {
		result.ExitIP = nonEmptyStringPointer(exitInfo.IP)
		observedCountry := exitInfo.CountryCode
		if observedCountry == "" {
			observedCountry = exitInfo.Country
		}
		result.Country = nonEmptyStringPointer(observedCountry)
		result.State = nonEmptyStringPointer(exitInfo.Region)
		result.City = nonEmptyStringPointer(exitInfo.City)
	}
	if target.Strict != nil && *target.Strict {
		if err := verifyCatProxiesStrictTarget(target, result); err != nil {
			result.StrictMatch = false
			return result, err
		}
	}
	return result, nil
}

type catProxiesRedactedError struct {
	message string
	cause   error
}

func (e *catProxiesRedactedError) Error() string { return e.message }
func (e *catProxiesRedactedError) Unwrap() error { return e.cause }

func redactCatProxiesProxyError(err error, proxy *Proxy) error {
	if err == nil {
		return nil
	}
	message := logredact.RedactText(err.Error(), "password", "username", "session")
	if proxy != nil {
		if proxyURL := proxy.URL(); proxyURL != "" {
			message = strings.ReplaceAll(message, proxyURL, "<managed-proxy-url-redacted>")
		}
		for _, secret := range []string{proxy.Username, proxy.Password} {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "***")
			}
		}
	}
	return &catProxiesRedactedError{message: message, cause: err}
}

func verifyCatProxiesStrictTarget(target CatProxiesProxyTarget, observed *CatProxiesProbeResult) error {
	checks := []struct {
		name     string
		target   *string
		observed *string
	}{
		{name: "country", target: target.Country, observed: observed.Country},
		{name: "state", target: target.State, observed: observed.State},
		{name: "city", target: target.City, observed: observed.City},
	}
	for _, check := range checks {
		if check.target == nil {
			continue
		}
		if check.observed == nil {
			return fmt.Errorf("%w: %s expected %q, probe returned no value", ErrCatProxiesRegionMismatch, check.name, *check.target)
		}
		if normalizeObservedCatProxiesRegion(*check.observed) != *check.target {
			return fmt.Errorf("%w: %s expected %q, observed %q", ErrCatProxiesRegionMismatch, check.name, *check.target, *check.observed)
		}
	}
	return nil
}

func normalizeObservedCatProxiesRegion(value string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.ToLower(strings.TrimSpace(value)))
}

func nonEmptyStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
