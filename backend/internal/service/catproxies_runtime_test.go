package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type managedProxyRuntimeRepoStub struct {
	managed                      *ManagedProxyAccountDTO
	recordTransportErr           error
	recordTransportRetrySucceeds bool
	recordTransportRetried       chan struct{}
	recordTransportNotRecorded   bool
	recordTransportCalls         int
	lastRecordContextErr         error
	clearTransportCalls          int
	recordRotationCalls          int
	activateCalls                int
	swapCalls                    int
	readyCalls                   []bool
	passwordUpdates              int
	migrations                   [][]ManagedProxyProviderCandidate
	reactivations                [][]ManagedProxyProviderCandidate
	reactivationFailures         []map[int64]string
	updatedConfig                *CatProxyProviderConfig
}

func (r *managedProxyRuntimeRepoStub) List(context.Context) ([]ManagedProxyAccountDTO, error) {
	return nil, nil
}
func (r *managedProxyRuntimeRepoStub) GetByAccountID(context.Context, int64) (*ManagedProxyAccountDTO, error) {
	if r.managed == nil {
		return nil, ErrManagedProxyLeaseNotFound
	}
	copy := *r.managed
	return &copy, nil
}
func (r *managedProxyRuntimeRepoStub) ListDue(context.Context, time.Time, int) ([]ManagedProxyAccountDTO, error) {
	return nil, nil
}
func (r *managedProxyRuntimeRepoStub) Activate(_ context.Context, _ int64, _ ManagedProxyCandidate) (*ManagedProxyAccountDTO, error) {
	r.activateCalls++
	return r.managed, nil
}
func (r *managedProxyRuntimeRepoStub) Swap(_ context.Context, _ int64, _ ManagedProxyCandidate) (*ManagedProxyAccountDTO, error) {
	r.swapCalls++
	return r.managed, nil
}
func (r *managedProxyRuntimeRepoStub) RecordRotationFailure(context.Context, int64, time.Time, bool, string) error {
	r.recordRotationCalls++
	return nil
}
func (r *managedProxyRuntimeRepoStub) RecordScheduledRotationFailure(context.Context, int64, time.Time, bool, string) error {
	r.recordRotationCalls++
	return nil
}
func (*managedProxyRuntimeRepoStub) Release(context.Context, int64) error { return nil }
func (r *managedProxyRuntimeRepoStub) UpdateProviderPassword(_ context.Context, config *CatProxyProviderConfig) error {
	r.passwordUpdates++
	copy := *config
	r.updatedConfig = &copy
	return nil
}
func (r *managedProxyRuntimeRepoStub) MigrateProvider(_ context.Context, config *CatProxyProviderConfig, candidates []ManagedProxyProviderCandidate) error {
	copy := *config
	r.updatedConfig = &copy
	r.migrations = append(r.migrations, append([]ManagedProxyProviderCandidate(nil), candidates...))
	return nil
}
func (r *managedProxyRuntimeRepoStub) ReactivateProvider(_ context.Context, config *CatProxyProviderConfig, candidates []ManagedProxyProviderCandidate, failures map[int64]string, _ time.Time) error {
	copy := *config
	r.updatedConfig = &copy
	r.reactivations = append(r.reactivations, append([]ManagedProxyProviderCandidate(nil), candidates...))
	failureCopy := make(map[int64]string, len(failures))
	for accountID, message := range failures {
		failureCopy[accountID] = message
	}
	r.reactivationFailures = append(r.reactivationFailures, failureCopy)
	return nil
}
func (r *managedProxyRuntimeRepoStub) UpdateProviderStatus(_ context.Context, config *CatProxyProviderConfig, ready bool, updateReady bool) error {
	copy := *config
	r.updatedConfig = &copy
	if updateReady {
		r.readyCalls = append(r.readyCalls, ready)
	}
	return nil
}
func (r *managedProxyRuntimeRepoStub) SetProviderReady(_ context.Context, _ int64, ready bool) error {
	r.readyCalls = append(r.readyCalls, ready)
	return nil
}
func (*managedProxyRuntimeRepoStub) MarkAccountNotReady(context.Context, int64, int64, string) (bool, error) {
	return true, nil
}
func (*managedProxyRuntimeRepoStub) MarkProviderCredentialErrorByAccount(context.Context, int64, int64, time.Time, string) (bool, error) {
	return true, nil
}
func (r *managedProxyRuntimeRepoStub) RecordTransportFailure(ctx context.Context, _, _ int64, _ time.Time, _ string) (bool, bool, error) {
	r.recordTransportCalls++
	r.lastRecordContextErr = ctx.Err()
	if r.recordTransportCalls > 1 && r.recordTransportRetried != nil {
		close(r.recordTransportRetried)
		r.recordTransportRetried = nil
	}
	if r.recordTransportRetrySucceeds && r.recordTransportCalls > 1 {
		return true, false, nil
	}
	return !r.recordTransportNotRecorded, false, r.recordTransportErr
}
func (r *managedProxyRuntimeRepoStub) ClearTransportFailures(context.Context, int64, int64) error {
	r.clearTransportCalls++
	return nil
}

