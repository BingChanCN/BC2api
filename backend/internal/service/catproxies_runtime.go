package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	managedProxyFailureWindow    = 2 * time.Minute
	managedProxyFailureThreshold = 2
	managedProxyRotationCooldown = 5 * time.Minute
	managedProxyRateLimitBackoff = 5 * time.Second
)

type ManagedProxyAccountDTO struct {
	Lease             ManagedProxyLease `json:"lease"`
	AccountName       string            `json:"account_name"`
	Platform          string            `json:"platform"`
	Schedulable       bool              `json:"schedulable"`
	ManagedProxyReady bool              `json:"managed_proxy_ready"`
	ProviderName      string            `json:"provider_name"`
}

// MarshalJSON deliberately omits the provider session identifier. It is needed
// to build and rotate a lease, but is never part of the runtime API contract.
func (dto ManagedProxyAccountDTO) MarshalJSON() ([]byte, error) {
	lease := struct {
		ID                      int64      `json:"id"`
		AccountID               int64      `json:"account_id"`
		ProxyID                 int64      `json:"proxy_id"`
		ProviderConfigID        int64      `json:"provider_config_id"`
		TargetCountry           *string    `json:"target_country,omitempty"`
		TargetState             *string    `json:"target_state,omitempty"`
		TargetCity              *string    `json:"target_city,omitempty"`
		Strict                  bool       `json:"strict"`
		LifetimeMinutes         int        `json:"lifetime_minutes"`
		State                   string     `json:"state"`
		HealthStatus            string     `json:"health_status"`
		HealthCheckedAt         *time.Time `json:"health_checked_at,omitempty"`
		ObservedExitIP          *string    `json:"observed_exit_ip,omitempty"`
		ObservedCountry         *string    `json:"observed_country,omitempty"`
		ObservedState           *string    `json:"observed_state,omitempty"`
		ObservedCity            *string    `json:"observed_city,omitempty"`
		ObservedLatencyMs       *int       `json:"observed_latency_ms,omitempty"`
		ActivatedAt             *time.Time `json:"activated_at,omitempty"`
		LastRotatedAt           *time.Time `json:"last_rotated_at,omitempty"`
		NextRotationAt          *time.Time `json:"next_rotation_at,omitempty"`
		ExpiresAt               *time.Time `json:"expires_at,omitempty"`
		FailureCount            int        `json:"failure_count"`
		ConsecutiveFailureCount int        `json:"consecutive_failure_count"`
		LastError               *string    `json:"last_error,omitempty"`
		LastErrorAt             *time.Time `json:"last_error_at,omitempty"`
		CreatedAt               time.Time  `json:"created_at"`
		UpdatedAt               time.Time  `json:"updated_at"`
	}{
		ID: dto.Lease.ID, AccountID: dto.Lease.AccountID, ProxyID: dto.Lease.ProxyID,
		ProviderConfigID: dto.Lease.ProviderConfigID, TargetCountry: dto.Lease.TargetCountry,
		TargetState: dto.Lease.TargetState, TargetCity: dto.Lease.TargetCity, Strict: dto.Lease.Strict,
		LifetimeMinutes: dto.Lease.LifetimeMinutes, State: dto.Lease.State,
		HealthStatus: dto.Lease.HealthStatus, HealthCheckedAt: dto.Lease.HealthCheckedAt,
		ObservedExitIP: dto.Lease.ObservedExitIP, ObservedCountry: dto.Lease.ObservedCountry,
		ObservedState: dto.Lease.ObservedState, ObservedCity: dto.Lease.ObservedCity,
		ObservedLatencyMs: dto.Lease.ObservedLatencyMs, ActivatedAt: dto.Lease.ActivatedAt,
		LastRotatedAt: dto.Lease.LastRotatedAt, NextRotationAt: dto.Lease.NextRotationAt,
		ExpiresAt: dto.Lease.ExpiresAt, FailureCount: dto.Lease.FailureCount,
		ConsecutiveFailureCount: dto.Lease.ConsecutiveFailureCount, LastError: dto.Lease.LastError,
		LastErrorAt: dto.Lease.LastErrorAt, CreatedAt: dto.Lease.CreatedAt, UpdatedAt: dto.Lease.UpdatedAt,
	}
	return json.Marshal(struct {
		Lease             any    `json:"lease"`
		AccountName       string `json:"account_name"`
		Platform          string `json:"platform"`
		Schedulable       bool   `json:"schedulable"`
		ManagedProxyReady bool   `json:"managed_proxy_ready"`
		ProviderName      string `json:"provider_name"`
	}{lease, dto.AccountName, dto.Platform, dto.Schedulable, dto.ManagedProxyReady, dto.ProviderName})
}

