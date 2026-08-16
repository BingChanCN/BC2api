package repository

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/catproxyproviderconfig"
	"github.com/Wei-Shaw/sub2api/ent/managedproxylease"
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type catProxyProviderConfigRepository struct {
	client *dbent.Client
}

func NewCatProxyProviderConfigRepository(client *dbent.Client) service.CatProxyProviderConfigRepository {
	return &catProxyProviderConfigRepository{client: client}
}

func (r *catProxyProviderConfigRepository) Create(ctx context.Context, config *service.CatProxyProviderConfig) error {
	if config == nil {
		return service.ErrCatProxyProviderConfigNotFound
	}
	row, err := clientFromContext(ctx, r.client).CatProxyProviderConfig.Create().
		SetName(config.Name).
		SetProviderType(catproxyproviderconfig.ProviderType(config.ProviderType)).
		SetStatus(catproxyproviderconfig.Status(config.Status)).
		SetIsDefault(config.IsDefault).
		SetProtocol(catproxyproviderconfig.Protocol(config.Protocol)).
		SetHost(config.Host).
		SetBaseUsername(config.BaseUsername).
		SetPassword(config.Password).
		SetNillableDefaultCountry(config.DefaultCountry).
		SetNillableDefaultState(config.DefaultState).
		SetNillableDefaultCity(config.DefaultCity).
		SetLifetimeMinutes(config.LifetimeMinutes).
		SetStrict(config.Strict).
		SetNillableLastProbeAt(config.LastProbeAt).
		SetNillableLastProbeLatencyMs(config.LastProbeLatencyMs).
		SetNillableLastError(config.LastError).
		SetNillableLastErrorAt(config.LastErrorAt).
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrCatProxyProviderConfigExists)
	}
	*config = *catProxyProviderConfigEntityToService(row)
	return nil
}

func (r *catProxyProviderConfigRepository) GetByID(ctx context.Context, id int64) (*service.CatProxyProviderConfig, error) {
	row, err := clientFromContext(ctx, r.client).CatProxyProviderConfig.Get(ctx, id)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrCatProxyProviderConfigNotFound, nil)
	}
	return catProxyProviderConfigEntityToService(row), nil
}