func TestManagedProxyAccountDTOJSONOmitsSessionID(t *testing.T) {
	dto := ManagedProxyAccountDTO{Lease: ManagedProxyLease{AccountID: 42, SessionID: "secret-session"}}
	encoded, err := json.Marshal(dto)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-session")
	require.Contains(t, string(encoded), `"account_id":42`)
}

func TestManagedProxyRuntimeServiceTransportSuccessSkipsRepositoryWithoutRecordedFailure(t *testing.T) {
	repo := &managedProxyRuntimeRepoStub{}
	svc := NewManagedProxyRuntimeService(nil, repo)

	svc.NotifyTransportSuccess(context.Background(), 42, 77)
	require.Zero(t, repo.clearTransportCalls)

	svc.NotifyTransportFailure(context.Background(), 42, 77, errors.New("connection reset"))
	require.Equal(t, 1, repo.recordTransportCalls)
	svc.NotifyTransportSuccess(context.Background(), 42, 77)
	require.Equal(t, 1, repo.clearTransportCalls)
	svc.NotifyTransportSuccess(context.Background(), 42, 77)
	require.Equal(t, 1, repo.clearTransportCalls)
}

func TestManagedProxyRuntimeServiceStaleSuccessDoesNotClearCurrentProxyFailure(t *testing.T) {
	repo := &managedProxyRuntimeRepoStub{}
	svc := NewManagedProxyRuntimeService(nil, repo)

	svc.NotifyTransportFailure(context.Background(), 42, 77, errors.New("connection reset"))
	svc.NotifyTransportSuccess(context.Background(), 42, 76)
	require.Zero(t, repo.clearTransportCalls)
	markedProxyID, recorded := svc.transportFailures.Load(int64(42))
	require.True(t, recorded)
	require.Equal(t, int64(77), markedProxyID)

	svc.NotifyTransportSuccess(context.Background(), 42, 77)
	require.Equal(t, 1, repo.clearTransportCalls)
	_, recorded = svc.transportFailures.Load(int64(42))
	require.False(t, recorded)
}

func TestManagedProxyRuntimeServicePersistsFailureAfterRequestCancellation(t *testing.T) {
	repo := &managedProxyRuntimeRepoStub{}
	svc := NewManagedProxyRuntimeService(nil, repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc.NotifyTransportFailure(ctx, 42, 77, errors.New("connection reset"))

	require.Equal(t, 1, repo.recordTransportCalls)
	require.NoError(t, repo.lastRecordContextErr)
}

func TestManagedProxyRuntimeServiceUnrecordedFailureDoesNotArmSuccessClear(t *testing.T) {
	repo := &managedProxyRuntimeRepoStub{recordTransportNotRecorded: true}
	svc := NewManagedProxyRuntimeService(nil, repo)

	svc.NotifyTransportFailure(context.Background(), 42, 77, errors.New("connection reset"))
	svc.NotifyTransportSuccess(context.Background(), 42, 77)
	require.Zero(t, repo.clearTransportCalls)
}

func TestManagedProxyRuntimeServiceRetriesFailedFailureWrite(t *testing.T) {
	retried := make(chan struct{})
	repo := &managedProxyRuntimeRepoStub{
		recordTransportErr: context.DeadlineExceeded, recordTransportRetrySucceeds: true,
		recordTransportRetried: retried,
	}
	svc := NewManagedProxyRuntimeService(nil, repo)

	svc.NotifyTransportFailure(context.Background(), 42, 77, errors.New("connection reset"))
	select {
	case <-retried:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fault persistence retry")
	}
	require.Eventually(t, func() bool {
		_, recorded := svc.transportFailures.Load(int64(42))
		return recorded
	}, time.Second, 5*time.Millisecond)
	svc.NotifyTransportSuccess(context.Background(), 42, 77)
	require.Equal(t, 2, repo.recordTransportCalls)
	require.Equal(t, 1, repo.clearTransportCalls)
}

func TestManagedProxyRuntimeServiceRetiringAllowsRotateButNotNewAllocation(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 7, Name: "retiring", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusRetiring,
		Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer-type-residential",
		Password: "secret", LifetimeMinutes: 60,
	}}
	managed := &ManagedProxyAccountDTO{Lease: ManagedProxyLease{AccountID: 9, ProviderConfigID: 7, LifetimeMinutes: 60}}
	runtimeRepo := &managedProxyRuntimeRepoStub{managed: managed}
	catproxies := NewCatProxiesManagedProxyService(configRepo, &catProxiesLeaseRepoStub{}, &catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.4"}}, nil, runtimeRepo)
	svc := NewManagedProxyRuntimeService(catproxies, runtimeRepo)

	_, err := svc.Rotate(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, 1, runtimeRepo.swapCalls)

	_, err = svc.Manage(context.Background(), 10, 7, CatProxiesProxyTarget{})
	require.Error(t, err)
	require.Zero(t, runtimeRepo.activateCalls)

	_, err = svc.Migrate(context.Background(), 9, 7)
	require.Error(t, err)
	require.Equal(t, 1, runtimeRepo.swapCalls)

	for _, status := range []string{CatProxiesStatusDisabled, CatProxiesStatusCredentialError} {
		configRepo.config.Status = status
		_, err = svc.Rotate(context.Background(), 9)
		require.Error(t, err)
		require.Equal(t, 1, runtimeRepo.swapCalls)
	}
	// A rejected migration never changes or marks the still-valid source lease.
	require.Equal(t, 2, runtimeRepo.recordRotationCalls)
}