type ManagedProxyCandidate struct {
	ProviderConfigID  int64
	ProviderUpdatedAt time.Time
	LifetimeMinutes   int
	InitialReady      *bool
	Derived           CatProxiesDerivedProxy
	Probe             CatProxiesProbeResult
}

type ManagedProxyProviderCandidate struct {
	AccountID int64
	Candidate ManagedProxyCandidate
}

type ManagedProxyRuntimeRepository interface {
	List(ctx context.Context) ([]ManagedProxyAccountDTO, error)
	GetByAccountID(ctx context.Context, accountID int64) (*ManagedProxyAccountDTO, error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]ManagedProxyAccountDTO, error)
	Activate(ctx context.Context, accountID int64, candidate ManagedProxyCandidate) (*ManagedProxyAccountDTO, error)
	Swap(ctx context.Context, accountID int64, candidate ManagedProxyCandidate) (*ManagedProxyAccountDTO, error)
	RecordRotationFailure(ctx context.Context, accountID int64, now time.Time, hardExpired bool, message string) error
	Release(ctx context.Context, accountID int64) error
	UpdateProviderPassword(ctx context.Context, config *CatProxyProviderConfig) error
	MigrateProvider(ctx context.Context, config *CatProxyProviderConfig, candidates []ManagedProxyProviderCandidate) error
	SetProviderReady(ctx context.Context, providerConfigID int64, ready bool) error
	MarkProviderCredentialErrorByAccount(ctx context.Context, accountID, observedProxyID int64, observedProxyUpdatedAt time.Time, message string) (marked bool, err error)
	RecordTransportFailure(ctx context.Context, accountID, observedProxyID int64, now time.Time, message string) (recorded bool, trigger bool, err error)
	ClearTransportFailures(ctx context.Context, accountID, observedProxyID int64) error
}

type scheduledRotationFailureRecorder interface {
	RecordScheduledRotationFailure(ctx context.Context, accountID int64, now time.Time, hardExpired bool, message string) error
}

type providerStatusUpdater interface {
	UpdateProviderStatus(ctx context.Context, config *CatProxyProviderConfig, ready bool, updateReady bool) error
}

type providerReactivator interface {
	ReactivateProvider(ctx context.Context, config *CatProxyProviderConfig, candidates []ManagedProxyProviderCandidate, failures map[int64]string, observedAt time.Time) error
}

type accountNotReadyMarker interface {
	MarkAccountNotReady(ctx context.Context, accountID, observedProxyID int64, message string) (marked bool, err error)
}

type rateLimitBackoffRecorder interface {
	RecordRateLimitBackoff(ctx context.Context, accountID int64, observedAt, until time.Time) error
}

type immediateTransportFailureRecorder interface {
	RecordImmediateTransportFailure(ctx context.Context, accountID, observedProxyID int64, now time.Time, message string) (recorded bool, trigger bool, err error)
}

type bindingsChangedRetryRecorder interface {
	RecordBindingsChangedRetry(ctx context.Context, accountID, observedProxyID int64, nextRetry time.Time) error
}

type ManagedProxyRuntimeService struct {
	catproxies        *CatProxiesManagedProxyService
	runtime           ManagedProxyRuntimeRepository
	now               func() time.Time
	locks             sync.Map
	transportFailures sync.Map
	faultRetrySlots   chan struct{}
}

func NewManagedProxyRuntimeService(catproxies *CatProxiesManagedProxyService, runtime ManagedProxyRuntimeRepository) *ManagedProxyRuntimeService {
	return &ManagedProxyRuntimeService{
		catproxies:      catproxies,
		runtime:         runtime,
		now:             time.Now,
		faultRetrySlots: make(chan struct{}, 32),
	}
}

func (s *ManagedProxyRuntimeService) List(ctx context.Context) ([]ManagedProxyAccountDTO, error) {
	return s.runtime.List(ctx)
}

func (s *ManagedProxyRuntimeService) Get(ctx context.Context, accountID int64) (*ManagedProxyAccountDTO, error) {
	return s.runtime.GetByAccountID(ctx, accountID)
}

// PrepareCandidate derives and synchronously probes a new session without
// writing database state. Import uses it before opening the provisioning tx.
func (s *ManagedProxyRuntimeService) PrepareCandidate(ctx context.Context, configID int64, target CatProxiesProxyTarget) (ManagedProxyCandidate, error) {
	return s.prepareCandidate(ctx, configID, target, false)
}