func (r *catProxyProviderConfigRepository) List(ctx context.Context) ([]service.CatProxyProviderConfig, error) {
	rows, err := clientFromContext(ctx, r.client).CatProxyProviderConfig.Query().
		Order(dbent.Desc(catproxyproviderconfig.FieldIsDefault), dbent.Asc(catproxyproviderconfig.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	configs := make([]service.CatProxyProviderConfig, 0, len(rows))
	for _, row := range rows {
		configs = append(configs, *catProxyProviderConfigEntityToService(row))
	}
	return configs, nil
}

func (r *catProxyProviderConfigRepository) Update(ctx context.Context, config *service.CatProxyProviderConfig) error {
	if config == nil {
		return service.ErrCatProxyProviderConfigNotFound
	}
	if dbent.TxFromContext(ctx) != nil {
		return r.updateInTransaction(ctx, config)
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.updateInTransaction(dbent.NewTxContext(ctx, tx), config); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *catProxyProviderConfigRepository) updateInTransaction(ctx context.Context, config *service.CatProxyProviderConfig) error {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("catproxies provider update requires a transaction")
	}
	if err := lockProviderRevision(ctx, tx, config.ID, config.UpdatedAt); err != nil {
		return err
	}
	client := tx.Client()
	update := client.CatProxyProviderConfig.UpdateOneID(config.ID).
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
	if _, err := client.ManagedProxyLease.Update().
		Where(managedproxylease.ProviderConfigIDEQ(config.ID)).
		SetLifetimeMinutes(config.LifetimeMinutes).
		Save(ctx); err != nil {
		return err
	}
	*config = *catProxyProviderConfigEntityToService(row)
	return nil
}

func (r *catProxyProviderConfigRepository) DefaultExists(ctx context.Context, excludeID int64) (bool, error) {
	query := clientFromContext(ctx, r.client).CatProxyProviderConfig.Query().
		Where(catproxyproviderconfig.IsDefaultEQ(true))
	if excludeID > 0 {
		query.Where(catproxyproviderconfig.IDNEQ(excludeID))
	}
	return query.Exist(ctx)
}

func (r *catProxyProviderConfigRepository) Delete(ctx context.Context, id int64) error {
	err := clientFromContext(ctx, r.client).CatProxyProviderConfig.DeleteOneID(id).Exec(ctx)
	return translatePersistenceError(err, service.ErrCatProxyProviderConfigNotFound, nil)
}

type managedProxyLeaseRepository struct {
	client *dbent.Client
}

func NewManagedProxyLeaseRepository(client *dbent.Client) service.ManagedProxyLeaseRepository {
	return &managedProxyLeaseRepository{client: client}
}

func (r *managedProxyLeaseRepository) Create(ctx context.Context, lease *service.ManagedProxyLease) error {
	if lease == nil {
		return service.ErrManagedProxyLeaseNotFound
	}
	row, err := clientFromContext(ctx, r.client).ManagedProxyLease.Create().
		SetAccountID(lease.AccountID).
		SetProxyID(lease.ProxyID).
		SetProviderConfigID(lease.ProviderConfigID).
		SetSessionID(lease.SessionID).
		SetNillableTargetCountry(lease.TargetCountry).
		SetNillableTargetState(lease.TargetState).
		SetNillableTargetCity(lease.TargetCity).
		SetStrict(lease.Strict).
		SetLifetimeMinutes(lease.LifetimeMinutes).
		SetState(managedproxylease.State(lease.State)).
		SetHealthStatus(managedproxylease.HealthStatus(lease.HealthStatus)).
		SetNillableHealthCheckedAt(lease.HealthCheckedAt).
		SetNillableObservedExitIP(lease.ObservedExitIP).
		SetNillableObservedCountry(lease.ObservedCountry).
		SetNillableObservedState(lease.ObservedState).
		SetNillableObservedCity(lease.ObservedCity).
		SetNillableObservedLatencyMs(lease.ObservedLatencyMs).
		SetNillableActivatedAt(lease.ActivatedAt).
		SetNillableLastRotatedAt(lease.LastRotatedAt).
		SetNillableNextRotationAt(lease.NextRotationAt).
		SetNillableExpiresAt(lease.ExpiresAt).
		SetFailureCount(lease.FailureCount).
		SetConsecutiveFailureCount(lease.ConsecutiveFailureCount).
		SetNillableLastError(lease.LastError).
		SetNillableLastErrorAt(lease.LastErrorAt).
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrManagedProxyLeaseExists)
	}
	*lease = *managedProxyLeaseEntityToService(row)
	return nil
}

func (r *managedProxyLeaseRepository) GetByAccountID(ctx context.Context, accountID int64) (*service.ManagedProxyLease, error) {
	row, err := clientFromContext(ctx, r.client).ManagedProxyLease.Query().
		Where(managedproxylease.AccountIDEQ(accountID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, nil)
	}
	return managedProxyLeaseEntityToService(row), nil
}

func (r *managedProxyLeaseRepository) LockByAccountID(ctx context.Context, accountID int64) (*service.ManagedProxyLease, error) {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return nil, fmt.Errorf("managed proxy lease lock requires a transaction")
	}
	row, err := lockManagedLease(ctx, tx, accountID)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, nil)
	}
	return managedProxyLeaseEntityToService(row), nil
}

func (r *managedProxyLeaseRepository) GetByProxyID(ctx context.Context, proxyID int64) (*service.ManagedProxyLease, error) {
	row, err := clientFromContext(ctx, r.client).ManagedProxyLease.Query().
		Where(managedproxylease.ProxyIDEQ(proxyID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, nil)
	}
	return managedProxyLeaseEntityToService(row), nil
}

