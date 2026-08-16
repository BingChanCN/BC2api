package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/ent/catproxyproviderconfig"
	"github.com/Wei-Shaw/sub2api/ent/managedproxylease"
	dbproxy "github.com/Wei-Shaw/sub2api/ent/proxy"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type managedProxyRuntimeRepository struct {
	client *dbent.Client
	db     *sql.DB
}

func NewManagedProxyRuntimeRepository(client *dbent.Client, db *sql.DB) service.ManagedProxyRuntimeRepository {
	return &managedProxyRuntimeRepository{client: client, db: db}
}

func (r *managedProxyRuntimeRepository) List(ctx context.Context) ([]service.ManagedProxyAccountDTO, error) {
	rows, err := r.client.ManagedProxyLease.Query().WithAccount().WithProviderConfig().Order(dbent.Asc(managedproxylease.FieldAccountID)).All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]service.ManagedProxyAccountDTO, 0, len(rows))
	for _, row := range rows {
		result = append(result, managedProxyRuntimeDTO(row))
	}
	return result, nil
}

func (r *managedProxyRuntimeRepository) GetByAccountID(ctx context.Context, accountID int64) (*service.ManagedProxyAccountDTO, error) {
	row, err := r.client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(accountID)).WithAccount().WithProviderConfig().Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, nil)
	}
	result := managedProxyRuntimeDTO(row)
	return &result, nil
}

func (r *managedProxyRuntimeRepository) ListDue(ctx context.Context, now time.Time, limit int) ([]service.ManagedProxyAccountDTO, error) {
	query := r.client.ManagedProxyLease.Query().Where(
		managedproxylease.NextRotationAtNotNil(), managedproxylease.NextRotationAtLTE(now),
		managedproxylease.StateIn(managedproxylease.StateActive, managedproxylease.StateFailed, managedproxylease.StateExpired),
		managedproxylease.HasProviderConfigWith(catproxyproviderconfig.StatusIn(
			catproxyproviderconfig.StatusActive,
			catproxyproviderconfig.StatusRetiring,
		)),
	).WithAccount().WithProviderConfig().Order(dbent.Asc(managedproxylease.FieldNextRotationAt)).Limit(limit)
	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]service.ManagedProxyAccountDTO, 0, len(rows))
	for _, row := range rows {
		result = append(result, managedProxyRuntimeDTO(row))
	}
	return result, nil
}