func TestAdminManagedAccountProxyMutationRejectsManagedLease(t *testing.T) {
	leaseRepo := &catProxiesLeaseRepoStub{leases: []ManagedProxyLease{{AccountID: 55, ProxyID: 77}}}
	leaseRepo.accountLease = &leaseRepo.leases[0]
	svc := &adminServiceImpl{managedProxyLeaseRepo: leaseRepo}

	err := svc.rejectManagedAccountProxyMutation(context.Background(), 55)
	require.ErrorIs(t, err, ErrManagedProxyAccountProxyImmutable)
	require.NoError(t, (&adminServiceImpl{managedProxyLeaseRepo: &catProxiesLeaseRepoStub{}}).rejectManagedAccountProxyMutation(context.Background(), 56))
}

func TestAdminProxyUpdateDeleteRejectManagedLease(t *testing.T) {
	leaseRepo := &catProxiesLeaseRepoStub{proxyLease: &ManagedProxyLease{ProxyID: 77}}
	svc := &adminServiceImpl{managedProxyLeaseRepo: leaseRepo}

	_, err := svc.UpdateProxy(context.Background(), 77, &UpdateProxyInput{})
	require.ErrorIs(t, err, ErrProxyInUse)
	err = svc.DeleteProxy(context.Background(), 77)
	require.ErrorIs(t, err, ErrProxyInUse)

	ordinary := &adminServiceImpl{managedProxyLeaseRepo: &catProxiesLeaseRepoStub{}}
	require.NoError(t, ordinary.rejectManagedProxyMutation(context.Background(), 78))
}

func TestCatProxiesManagedProxyServiceRetiringKeepsReadyAndClearsDefault(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 7, Name: "default", ProviderType: CatProxiesProviderType, Status: CatProxiesStatusActive, IsDefault: true,
		Protocol: CatProxiesProtocolHTTP, Host: "proxy.example.com", BaseUsername: "customer-type-residential",
		Password: "secret", LifetimeMinutes: 60,
	}}
	runtimeRepo := &managedProxyRuntimeRepoStub{}
	svc := NewCatProxiesManagedProxyService(configRepo, &catProxiesLeaseRepoStub{}, nil, nil, runtimeRepo)

	dto, err := svc.UpdateConfigStatus(context.Background(), 7, CatProxiesStatusRetiring)
	require.NoError(t, err)
	require.False(t, dto.IsDefault)
	require.Empty(t, runtimeRepo.readyCalls)

	_, err = svc.UpdateConfigStatus(context.Background(), 7, CatProxiesStatusDisabled)
	require.NoError(t, err)
	require.Equal(t, []bool{false}, runtimeRepo.readyCalls)
}

var _ ManagedProxyRuntimeRepository = (*managedProxyRuntimeRepoStub)(nil)