func (s *ManagedProxyRuntimeService) Manage(ctx context.Context, accountID, configID int64, target CatProxiesProxyTarget) (*ManagedProxyAccountDTO, error) {
	candidate, err := s.prepareCandidate(ctx, configID, target, false)
	if err != nil {
		return nil, err
	}
	managed, err := s.runtime.Activate(ctx, accountID, candidate)
	if err != nil {
		return nil, err
	}
	return managed, nil
}

func (s *ManagedProxyRuntimeService) Rotate(ctx context.Context, accountID int64) (*ManagedProxyAccountDTO, error) {
	return s.rotate(ctx, accountID, false)
}

// RotateDue is used only by the periodic worker. A failed attempt before the
// hard expiry keeps the old lease usable and moves its next retry one minute.
func (s *ManagedProxyRuntimeService) RotateDue(ctx context.Context, accountID int64) (*ManagedProxyAccountDTO, error) {
	return s.rotate(ctx, accountID, true)
}

func (s *ManagedProxyRuntimeService) rotate(ctx context.Context, accountID int64, scheduled bool) (*ManagedProxyAccountDTO, error) {
	value, _ := s.locks.LoadOrStore(accountID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	managed, err := s.runtime.GetByAccountID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	target := CatProxiesProxyTarget{Country: managed.Lease.TargetCountry, State: managed.Lease.TargetState, City: managed.Lease.TargetCity}
	strict := managed.Lease.Strict
	target.Strict = &strict
	candidate, err := s.prepareCandidate(ctx, managed.Lease.ProviderConfigID, target, true)
	if err != nil {
		return nil, s.recordRotationFailure(ctx, managed, accountID, scheduled, err)
	}
	updated, err := s.runtime.Swap(ctx, accountID, candidate)
	if err != nil {
		if errors.Is(err, ErrManagedProxyBindingsChanged) {
			if recorder, ok := s.runtime.(bindingsChangedRetryRecorder); ok {
				persistCtx, cancel := managedProxyPersistenceContext(ctx)
				defer cancel()
				if retryErr := recorder.RecordBindingsChangedRetry(persistCtx, accountID, managed.Lease.ProxyID, s.now().Add(time.Minute)); retryErr != nil {
					return nil, fmt.Errorf("rotation bindings changed: %v; schedule retry: %w", err, retryErr)
				}
			}
			return nil, err
		}
		return nil, s.recordRotationFailure(ctx, managed, accountID, scheduled, err)
	}
	return updated, nil
}

func (s *ManagedProxyRuntimeService) recordRotationFailure(ctx context.Context, managed *ManagedProxyAccountDTO, accountID int64, scheduled bool, cause error) error {
	persistCtx, cancel := managedProxyPersistenceContext(ctx)
	defer cancel()
	now := s.now()
	hardExpired := managed.Lease.ExpiresAt != nil && !now.Before(*managed.Lease.ExpiresAt)
	message := cause.Error()
	if scheduled {
		if recorder, ok := s.runtime.(scheduledRotationFailureRecorder); ok {
			if err := recorder.RecordScheduledRotationFailure(persistCtx, accountID, now, hardExpired, message); err != nil {
				return fmt.Errorf("rotation failed: %v; record scheduled failure: %w", cause, err)
			}
		} else if err := s.runtime.RecordRotationFailure(persistCtx, accountID, now, hardExpired, message); err != nil {
			return fmt.Errorf("rotation failed: %v; record failure: %w", cause, err)
		}
	} else if err := s.runtime.RecordRotationFailure(persistCtx, accountID, now, hardExpired, message); err != nil {
		return fmt.Errorf("rotation failed: %v; record failure: %w", cause, err)
	}
	return cause
}

func (s *ManagedProxyRuntimeService) Migrate(ctx context.Context, accountID, configID int64) (*ManagedProxyAccountDTO, error) {
	managed, err := s.runtime.GetByAccountID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	target := CatProxiesProxyTarget{Country: managed.Lease.TargetCountry, State: managed.Lease.TargetState, City: managed.Lease.TargetCity}
	strict := managed.Lease.Strict
	target.Strict = &strict
	candidate, err := s.prepareCandidate(ctx, configID, target, false)
	if err != nil {
		return nil, err
	}
	updated, err := s.runtime.Swap(ctx, accountID, candidate)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *ManagedProxyRuntimeService) Release(ctx context.Context, accountID int64) error {
	return s.runtime.Release(ctx, accountID)
}

func (s *ManagedProxyRuntimeService) NotifyTransportFailure(ctx context.Context, accountID, observedProxyID int64, err error) {
	if s == nil || s.runtime == nil || accountID <= 0 || observedProxyID <= 0 || err == nil {
		return
	}
	observedAt := s.now()
	message := err.Error()
	s.recordTransportFault(ctx, accountID, observedProxyID, "transport_failure_threshold", func(attemptCtx context.Context) (bool, bool, error) {
		return s.runtime.RecordTransportFailure(attemptCtx, accountID, observedProxyID, observedAt, message)
	})
}

func (s *ManagedProxyRuntimeService) NotifyImmediateTransportFailure(ctx context.Context, accountID, observedProxyID int64, err error) {
	if s == nil || s.runtime == nil || accountID <= 0 || observedProxyID <= 0 || err == nil {
		return
	}
	recorder, ok := s.runtime.(immediateTransportFailureRecorder)
	if !ok {
		return
	}
	observedAt := s.now()
	message := err.Error()
	s.recordTransportFault(ctx, accountID, observedProxyID, "proxy_gateway_failure", func(attemptCtx context.Context) (bool, bool, error) {
		return recorder.RecordImmediateTransportFailure(attemptCtx, accountID, observedProxyID, observedAt, message)
	})
}

type managedProxyFaultRecorder func(context.Context) (recorded bool, trigger bool, err error)

func (s *ManagedProxyRuntimeService) recordTransportFault(ctx context.Context, accountID, observedProxyID int64, reason string, record managedProxyFaultRecorder) {
	persistCtx, cancel := managedProxyPersistenceContext(ctx)
	recorded, trigger, recordErr := record(persistCtx)
	cancel()
	if recordErr == nil {
		s.handleRecordedTransportFault(accountID, observedProxyID, reason, recorded, trigger)
		return
	}
	lookupCtx, lookupCancel := context.WithTimeout(context.Background(), 2*time.Second)
	providerID := s.providerIDForAccount(lookupCtx, accountID)
	lookupCancel()
	log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=%s result=retrying error=%v", accountID, providerID, reason, recordErr)
	select {
	case s.faultRetrySlots <- struct{}{}:
		go func() {
			defer func() { <-s.faultRetrySlots }()
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer retryCancel()
			retryRecorded, retryTrigger, retryErr := record(retryCtx)
			if retryErr != nil {
				log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=%s result=failed error=%v", accountID, providerID, reason, retryErr)
				return
			}
			s.handleRecordedTransportFault(accountID, observedProxyID, reason, retryRecorded, retryTrigger)
		}()
	default:
		log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=%s result=retry_queue_full error=%v", accountID, providerID, reason, recordErr)
	}
}

func (s *ManagedProxyRuntimeService) handleRecordedTransportFault(accountID, observedProxyID int64, reason string, recorded, trigger bool) {
	if !recorded {
		return
	}
	s.transportFailures.Store(accountID, observedProxyID)
	if trigger {
		s.triggerFaultRotation(accountID, reason)
	}
}

func (s *ManagedProxyRuntimeService) triggerFaultRotation(accountID int64, reason string) {
	go func() {
		rotateCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		providerID := int64(0)
		if managed, err := s.runtime.GetByAccountID(rotateCtx, accountID); err == nil && managed != nil {
			providerID = managed.Lease.ProviderConfigID
		}
		_, err := s.Rotate(rotateCtx, accountID)
		if err != nil {
			log.Printf("[CatProxiesRotation] account_id=%d provider_id=%d reason=%s result=failed error=%v", accountID, providerID, reason, err)
			return
		}
		log.Printf("[CatProxiesRotation] account_id=%d provider_id=%d reason=%s result=succeeded", accountID, providerID, reason)
	}()
}

func (s *ManagedProxyRuntimeService) NotifyRateLimit(ctx context.Context, accountID int64) {
	if s == nil || s.runtime == nil || accountID <= 0 {
		return
	}
	if recorder, ok := s.runtime.(rateLimitBackoffRecorder); ok {
		persistCtx, cancel := managedProxyPersistenceContext(ctx)
		defer cancel()
		now := s.now()
		_ = recorder.RecordRateLimitBackoff(persistCtx, accountID, now, now.Add(managedProxyRateLimitBackoff))
	}
}

func (s *ManagedProxyRuntimeService) NotifyProviderCredentialError(ctx context.Context, accountID, observedProxyID int64, observedProxyUpdatedAt time.Time, err error) {
	if s == nil || s.runtime == nil || accountID <= 0 || observedProxyID <= 0 || observedProxyUpdatedAt.IsZero() || err == nil {
		return
	}
	persistCtx, cancel := managedProxyPersistenceContext(ctx)
	defer cancel()
	providerID := s.providerIDForAccount(persistCtx, accountID)
	marked, markErr := s.runtime.MarkProviderCredentialErrorByAccount(persistCtx, accountID, observedProxyID, observedProxyUpdatedAt, err.Error())
	if markErr != nil {
		log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=credential_error result=failed error=%v", accountID, providerID, markErr)
		return
	}
	if !marked {
		log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=credential_error result=stale_ignored", accountID, providerID)
		return
	}
	log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=credential_error result=blocked", accountID, providerID)
}

func (s *ManagedProxyRuntimeService) NotifyGatewayFailure(ctx context.Context, accountID, observedProxyID int64, err error) {
	if s == nil || s.runtime == nil || accountID <= 0 || observedProxyID <= 0 || err == nil {
		return
	}
	marker, ok := s.runtime.(accountNotReadyMarker)
	if !ok {
		return
	}
	persistCtx, cancel := managedProxyPersistenceContext(ctx)
	defer cancel()
	providerID := s.providerIDForAccount(persistCtx, accountID)
	marked, markErr := marker.MarkAccountNotReady(persistCtx, accountID, observedProxyID, err.Error())
	if markErr != nil {
		log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=gateway_dns result=failed error=%v", accountID, providerID, markErr)
		return
	}
	if !marked {
		log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=gateway_dns result=stale_ignored", accountID, providerID)
		return
	}
	log.Printf("[CatProxiesFault] account_id=%d provider_id=%d reason=gateway_dns result=blocked", accountID, providerID)
}

func (s *ManagedProxyRuntimeService) providerIDForAccount(ctx context.Context, accountID int64) int64 {
	managed, err := s.runtime.GetByAccountID(ctx, accountID)
	if err != nil || managed == nil {
		return 0
	}
	return managed.Lease.ProviderConfigID
}

func (s *ManagedProxyRuntimeService) NotifyTransportSuccess(ctx context.Context, accountID, observedProxyID int64) {
	if s == nil || s.runtime == nil || accountID <= 0 || observedProxyID <= 0 {
		return
	}
	recordedProxyID, recorded := s.transportFailures.Load(accountID)
	if !recorded || recordedProxyID != observedProxyID || !s.transportFailures.CompareAndDelete(accountID, recordedProxyID) {
		return
	}
	persistCtx, cancel := managedProxyPersistenceContext(ctx)
	defer cancel()
	if err := s.runtime.ClearTransportFailures(persistCtx, accountID, observedProxyID); err != nil {
		s.transportFailures.LoadOrStore(accountID, observedProxyID)
	}
}

func managedProxyPersistenceContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
}