func (r *managedProxyRuntimeRepository) Activate(ctx context.Context, accountID int64, candidate service.ManagedProxyCandidate) (*service.ManagedProxyAccountDTO, error) {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return r.activateInTx(ctx, tx, accountID, candidate)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := r.activateInTx(ctx, tx, accountID, candidate)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *managedProxyRuntimeRepository) activateInTx(ctx context.Context, tx *dbent.Tx, accountID int64, candidate service.ManagedProxyCandidate) (*service.ManagedProxyAccountDTO, error) {
	if err := lockCandidateProvider(ctx, tx, candidate, catproxyproviderconfig.StatusActive); err != nil {
		return nil, err
	}
	if _, err := lockAccount(ctx, tx, accountID); err != nil {
		return nil, translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	if _, err := tx.Account.Get(ctx, accountID); err != nil {
		return nil, translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	exists, err := tx.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(accountID)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, service.ErrManagedProxyLeaseExists
	}
	proxyRow, err := createManagedProxy(ctx, tx, candidate)
	if err != nil {
		return nil, err
	}
	ready := true
	if candidate.InitialReady != nil {
		ready = *candidate.InitialReady
	}
	if _, err := tx.Account.UpdateOneID(accountID).SetProxyID(proxyRow.ID).SetManagedProxyReady(ready).Save(ctx); err != nil {
		return nil, err
	}
	leaseRow, err := createManagedLease(ctx, tx, accountID, proxyRow.ID, candidate)
	if err != nil {
		return nil, err
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, []int64{accountID}); err != nil {
		return nil, err
	}
	hydrated, err := tx.ManagedProxyLease.Query().Where(managedproxylease.IDEQ(leaseRow.ID)).WithAccount().WithProviderConfig().Only(ctx)
	if err != nil {
		return nil, err
	}
	result := managedProxyRuntimeDTO(hydrated)
	return &result, nil
}

func (r *managedProxyRuntimeRepository) Swap(ctx context.Context, accountID int64, candidate service.ManagedProxyCandidate) (*service.ManagedProxyAccountDTO, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockCandidateProvider(ctx, tx, candidate, catproxyproviderconfig.StatusActive, catproxyproviderconfig.StatusRetiring); err != nil {
		return nil, err
	}
	if _, err := lockAccount(ctx, tx, accountID); err != nil {
		return nil, translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	lease, err := lockManagedLease(ctx, tx, accountID)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, nil)
	}
	oldProxyID := lease.ProxyID
	proxyRow, err := createManagedProxy(ctx, tx, candidate)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Account.UpdateOneID(accountID).SetProxyID(proxyRow.ID).SetManagedProxyReady(true).Save(ctx); err != nil {
		return nil, err
	}
	updated, err := tx.ManagedProxyLease.UpdateOne(lease).
		SetProxyID(proxyRow.ID).
		SetProviderConfigID(candidate.ProviderConfigID).
		SetSessionID(candidate.Derived.SessionID).
		SetNillableTargetCountry(candidate.Derived.Target.Country).
		SetNillableTargetState(candidate.Derived.Target.State).
		SetNillableTargetCity(candidate.Derived.Target.City).
		SetStrict(catProxiesTargetStrict(candidate.Derived.Target)).
		SetLifetimeMinutes(candidateLifetime(candidate)).
		SetState(managedproxylease.StateActive).
		SetHealthStatus(managedproxylease.HealthStatusHealthy).
		SetHealthCheckedAt(candidate.Probe.CheckedAt).
		SetNillableObservedExitIP(candidate.Probe.ExitIP).
		SetNillableObservedCountry(candidate.Probe.Country).
		SetNillableObservedState(candidate.Probe.State).
		SetNillableObservedCity(candidate.Probe.City).
		SetObservedLatencyMs(candidate.Probe.LatencyMs).
		SetLastRotatedAt(candidate.Probe.CheckedAt).
		SetNextRotationAt(candidate.Derived.Timing.RotateAt).
		SetExpiresAt(candidate.Derived.Timing.HardExpiresAt).
		SetConsecutiveFailureCount(0).
		ClearLastError().ClearLastErrorAt().Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.Proxy.DeleteOneID(oldProxyID).Exec(mixins.SkipSoftDelete(ctx)); err != nil {
		return nil, fmt.Errorf("delete replaced managed proxy: %w", err)
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, []int64{accountID}); err != nil {
		return nil, err
	}
	hydrated, err := tx.ManagedProxyLease.Query().Where(managedproxylease.IDEQ(updated.ID)).WithAccount().WithProviderConfig().Only(ctx)
	if err != nil {
		return nil, err
	}
	result := managedProxyRuntimeDTO(hydrated)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *managedProxyRuntimeRepository) RecordRotationFailure(ctx context.Context, accountID int64, now time.Time, hardExpired bool, message string) error {
	return r.recordRotationFailure(ctx, accountID, now, hardExpired, false, message)
}

func (r *managedProxyRuntimeRepository) RecordScheduledRotationFailure(ctx context.Context, accountID int64, now time.Time, hardExpired bool, message string) error {
	return r.recordRotationFailure(ctx, accountID, now, hardExpired, true, message)
}