func (r *managedProxyLeaseRepository) ListByProviderConfigID(ctx context.Context, providerConfigID int64) ([]service.ManagedProxyLease, error) {
	rows, err := clientFromContext(ctx, r.client).ManagedProxyLease.Query().
		Where(managedproxylease.ProviderConfigIDEQ(providerConfigID)).
		Order(dbent.Asc(managedproxylease.FieldAccountID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	leases := make([]service.ManagedProxyLease, 0, len(rows))
	for _, row := range rows {
		leases = append(leases, *managedProxyLeaseEntityToService(row))
	}
	return leases, nil
}

func (r *managedProxyLeaseRepository) ListDue(ctx context.Context, dueAt time.Time, limit int) ([]service.ManagedProxyLease, error) {
	query := clientFromContext(ctx, r.client).ManagedProxyLease.Query().
		Where(
			managedproxylease.NextRotationAtNotNil(),
			managedproxylease.NextRotationAtLTE(dueAt),
			managedproxylease.StateIn(managedproxylease.StateActive, managedproxylease.StateFailed),
		).
		Order(dbent.Asc(managedproxylease.FieldNextRotationAt), dbent.Asc(managedproxylease.FieldID))
	if limit > 0 {
		query.Limit(limit)
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	leases := make([]service.ManagedProxyLease, 0, len(rows))
	for _, row := range rows {
		leases = append(leases, *managedProxyLeaseEntityToService(row))
	}
	return leases, nil
}

func (r *managedProxyLeaseRepository) CountByProviderConfigID(ctx context.Context, providerConfigID int64) (int64, error) {
	count, err := clientFromContext(ctx, r.client).ManagedProxyLease.Query().
		Where(managedproxylease.ProviderConfigIDEQ(providerConfigID)).
		Count(ctx)
	return int64(count), err
}

func (r *managedProxyLeaseRepository) Update(ctx context.Context, lease *service.ManagedProxyLease) error {
	if lease == nil {
		return service.ErrManagedProxyLeaseNotFound
	}
	update := clientFromContext(ctx, r.client).ManagedProxyLease.UpdateOneID(lease.ID).
		SetAccountID(lease.AccountID).
		SetProxyID(lease.ProxyID).
		SetProviderConfigID(lease.ProviderConfigID).
		SetSessionID(lease.SessionID).
		SetStrict(lease.Strict).
		SetLifetimeMinutes(lease.LifetimeMinutes).
		SetState(managedproxylease.State(lease.State)).
		SetHealthStatus(managedproxylease.HealthStatus(lease.HealthStatus)).
		SetFailureCount(lease.FailureCount).
		SetConsecutiveFailureCount(lease.ConsecutiveFailureCount)
	setOptionalString := func(value *string, set func(string), clear func()) {
		if value != nil {
			set(*value)
		} else {
			clear()
		}
	}
	setOptionalTime := func(value *time.Time, set func(time.Time), clear func()) {
		if value != nil {
			set(*value)
		} else {
			clear()
		}
	}
	setOptionalString(lease.TargetCountry, func(v string) { update.SetTargetCountry(v) }, func() { update.ClearTargetCountry() })
	setOptionalString(lease.TargetState, func(v string) { update.SetTargetState(v) }, func() { update.ClearTargetState() })
	setOptionalString(lease.TargetCity, func(v string) { update.SetTargetCity(v) }, func() { update.ClearTargetCity() })
	setOptionalTime(lease.HealthCheckedAt, func(v time.Time) { update.SetHealthCheckedAt(v) }, func() { update.ClearHealthCheckedAt() })
	setOptionalString(lease.ObservedExitIP, func(v string) { update.SetObservedExitIP(v) }, func() { update.ClearObservedExitIP() })
	setOptionalString(lease.ObservedCountry, func(v string) { update.SetObservedCountry(v) }, func() { update.ClearObservedCountry() })
	setOptionalString(lease.ObservedState, func(v string) { update.SetObservedState(v) }, func() { update.ClearObservedState() })
	setOptionalString(lease.ObservedCity, func(v string) { update.SetObservedCity(v) }, func() { update.ClearObservedCity() })
	if lease.ObservedLatencyMs != nil {
		update.SetObservedLatencyMs(*lease.ObservedLatencyMs)
	} else {
		update.ClearObservedLatencyMs()
	}
	setOptionalTime(lease.ActivatedAt, func(v time.Time) { update.SetActivatedAt(v) }, func() { update.ClearActivatedAt() })
	setOptionalTime(lease.LastRotatedAt, func(v time.Time) { update.SetLastRotatedAt(v) }, func() { update.ClearLastRotatedAt() })
	setOptionalTime(lease.NextRotationAt, func(v time.Time) { update.SetNextRotationAt(v) }, func() { update.ClearNextRotationAt() })
	setOptionalTime(lease.ExpiresAt, func(v time.Time) { update.SetExpiresAt(v) }, func() { update.ClearExpiresAt() })
	setOptionalString(lease.LastError, func(v string) { update.SetLastError(v) }, func() { update.ClearLastError() })
	setOptionalTime(lease.LastErrorAt, func(v time.Time) { update.SetLastErrorAt(v) }, func() { update.ClearLastErrorAt() })

	row, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrManagedProxyLeaseNotFound, service.ErrManagedProxyLeaseExists)
	}
	*lease = *managedProxyLeaseEntityToService(row)
	return nil
}

func (r *managedProxyLeaseRepository) Delete(ctx context.Context, accountID int64) error {
	deleted, err := clientFromContext(ctx, r.client).ManagedProxyLease.Delete().
		Where(managedproxylease.AccountIDEQ(accountID)).
		Exec(ctx)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return service.ErrManagedProxyLeaseNotFound
	}
	return nil
}