func (s *ManagedProxyRuntimeService) prepareCandidate(ctx context.Context, configID int64, target CatProxiesProxyTarget, allowRetiring bool) (ManagedProxyCandidate, error) {
	config, err := s.catproxies.configRepo.GetByID(ctx, configID)
	if err != nil {
		return ManagedProxyCandidate{}, err
	}
	if config.Status != CatProxiesStatusActive && !(allowRetiring && config.Status == CatProxiesStatusRetiring) {
		return ManagedProxyCandidate{}, fmt.Errorf("catproxies config %d is not available for this operation", configID)
	}
	if s.catproxies.targeting != nil {
		resolved := resolveCatProxiesTarget(config, target)
		if err := s.catproxies.targeting.ValidateTarget(resolved.Country, resolved.State, resolved.City); err != nil {
			return ManagedProxyCandidate{}, catProxiesBadRequest(err)
		}
	}
	derived, err := DeriveCatProxiesProxy(config, target, s.now())
	if err != nil {
		return ManagedProxyCandidate{}, err
	}
	probe, err := s.catproxies.ProbeProxy(ctx, derived.Proxy, derived.Target)
	if err != nil {
		return ManagedProxyCandidate{}, fmt.Errorf("probe managed proxy candidate: %w", err)
	}
	return ManagedProxyCandidate{
		ProviderConfigID:  configID,
		ProviderUpdatedAt: config.UpdatedAt,
		LifetimeMinutes:   config.LifetimeMinutes,
		Derived:           *derived,
		Probe:             *probe,
	}, nil
}