func (r *managedProxyRuntimeRepository) recordRotationFailure(ctx context.Context, accountID int64, now time.Time, hardExpired, scheduled bool, message string) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockAccount(ctx, tx, accountID); err != nil {
		return translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	lease, err := lockManagedLease(ctx, tx, accountID)
	if err != nil {
		return translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, nil)
	}
	update := tx.ManagedProxyLease.UpdateOne(lease).
		AddFailureCount(1).
		AddConsecutiveFailureCount(1).
		SetLastError(message).
		SetLastErrorAt(now).
		SetNextRotationAt(now.Add(time.Minute))
	closeGate := !scheduled || hardExpired
	if hardExpired {
		update.SetState(managedproxylease.StateExpired).SetHealthStatus(managedproxylease.HealthStatusUnhealthy)
	} else if scheduled {
		update.SetState(managedproxylease.StateFailed).
			SetHealthStatus(managedproxylease.HealthStatusDegraded)
	} else {
		update.SetState(managedproxylease.StateFailed).SetHealthStatus(managedproxylease.HealthStatusUnhealthy)
	}
	if closeGate {
		if _, err := tx.Account.UpdateOneID(accountID).SetManagedProxyReady(false).Save(ctx); err != nil {
			return err
		}
	}
	if _, err := update.Save(ctx); err != nil {
		return err
	}
	if closeGate {
		if err := enqueueManagedProxyAccounts(ctx, tx, []int64{accountID}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) Release(ctx context.Context, accountID int64) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockAccount(ctx, tx, accountID); err != nil {
		return translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	lease, err := lockManagedLease(ctx, tx, accountID)
	if err != nil {
		return translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, nil)
	}
	if _, err := tx.Account.Update().Where(dbaccount.IDEQ(accountID), dbaccount.ProxyIDEQ(lease.ProxyID)).ClearProxyID().SetManagedProxyReady(true).Save(ctx); err != nil {
		return err
	}
	if err := tx.ManagedProxyLease.DeleteOne(lease).Exec(ctx); err != nil {
		return err
	}
	if err := tx.Proxy.DeleteOneID(lease.ProxyID).Exec(mixins.SkipSoftDelete(ctx)); err != nil {
		return err
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, []int64{accountID}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (r *managedProxyRuntimeRepository) UpdateProviderPassword(ctx context.Context, config *service.CatProxyProviderConfig) error {
	if config == nil {
		return service.ErrCatProxyProviderConfigNotFound
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProviderRevision(ctx, tx, config.ID, config.UpdatedAt); err != nil {
		return err
	}
	leases, err := lockProviderLeases(ctx, tx, config.ID)
	if err != nil {
		return err
	}
	accountIDs := make([]int64, 0, len(leases))
	for _, lease := range leases {
		if _, err := tx.Proxy.UpdateOneID(lease.ProxyID).SetPassword(config.Password).Save(ctx); err != nil {
			return err
		}
		if _, err := tx.ManagedProxyLease.UpdateOne(lease).SetLifetimeMinutes(config.LifetimeMinutes).Save(ctx); err != nil {
			return err
		}
		accountIDs = append(accountIDs, lease.AccountID)
	}
	if err := updateCatProxyProviderConfigTx(ctx, tx, config); err != nil {
		return err
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, accountIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) MigrateProvider(ctx context.Context, config *service.CatProxyProviderConfig, candidates []service.ManagedProxyProviderCandidate) error {
	if config == nil {
		return service.ErrCatProxyProviderConfigNotFound
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProviderRevision(ctx, tx, config.ID, config.UpdatedAt); err != nil {
		return err
	}
	if err := lockProviderAccounts(ctx, tx, config.ID); err != nil {
		return err
	}
	leases, err := lockProviderLeases(ctx, tx, config.ID)
	if err != nil {
		return err
	}
	byAccount := make(map[int64]service.ManagedProxyCandidate, len(candidates))
	for _, item := range candidates {
		if item.AccountID <= 0 || item.Candidate.ProviderConfigID != config.ID ||
			item.Candidate.ProviderUpdatedAt.IsZero() || !item.Candidate.ProviderUpdatedAt.Equal(config.UpdatedAt) {
			return service.ErrManagedProxyBindingsChanged
		}
		if _, exists := byAccount[item.AccountID]; exists {
			return service.ErrManagedProxyBindingsChanged
		}
		byAccount[item.AccountID] = item.Candidate
	}
	if len(leases) != len(byAccount) {
		return service.ErrManagedProxyBindingsChanged
	}
	accountIDs := make([]int64, 0, len(leases))
	ready := config.Status == service.CatProxiesStatusActive || config.Status == service.CatProxiesStatusRetiring
	for _, lease := range leases {
		candidate, exists := byAccount[lease.AccountID]
		if !exists {
			return service.ErrManagedProxyBindingsChanged
		}
		oldProxyID := lease.ProxyID
		proxyRow, err := createManagedProxy(ctx, tx, candidate)
		if err != nil {
			return err
		}
		if _, err := tx.Account.UpdateOneID(lease.AccountID).SetProxyID(proxyRow.ID).SetManagedProxyReady(ready).Save(ctx); err != nil {
			return err
		}
		if _, err := tx.ManagedProxyLease.UpdateOne(lease).
			SetProxyID(proxyRow.ID).
			SetProviderConfigID(config.ID).
			SetSessionID(candidate.Derived.SessionID).
			SetNillableTargetCountry(candidate.Derived.Target.Country).
			SetNillableTargetState(candidate.Derived.Target.State).
			SetNillableTargetCity(candidate.Derived.Target.City).
			SetStrict(catProxiesTargetStrict(candidate.Derived.Target)).
			SetLifetimeMinutes(candidateLifetime(candidate)).
			SetState(managedproxylease.StateActive).
			SetHealthStatus(managedproxylease.HealthStatusHealthy).
			SetHealthCheckedAt(candidate.Probe.CheckedAt).
			SetNillableObservedExitIP(candidate.Probe.ExitIP).
			SetNillableObservedCountry(candidate.Probe.Country).
			SetNillableObservedState(candidate.Probe.State).
			SetNillableObservedCity(candidate.Probe.City).
			SetObservedLatencyMs(candidate.Probe.LatencyMs).
			SetLastRotatedAt(candidate.Probe.CheckedAt).
			SetNextRotationAt(candidate.Derived.Timing.RotateAt).
			SetExpiresAt(candidate.Derived.Timing.HardExpiresAt).
			SetConsecutiveFailureCount(0).
			ClearLastError().
			ClearLastErrorAt().
			Save(ctx); err != nil {
			return err
		}
		if err := tx.Proxy.DeleteOneID(oldProxyID).Exec(mixins.SkipSoftDelete(ctx)); err != nil {
			return fmt.Errorf("delete replaced managed proxy: %w", err)
		}
		accountIDs = append(accountIDs, lease.AccountID)
	}
	if err := updateCatProxyProviderConfigTx(ctx, tx, config); err != nil {
		return err
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, accountIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) ReactivateProvider(ctx context.Context, config *service.CatProxyProviderConfig, candidates []service.ManagedProxyProviderCandidate, failures map[int64]string, observedAt time.Time) error {
	if config == nil {
		return service.ErrCatProxyProviderConfigNotFound
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProviderRevision(ctx, tx, config.ID, config.UpdatedAt); err != nil {
		return err
	}
	if err := lockProviderAccounts(ctx, tx, config.ID); err != nil {
		return err
	}
	leases, err := lockProviderLeases(ctx, tx, config.ID)
	if err != nil {
		return err
	}
	byAccount := make(map[int64]service.ManagedProxyCandidate, len(candidates))
	for _, item := range candidates {
		if item.AccountID <= 0 || item.Candidate.ProviderConfigID != config.ID ||
			item.Candidate.ProviderUpdatedAt.IsZero() || !item.Candidate.ProviderUpdatedAt.Equal(config.UpdatedAt) {
			return service.ErrManagedProxyBindingsChanged
		}
		if _, exists := byAccount[item.AccountID]; exists {
			return service.ErrManagedProxyBindingsChanged
		}
		byAccount[item.AccountID] = item.Candidate
	}
	if len(leases) != len(byAccount)+len(failures) {
		return service.ErrManagedProxyBindingsChanged
	}
	accountIDs := make([]int64, 0, len(leases))
	for _, lease := range leases {
		candidate, succeeded := byAccount[lease.AccountID]
		failure, failed := failures[lease.AccountID]
		if succeeded == failed {
			return service.ErrManagedProxyBindingsChanged
		}
		if failed {
			if _, err := tx.Account.UpdateOneID(lease.AccountID).SetManagedProxyReady(false).Save(ctx); err != nil {
				return err
			}
			if _, err := tx.ManagedProxyLease.UpdateOne(lease).
				SetState(managedproxylease.StateFailed).
				SetHealthStatus(managedproxylease.HealthStatusUnhealthy).
				SetHealthCheckedAt(observedAt).
				SetNextRotationAt(observedAt.Add(time.Minute)).
				AddFailureCount(1).
				AddConsecutiveFailureCount(1).
				SetLastError(failure).
				SetLastErrorAt(observedAt).
				Save(ctx); err != nil {
				return err
			}
			accountIDs = append(accountIDs, lease.AccountID)
			continue
		}

		oldProxyID := lease.ProxyID
		proxyRow, err := createManagedProxy(ctx, tx, candidate)
		if err != nil {
			return err
		}
		if _, err := tx.Account.UpdateOneID(lease.AccountID).SetProxyID(proxyRow.ID).SetManagedProxyReady(true).Save(ctx); err != nil {
			return err
		}
		if _, err := tx.ManagedProxyLease.UpdateOne(lease).
			SetProxyID(proxyRow.ID).
			SetProviderConfigID(config.ID).
			SetSessionID(candidate.Derived.SessionID).
			SetNillableTargetCountry(candidate.Derived.Target.Country).
			SetNillableTargetState(candidate.Derived.Target.State).
			SetNillableTargetCity(candidate.Derived.Target.City).
			SetStrict(catProxiesTargetStrict(candidate.Derived.Target)).
			SetLifetimeMinutes(candidateLifetime(candidate)).
			SetState(managedproxylease.StateActive).
			SetHealthStatus(managedproxylease.HealthStatusHealthy).
			SetHealthCheckedAt(candidate.Probe.CheckedAt).
			SetNillableObservedExitIP(candidate.Probe.ExitIP).
			SetNillableObservedCountry(candidate.Probe.Country).
			SetNillableObservedState(candidate.Probe.State).
			SetNillableObservedCity(candidate.Probe.City).
			SetObservedLatencyMs(candidate.Probe.LatencyMs).
			SetLastRotatedAt(candidate.Probe.CheckedAt).
			SetNextRotationAt(candidate.Derived.Timing.RotateAt).
			SetExpiresAt(candidate.Derived.Timing.HardExpiresAt).
			SetConsecutiveFailureCount(0).
			ClearLastError().
			ClearLastErrorAt().
			Save(ctx); err != nil {
			return err
		}
		if err := tx.Proxy.DeleteOneID(oldProxyID).Exec(mixins.SkipSoftDelete(ctx)); err != nil {
			return fmt.Errorf("delete replaced managed proxy: %w", err)
		}
		accountIDs = append(accountIDs, lease.AccountID)
	}
	if err := updateCatProxyProviderConfigTx(ctx, tx, config); err != nil {
		return err
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, accountIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func updateCatProxyProviderConfigTx(ctx context.Context, tx *dbent.Tx, config *service.CatProxyProviderConfig) error {
	update := tx.CatProxyProviderConfig.UpdateOneID(config.ID).
		SetName(config.Name).
		SetProviderType(catproxyproviderconfig.ProviderType(config.ProviderType)).
		SetStatus(catproxyproviderconfig.Status(config.Status)).
		SetIsDefault(config.IsDefault).
		SetProtocol(catproxyproviderconfig.Protocol(config.Protocol)).
		SetHost(config.Host).
		SetBaseUsername(config.BaseUsername).
		SetPassword(config.Password).
		SetLifetimeMinutes(config.LifetimeMinutes).
		SetStrict(config.Strict)
	if config.DefaultCountry != nil {
		update.SetDefaultCountry(*config.DefaultCountry)
	} else {
		update.ClearDefaultCountry()
	}
	if config.DefaultState != nil {
		update.SetDefaultState(*config.DefaultState)
	} else {
		update.ClearDefaultState()
	}
	if config.DefaultCity != nil {
		update.SetDefaultCity(*config.DefaultCity)
	} else {
		update.ClearDefaultCity()
	}
	if config.LastProbeAt != nil {
		update.SetLastProbeAt(*config.LastProbeAt)
	} else {
		update.ClearLastProbeAt()
	}
	if config.LastProbeLatencyMs != nil {
		update.SetLastProbeLatencyMs(*config.LastProbeLatencyMs)
	} else {
		update.ClearLastProbeLatencyMs()
	}
	if config.LastError != nil {
		update.SetLastError(*config.LastError)
	} else {
		update.ClearLastError()
	}
	if config.LastErrorAt != nil {
		update.SetLastErrorAt(*config.LastErrorAt)
	} else {
		update.ClearLastErrorAt()
	}
	row, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrCatProxyProviderConfigNotFound, service.ErrCatProxyProviderConfigExists)
	}
	*config = *catProxyProviderConfigEntityToService(row)
	return nil
}

func (r *managedProxyRuntimeRepository) SetProviderReady(ctx context.Context, providerConfigID int64, ready bool) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockProvider(ctx, tx, providerConfigID); err != nil {
		return translatePersistenceError(err, service.ErrCatProxyProviderConfigNotFound, nil)
	}
	if err := lockProviderAccounts(ctx, tx, providerConfigID); err != nil {
		return err
	}
	leases, err := lockProviderLeases(ctx, tx, providerConfigID)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(leases))
	for _, lease := range leases {
		ids = append(ids, lease.AccountID)
	}
	if len(ids) > 0 {
		if _, err := tx.Account.Update().Where(dbaccount.IDIn(ids...)).SetManagedProxyReady(ready).Save(ctx); err != nil {
			return err
		}
		if err := enqueueManagedProxyAccounts(ctx, tx, ids); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) UpdateProviderStatus(ctx context.Context, config *service.CatProxyProviderConfig, ready bool, updateReady bool) error {
	if config == nil {
		return service.ErrCatProxyProviderConfigNotFound
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockProviderRevision(ctx, tx, config.ID, config.UpdatedAt); err != nil {
		return err
	}
	if updateReady {
		if err := lockProviderAccounts(ctx, tx, config.ID); err != nil {
			return err
		}
	}
	if err := updateCatProxyProviderConfigTx(ctx, tx, config); err != nil {
		return err
	}
	if !updateReady {
		return tx.Commit()
	}
	leases, err := lockProviderLeases(ctx, tx, config.ID)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(leases))
	for _, lease := range leases {
		ids = append(ids, lease.AccountID)
	}
	if len(ids) > 0 {
		if _, err := tx.Account.Update().Where(dbaccount.IDIn(ids...)).SetManagedProxyReady(ready).Save(ctx); err != nil {
			return err
		}
		if err := enqueueManagedProxyAccounts(ctx, tx, ids); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) MarkProviderCredentialErrorByAccount(ctx context.Context, accountID, observedProxyID int64, observedProxyUpdatedAt time.Time, message string) (bool, error) {
	// Read only the provider identity before starting the ordered lock sequence.
	// The provider row is the serialization point for all lifecycle operations.
	snapshot, err := r.client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(accountID)).Only(ctx)
	if dbent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	provider, err := lockProvider(ctx, tx, snapshot.ProviderConfigID)
	if err != nil {
		return false, err
	}
	if provider.Status != catproxyproviderconfig.StatusActive && provider.Status != catproxyproviderconfig.StatusRetiring {
		return false, nil
	}
	if err := lockProviderAccounts(ctx, tx, snapshot.ProviderConfigID); err != nil {
		return false, err
	}
	leases, err := lockProviderLeases(ctx, tx, snapshot.ProviderConfigID)
	if err != nil {
		return false, err
	}
	matched := false
	for _, lease := range leases {
		if lease.AccountID == accountID && lease.ProxyID == observedProxyID {
			matched = true
			break
		}
	}
	if !matched {
		return false, nil
	}
	currentProxy, err := lockProxy(ctx, tx, observedProxyID)
	if dbent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if observedProxyUpdatedAt.IsZero() || !currentProxy.UpdatedAt.Equal(observedProxyUpdatedAt) {
		return false, nil
	}
	now := time.Now()
	if _, err := tx.CatProxyProviderConfig.UpdateOneID(snapshot.ProviderConfigID).
		SetStatus(catproxyproviderconfig.StatusCredentialError).SetIsDefault(false).
		SetLastError(message).SetLastErrorAt(now).Save(ctx); err != nil {
		return false, err
	}
	ids := make([]int64, 0, len(leases))
	for _, bound := range leases {
		ids = append(ids, bound.AccountID)
	}
	if len(ids) > 0 {
		if _, err := tx.Account.Update().Where(dbaccount.IDIn(ids...)).SetManagedProxyReady(false).Save(ctx); err != nil {
			return false, err
		}
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, ids); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *managedProxyRuntimeRepository) RecordRateLimitBackoff(ctx context.Context, accountID int64, observedAt, until time.Time) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	account, err := lockAccount(ctx, tx, accountID)
	if dbent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := lockManagedLease(ctx, tx, accountID); dbent.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if err != nil {
		return err
	}
	if account.RateLimitResetAt != nil && account.RateLimitResetAt.After(until) {
		return nil
	}
	if _, err := tx.Account.UpdateOneID(accountID).SetRateLimitedAt(observedAt).SetRateLimitResetAt(until).Save(ctx); err != nil {
		return err
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, []int64{accountID}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) RecordTransportFailure(ctx context.Context, accountID, observedProxyID int64, now time.Time, message string) (bool, bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	lease, err := lockManagedLease(ctx, tx, accountID)
	if dbent.IsNotFound(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if lease.ProxyID != observedProxyID {
		return false, false, nil
	}
	count := lease.ConsecutiveFailureCount + 1
	if lease.LastErrorAt == nil || now.Sub(*lease.LastErrorAt) > 2*time.Minute {
		count = 1
	} else {
		account, accountErr := tx.Account.Query().Where(dbaccount.IDEQ(accountID)).Select(dbaccount.FieldLastUsedAt).Only(ctx)
		if accountErr != nil && !dbent.IsNotFound(accountErr) {
			return false, false, accountErr
		}
		if accountErr == nil && account.LastUsedAt != nil && account.LastUsedAt.After(*lease.LastErrorAt) {
			count = 1
		}
	}
	trigger := count >= 2 && (lease.LastRotatedAt == nil || now.Sub(*lease.LastRotatedAt) >= 5*time.Minute)
	update := tx.ManagedProxyLease.UpdateOne(lease).SetLastError(message).SetLastErrorAt(now).SetConsecutiveFailureCount(count)
	if trigger {
		// LastRotatedAt is the successful session timestamp. A failed trigger
		// must not consume the five-minute rotation cooldown.
		update.SetConsecutiveFailureCount(0)
	}
	if _, err = update.Save(ctx); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return true, trigger, nil
}

func (r *managedProxyRuntimeRepository) RecordImmediateTransportFailure(ctx context.Context, accountID, observedProxyID int64, now time.Time, message string) (bool, bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	lease, err := lockManagedLease(ctx, tx, accountID)
	if dbent.IsNotFound(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if lease.ProxyID != observedProxyID {
		return false, false, nil
	}
	trigger := lease.LastRotatedAt == nil || now.Sub(*lease.LastRotatedAt) >= 5*time.Minute
	update := tx.ManagedProxyLease.UpdateOne(lease).
		SetLastError(message).
		SetLastErrorAt(now).
		SetConsecutiveFailureCount(0)
	if _, err = update.Save(ctx); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return true, trigger, nil
}

func (r *managedProxyRuntimeRepository) RecordBindingsChangedRetry(ctx context.Context, accountID, observedProxyID int64, nextRetry time.Time) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	lease, err := lockManagedLease(ctx, tx, accountID)
	if dbent.IsNotFound(err) || (err == nil && lease.ProxyID != observedProxyID) {
		return nil
	}
	if err != nil {
		return err
	}
	if lease.ExpiresAt != nil && lease.ExpiresAt.Before(nextRetry) {
		nextRetry = *lease.ExpiresAt
	}
	if _, err := tx.ManagedProxyLease.UpdateOne(lease).SetNextRotationAt(nextRetry).Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) ClearTransportFailures(ctx context.Context, accountID, observedProxyID int64) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	lease, err := lockManagedLease(ctx, tx, accountID)
	if dbent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if lease.ProxyID != observedProxyID {
		return nil
	}
	if _, err := tx.ManagedProxyLease.UpdateOne(lease).SetConsecutiveFailureCount(0).ClearLastError().ClearLastErrorAt().Save(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *managedProxyRuntimeRepository) MarkAccountNotReady(ctx context.Context, accountID, observedProxyID int64, message string) (bool, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockAccount(ctx, tx, accountID); dbent.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	lease, err := lockManagedLease(ctx, tx, accountID)
	if dbent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if lease.ProxyID != observedProxyID {
		return false, nil
	}
	now := time.Now()
	if _, err := tx.ManagedProxyLease.UpdateOne(lease).
		SetState(managedproxylease.StateFailed).
		SetHealthStatus(managedproxylease.HealthStatusUnhealthy).
		SetLastError(message).
		SetLastErrorAt(now).
		SetNextRotationAt(now.Add(time.Minute)).
		Save(ctx); err != nil {
		return false, err
	}
	if _, err := tx.Account.UpdateOneID(accountID).SetManagedProxyReady(false).Save(ctx); err != nil {
		return false, err
	}
	if err := enqueueManagedProxyAccounts(ctx, tx, []int64{accountID}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func lockCandidateProvider(ctx context.Context, tx *dbent.Tx, candidate service.ManagedProxyCandidate, allowed ...catproxyproviderconfig.Status) error {
	provider, err := lockProvider(ctx, tx, candidate.ProviderConfigID)
	if err != nil {
		return translatePersistenceError(err, service.ErrCatProxyProviderConfigNotFound, nil)
	}
	if candidate.ProviderUpdatedAt.IsZero() || !provider.UpdatedAt.Equal(candidate.ProviderUpdatedAt) {
		return service.ErrManagedProxyBindingsChanged
	}
	for _, status := range allowed {
		if provider.Status == status {
			return nil
		}
	}
	return service.ErrManagedProxyBindingsChanged
}

func lockProviderRevision(ctx context.Context, tx *dbent.Tx, providerID int64, updatedAt time.Time) error {
	provider, err := lockProvider(ctx, tx, providerID)
	if err != nil {
		return translatePersistenceError(err, service.ErrCatProxyProviderConfigNotFound, nil)
	}
	if updatedAt.IsZero() || !provider.UpdatedAt.Equal(updatedAt) {
		return service.ErrManagedProxyBindingsChanged
	}
	return nil
}

func lockProvider(ctx context.Context, tx *dbent.Tx, providerID int64) (*dbent.CatProxyProviderConfig, error) {
	row, err := tx.CatProxyProviderConfig.Query().Where(catproxyproviderconfig.IDEQ(providerID)).ForUpdate().Only(ctx)
	if isSQLiteRowLockUnsupported(err) {
		return tx.CatProxyProviderConfig.Query().Where(catproxyproviderconfig.IDEQ(providerID)).Only(ctx)
	}
	return row, err
}

func lockProviderAccounts(ctx context.Context, tx *dbent.Tx, providerID int64) error {
	leases, err := tx.ManagedProxyLease.Query().
		Where(managedproxylease.ProviderConfigIDEQ(providerID)).
		Order(dbent.Asc(managedproxylease.FieldAccountID)).
		Select(managedproxylease.FieldAccountID).
		All(ctx)
	if err != nil || len(leases) == 0 {
		return err
	}
	accountIDs := make([]int64, 0, len(leases))
	for _, lease := range leases {
		accountIDs = append(accountIDs, lease.AccountID)
	}
	return lockAccounts(ctx, tx, accountIDs)
}

func lockAccounts(ctx context.Context, tx *dbent.Tx, accountIDs []int64) error {
	rows, err := tx.Account.Query().Where(dbaccount.IDIn(accountIDs...)).Order(dbent.Asc(dbaccount.FieldID)).ForUpdate().All(ctx)
	if isSQLiteRowLockUnsupported(err) {
		rows, err = tx.Account.Query().Where(dbaccount.IDIn(accountIDs...)).Order(dbent.Asc(dbaccount.FieldID)).All(ctx)
	}
	if err != nil {
		return err
	}
	if len(rows) != len(accountIDs) {
		return service.ErrManagedProxyBindingsChanged
	}
	return nil
}

func lockProviderLeases(ctx context.Context, tx *dbent.Tx, providerID int64) ([]*dbent.ManagedProxyLease, error) {
	query := func() *dbent.ManagedProxyLeaseQuery {
		return tx.ManagedProxyLease.Query().Where(managedproxylease.ProviderConfigIDEQ(providerID)).Order(dbent.Asc(managedproxylease.FieldAccountID))
	}
	rows, err := query().ForUpdate().All(ctx)
	if isSQLiteRowLockUnsupported(err) {
		return query().All(ctx)
	}
	return rows, err
}

func lockManagedLease(ctx context.Context, tx *dbent.Tx, accountID int64) (*dbent.ManagedProxyLease, error) {
	row, err := tx.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(accountID)).ForUpdate().Only(ctx)
	if isSQLiteRowLockUnsupported(err) {
		return tx.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(accountID)).Only(ctx)
	}
	return row, err
}

func lockAccount(ctx context.Context, tx *dbent.Tx, accountID int64) (*dbent.Account, error) {
	row, err := tx.Account.Query().Where(dbaccount.IDEQ(accountID)).ForUpdate().Only(ctx)
	if isSQLiteRowLockUnsupported(err) {
		return tx.Account.Query().Where(dbaccount.IDEQ(accountID)).Only(ctx)
	}
	return row, err
}

func lockProxy(ctx context.Context, tx *dbent.Tx, proxyID int64) (*dbent.Proxy, error) {
	row, err := tx.Proxy.Query().Where(dbproxy.IDEQ(proxyID)).ForUpdate().Only(ctx)
	if isSQLiteRowLockUnsupported(err) {
		return tx.Proxy.Query().Where(dbproxy.IDEQ(proxyID)).Only(ctx)
	}
	return row, err
}

func isSQLiteRowLockUnsupported(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOR UPDATE/SHARE not supported in SQLite")
}

func createManagedProxy(ctx context.Context, tx *dbent.Tx, candidate service.ManagedProxyCandidate) (*dbent.Proxy, error) {
	proxy := candidate.Derived.Proxy
	return tx.Proxy.Create().SetName(proxy.Name).SetProtocol(proxy.Protocol).SetHost(proxy.Host).SetPort(proxy.Port).
		SetUsername(proxy.Username).SetPassword(proxy.Password).SetStatus(service.StatusActive).
		SetExpiresAt(candidate.Derived.Timing.HardExpiresAt).SetFallbackMode(service.FallbackModeNone).Save(ctx)
}

func createManagedLease(ctx context.Context, tx *dbent.Tx, accountID, proxyID int64, candidate service.ManagedProxyCandidate) (*dbent.ManagedProxyLease, error) {
	return tx.ManagedProxyLease.Create().SetAccountID(accountID).SetProxyID(proxyID).SetProviderConfigID(candidate.ProviderConfigID).
		SetSessionID(candidate.Derived.SessionID).SetNillableTargetCountry(candidate.Derived.Target.Country).
		SetNillableTargetState(candidate.Derived.Target.State).SetNillableTargetCity(candidate.Derived.Target.City).
		SetStrict(catProxiesTargetStrict(candidate.Derived.Target)).SetLifetimeMinutes(candidateLifetime(candidate)).
		SetState(managedproxylease.StateActive).SetHealthStatus(managedproxylease.HealthStatusHealthy).
		SetHealthCheckedAt(candidate.Probe.CheckedAt).SetNillableObservedExitIP(candidate.Probe.ExitIP).
		SetNillableObservedCountry(candidate.Probe.Country).SetNillableObservedState(candidate.Probe.State).
		SetNillableObservedCity(candidate.Probe.City).SetObservedLatencyMs(candidate.Probe.LatencyMs).
		SetActivatedAt(candidate.Probe.CheckedAt).SetNextRotationAt(candidate.Derived.Timing.RotateAt).
		SetExpiresAt(candidate.Derived.Timing.HardExpiresAt).Save(ctx)
}

func candidateLifetime(candidate service.ManagedProxyCandidate) int {
	return candidate.LifetimeMinutes
}

func catProxiesTargetStrict(target service.CatProxiesProxyTarget) bool {
	return target.Strict != nil && *target.Strict
}

func managedProxyRuntimeDTO(row *dbent.ManagedProxyLease) service.ManagedProxyAccountDTO {
	account := row.Edges.Account
	provider := row.Edges.ProviderConfig
	result := service.ManagedProxyAccountDTO{Lease: *managedProxyLeaseEntityToService(row)}
	if account != nil {
		result.AccountName, result.Platform, result.Schedulable, result.ManagedProxyReady = account.Name, account.Platform, account.Schedulable, account.ManagedProxyReady
	}
	if provider != nil {
		result.ProviderName = provider.Name
	}
	return result
}

func enqueueManagedProxyAccounts(ctx context.Context, exec sqlExecutor, accountIDs []int64) error {
	for _, accountID := range accountIDs {
		if err := enqueueSchedulerOutbox(ctx, exec, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			return fmt.Errorf("enqueue scheduler account change for %d: %w", accountID, err)
		}
	}
	return nil
}