func (r *managedProxyLeaseRepository) DeleteProxyPermanently(ctx context.Context, proxyID int64) error {
	err := clientFromContext(ctx, r.client).Proxy.DeleteOneID(proxyID).Exec(mixins.SkipSoftDelete(ctx))
	return translatePersistenceError(err, service.ErrProxyNotFound, nil)
}

func catProxyProviderConfigEntityToService(row *dbent.CatProxyProviderConfig) *service.CatProxyProviderConfig {
	if row == nil {
		return nil
	}
	return &service.CatProxyProviderConfig{
		ID:                 row.ID,
		Name:               row.Name,
		ProviderType:       string(row.ProviderType),
		Status:             string(row.Status),
		IsDefault:          row.IsDefault,
		Protocol:           string(row.Protocol),
		Host:               row.Host,
		BaseUsername:       row.BaseUsername,
		Password:           row.Password,
		DefaultCountry:     row.DefaultCountry,
		DefaultState:       row.DefaultState,
		DefaultCity:        row.DefaultCity,
		LifetimeMinutes:    row.LifetimeMinutes,
		Strict:             row.Strict,
		LastProbeAt:        row.LastProbeAt,
		LastProbeLatencyMs: row.LastProbeLatencyMs,
		LastError:          row.LastError,
		LastErrorAt:        row.LastErrorAt,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

func managedProxyLeaseEntityToService(row *dbent.ManagedProxyLease) *service.ManagedProxyLease {
	if row == nil {
		return nil
	}
	return &service.ManagedProxyLease{
		ID:                      row.ID,
		AccountID:               row.AccountID,
		ProxyID:                 row.ProxyID,
		ProviderConfigID:        row.ProviderConfigID,
		SessionID:               row.SessionID,
		TargetCountry:           row.TargetCountry,
		TargetState:             row.TargetState,
		TargetCity:              row.TargetCity,
		Strict:                  row.Strict,
		LifetimeMinutes:         row.LifetimeMinutes,
		State:                   string(row.State),
		HealthStatus:            string(row.HealthStatus),
		HealthCheckedAt:         row.HealthCheckedAt,
		ObservedExitIP:          row.ObservedExitIP,
		ObservedCountry:         row.ObservedCountry,
		ObservedState:           row.ObservedState,
		ObservedCity:            row.ObservedCity,
		ObservedLatencyMs:       row.ObservedLatencyMs,
		ActivatedAt:             row.ActivatedAt,
		LastRotatedAt:           row.LastRotatedAt,
		NextRotationAt:          row.NextRotationAt,
		ExpiresAt:               row.ExpiresAt,
		FailureCount:            row.FailureCount,
		ConsecutiveFailureCount: row.ConsecutiveFailureCount,
		LastError:               row.LastError,
		LastErrorAt:             row.LastErrorAt,
		CreatedAt:               row.CreatedAt,
		UpdatedAt:               row.UpdatedAt,
	}
}
