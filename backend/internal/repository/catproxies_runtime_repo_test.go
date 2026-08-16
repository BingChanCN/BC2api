package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/ent/catproxyproviderconfig"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/managedproxylease"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func newManagedProxyRuntimeRepositoryTest(t *testing.T) (*managedProxyRuntimeRepository, *dbent.Client) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	driver := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(driver)))
	t.Cleanup(func() { _ = client.Close() })
	_, err = db.Exec(`CREATE TABLE scheduler_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		account_id INTEGER,
		group_id INTEGER,
		payload BLOB,
		dedup_key TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE UNIQUE INDEX scheduler_outbox_dedup_key ON scheduler_outbox (dedup_key) WHERE dedup_key IS NOT NULL`)
	require.NoError(t, err)
	return &managedProxyRuntimeRepository{client: client, db: db}, client
}

type managedProxyRuntimeFixture struct {
	config     *service.CatProxyProviderConfig
	accountIDs []int64
	proxyIDs   []int64
	sessions   []string
}

func createManagedProxyRuntimeFixture(t *testing.T, client *dbent.Client) managedProxyRuntimeFixture {
	t.Helper()
	ctx := context.Background()
	provider, err := client.CatProxyProviderConfig.Create().
		SetName("provider").
		SetProviderType("catproxies").
		SetStatus("active").
		SetIsDefault(true).
		SetProtocol("http").
		SetHost("old.example.com").
		SetBaseUsername("customer-type-residential").
		SetPassword("old-secret").
		SetLifetimeMinutes(60).
		SetStrict(false).
		Save(ctx)
	require.NoError(t, err)

	fixture := managedProxyRuntimeFixture{
		config: &service.CatProxyProviderConfig{
			ID: provider.ID, Name: provider.Name, ProviderType: string(provider.ProviderType), Status: string(provider.Status),
			IsDefault: provider.IsDefault, Protocol: string(provider.Protocol), Host: provider.Host,
			BaseUsername: provider.BaseUsername, Password: provider.Password, LifetimeMinutes: provider.LifetimeMinutes,
			Strict: provider.Strict, CreatedAt: provider.CreatedAt, UpdatedAt: provider.UpdatedAt,
		},
	}
	for i := 0; i < 2; i++ {
		proxy, err := client.Proxy.Create().
			SetName(fmt.Sprintf("old-proxy-%d", i)).SetProtocol("http").SetHost("old.example.com").SetPort(10000).
			SetUsername(fmt.Sprintf("old-session-%d", i)).SetPassword("old-secret").SetStatus(service.StatusActive).
			SetFallbackMode(service.FallbackModeNone).Save(ctx)
		require.NoError(t, err)
		account, err := client.Account.Create().SetName(fmt.Sprintf("account-%d", i)).SetPlatform(service.PlatformOpenAI).
			SetType(service.AccountTypeOAuth).SetProxyID(proxy.ID).Save(ctx)
		require.NoError(t, err)
		session := fmt.Sprintf("session-%d", i)
		_, err = client.ManagedProxyLease.Create().SetAccountID(account.ID).SetProxyID(proxy.ID).
			SetProviderConfigID(provider.ID).SetSessionID(session).SetStrict(false).SetLifetimeMinutes(60).
			SetState(managedproxylease.StateActive).SetHealthStatus(managedproxylease.HealthStatusHealthy).Save(ctx)
		require.NoError(t, err)
		fixture.accountIDs = append(fixture.accountIDs, account.ID)
		fixture.proxyIDs = append(fixture.proxyIDs, proxy.ID)
		fixture.sessions = append(fixture.sessions, session)
	}
	return fixture
}

func managedProxyRepositoryCandidate(accountID, providerID int64, session, host string, providerUpdatedAt ...time.Time) service.ManagedProxyProviderCandidate {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	var revision time.Time
	if len(providerUpdatedAt) > 0 {
		revision = providerUpdatedAt[0]
	}
	return service.ManagedProxyProviderCandidate{
		AccountID: accountID,
		Candidate: service.ManagedProxyCandidate{
			ProviderConfigID:  providerID,
			ProviderUpdatedAt: revision,
			LifetimeMinutes:   60,
			Derived: service.CatProxiesDerivedProxy{
				Proxy:     &service.Proxy{Name: "new-" + session, Protocol: service.CatProxiesProtocolSOCKS5H, Host: host, Port: service.CatProxiesSOCKS5HStickyPort, Username: session, Password: "new-secret", Status: service.StatusActive, FallbackMode: service.FallbackModeNone},
				SessionID: session,
				Timing:    service.CatProxiesLeaseTiming{RotateAt: now.Add(55 * time.Minute), HardExpiresAt: now.Add(time.Hour)},
			},
			Probe: service.CatProxiesProbeResult{LatencyMs: 25, CheckedAt: now, StrictMatch: true},
		},
	}
}

func TestCatProxyProviderSchemaEnforcesSingleDefault(t *testing.T) {
	_, client := newManagedProxyRuntimeRepositoryTest(t)
	ctx := context.Background()
	create := func(name string) error {
		_, err := client.CatProxyProviderConfig.Create().
			SetName(name).SetProviderType("catproxies").SetStatus("active").SetIsDefault(true).
			SetProtocol("http").SetHost(name + ".example.com").SetBaseUsername("customer").
			SetPassword("secret").SetLifetimeMinutes(60).SetStrict(false).Save(ctx)
		return err
	}
	require.NoError(t, create("first"))
	require.Error(t, create("second"))
}

func TestManagedProxyRuntimeRepositoryListDueHonorsProviderLifecycle(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	_, err := client.ManagedProxyLease.Update().SetNextRotationAt(now.Add(-time.Minute)).Save(ctx)
	require.NoError(t, err)

	due, err := repo.ListDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, due, 2)

	_, err = client.CatProxyProviderConfig.UpdateOneID(fixture.config.ID).SetStatus("disabled").Save(ctx)
	require.NoError(t, err)
	due, err = repo.ListDue(ctx, now, 10)
	require.NoError(t, err)
	require.Empty(t, due)

	_, err = client.CatProxyProviderConfig.UpdateOneID(fixture.config.ID).SetStatus("retiring").Save(ctx)
	require.NoError(t, err)
	due, err = repo.ListDue(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, due, 2)
}

func TestManagedProxyRuntimeRepositoryRejectsCandidatePreparedBeforeProviderDisable(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	candidate := managedProxyRepositoryCandidate(
		fixture.accountIDs[0], fixture.config.ID, "stale-session", fixture.config.Host, fixture.config.UpdatedAt,
	).Candidate

	fixture.config.Status = service.CatProxiesStatusDisabled
	fixture.config.IsDefault = false
	require.NoError(t, repo.UpdateProviderStatus(ctx, fixture.config, false, true))

	_, err := repo.Swap(ctx, fixture.accountIDs[0], candidate)
	require.ErrorIs(t, err, service.ErrManagedProxyBindingsChanged)
	account, err := client.Account.Get(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.False(t, account.ManagedProxyReady)
	require.Equal(t, fixture.proxyIDs[0], *account.ProxyID)
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, fixture.sessions[0], lease.SessionID)
	require.Equal(t, fixture.proxyIDs[0], lease.ProxyID)
}

func TestManagedProxyRuntimeRepositoryManualFailureClosesGateAndSchedulesRetry(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	require.NoError(t, repo.RecordRotationFailure(ctx, fixture.accountIDs[0], now, false, "probe failed"))
	account, err := client.Account.Get(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.False(t, account.ManagedProxyReady)
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, managedproxylease.StateFailed, lease.State)
	require.NotNil(t, lease.NextRotationAt)
	require.Equal(t, now.Add(time.Minute), *lease.NextRotationAt)
}

func TestManagedAccountProvisionCreatesAccountProxyAndLeaseInOneTransaction(t *testing.T) {
	runtime, client := newManagedProxyRuntimeRepositoryTest(t)
	ctx := context.Background()
	provider, err := client.CatProxyProviderConfig.Create().
		SetName("provision-provider").SetProviderType("catproxies").SetStatus("active").SetProtocol("http").
		SetHost("gateway.example.com").SetBaseUsername("customer-type-residential").SetPassword("secret").
		SetLifetimeMinutes(60).SetStrict(false).Save(ctx)
	require.NoError(t, err)

	accountRepo := newAccountRepositoryWithSQL(client, runtime.db, nil)
	provision := &managedAccountProvisionRepository{client: client, accounts: accountRepo, runtime: runtime}
	account := &service.Account{
		Name: "provisioned", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "test"}, Status: service.StatusActive, Schedulable: true,
	}
	candidate := managedProxyRepositoryCandidate(0, provider.ID, "provision-session", "gateway.example.com", provider.UpdatedAt).Candidate
	require.NoError(t, provision.Create(ctx, account, nil, candidate))
	require.Positive(t, account.ID)

	stored, err := client.Account.Get(ctx, account.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.ProxyID)
	require.True(t, stored.ManagedProxyReady)
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(account.ID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, *stored.ProxyID, lease.ProxyID)
	require.Equal(t, "provision-session", lease.SessionID)
}

func TestManagedAccountProvisionRollsBackAccountWhenActivationFails(t *testing.T) {
	runtime, client := newManagedProxyRuntimeRepositoryTest(t)
	ctx := context.Background()
	accountRepo := newAccountRepositoryWithSQL(client, runtime.db, nil)
	provision := &managedAccountProvisionRepository{client: client, accounts: accountRepo, runtime: runtime}
	account := &service.Account{
		Name: "rolled-back", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "test"}, Status: service.StatusActive, Schedulable: true,
	}
	candidate := managedProxyRepositoryCandidate(0, 999999, "failed-session", "gateway.example.com").Candidate
	require.Error(t, provision.Create(ctx, account, nil, candidate))

	accountCount, err := client.Account.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, accountCount)
	proxyCount, err := client.Proxy.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, proxyCount)
	leaseCount, err := client.ManagedProxyLease.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, leaseCount)
}

func TestProxyManagementListsExcludeManagedDerivedProxies(t *testing.T) {
	_, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	staticProxy, err := client.Proxy.Create().SetName("static").SetProtocol("http").SetHost("static.example.com").SetPort(8080).SetStatus(service.StatusActive).SetFallbackMode(service.FallbackModeNone).Save(ctx)
	require.NoError(t, err)
	repo := newProxyRepositoryWithSQL(client, nil)

	listed, page, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 20}, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Len(t, listed, 1)
	require.Equal(t, staticProxy.ID, listed[0].ID)
	active, err := repo.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, staticProxy.ID, active[0].ID)
	byIDs, err := repo.ListByIDs(ctx, append([]int64{staticProxy.ID}, fixture.proxyIDs...))
	require.NoError(t, err)
	require.Len(t, byIDs, 1)
	require.Equal(t, staticProxy.ID, byIDs[0].ID)
	for _, managedProxyID := range fixture.proxyIDs {
		require.NotEqual(t, managedProxyID, listed[0].ID)
	}
}

func TestManagedProxyRuntimeRepositoryReleaseRemovesCurrentLeaseAndProxy(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()

	require.NoError(t, repo.Release(ctx, fixture.accountIDs[0]))
	account, err := client.Account.Get(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.Nil(t, account.ProxyID)
	require.True(t, account.ManagedProxyReady)
	require.False(t, client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).ExistX(ctx))
	var proxyRows int
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxies WHERE id = ?`, fixture.proxyIDs[0]).Scan(&proxyRows))
	require.Zero(t, proxyRows)
}

func TestManagedProxyRuntimeRepositoryPasswordAndRoutingUpdatesAreAtomic(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()

	fixture.config.Password = "new-secret"
	require.NoError(t, repo.UpdateProviderPassword(ctx, fixture.config))
	for i, proxyID := range fixture.proxyIDs {
		proxy, err := client.Proxy.Get(ctx, proxyID)
		require.NoError(t, err)
		require.NotNil(t, proxy.Password)
		require.Equal(t, "new-secret", *proxy.Password)
		lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[i])).Only(ctx)
		require.NoError(t, err)
		require.Equal(t, fixture.sessions[i], lease.SessionID)
		require.Equal(t, proxyID, lease.ProxyID)
	}

	fixture.config.Protocol = service.CatProxiesProtocolSOCKS5H
	fixture.config.Host = "new.example.com"
	fixture.config.BaseUsername = "new-customer-type-residential"
	fixture.config.Status = service.CatProxiesStatusDisabled
	fixture.config.IsDefault = false
	candidates := []service.ManagedProxyProviderCandidate{
		managedProxyRepositoryCandidate(fixture.accountIDs[0], fixture.config.ID, "fresh-0", fixture.config.Host, fixture.config.UpdatedAt),
		managedProxyRepositoryCandidate(fixture.accountIDs[1], fixture.config.ID, "fresh-1", fixture.config.Host, fixture.config.UpdatedAt),
	}
	require.NoError(t, repo.MigrateProvider(ctx, fixture.config, candidates))
	for i, accountID := range fixture.accountIDs {
		lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(accountID)).Only(ctx)
		require.NoError(t, err)
		require.Equal(t, candidates[i].Candidate.Derived.SessionID, lease.SessionID)
		require.NotEqual(t, fixture.proxyIDs[i], lease.ProxyID)
		account, err := client.Account.Get(ctx, accountID)
		require.NoError(t, err)
		require.Equal(t, lease.ProxyID, *account.ProxyID)
		require.False(t, account.ManagedProxyReady)
		proxy, err := client.Proxy.Get(ctx, lease.ProxyID)
		require.NoError(t, err)
		require.Equal(t, service.CatProxiesProtocolSOCKS5H, proxy.Protocol)
		require.Equal(t, service.FallbackModeNone, proxy.FallbackMode)
	}
	provider, err := client.CatProxyProviderConfig.Get(ctx, fixture.config.ID)
	require.NoError(t, err)
	require.Equal(t, "new.example.com", provider.Host)
	var oldProxyRows int
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM proxies WHERE id IN (?, ?)`, fixture.proxyIDs[0], fixture.proxyIDs[1]).Scan(&oldProxyRows))
	require.Zero(t, oldProxyRows, "replaced managed proxies must be physically removed")
}

func TestManagedProxyRuntimeRepositoryReactivationOnlyOpensSuccessfullyReplacedAccounts(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	observedAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	fixture.config.Status = service.CatProxiesStatusActive
	fixture.config.IsDefault = false
	message := "one managed proxy account failed reactivation"
	fixture.config.LastError = &message
	fixture.config.LastErrorAt = &observedAt
	candidate := managedProxyRepositoryCandidate(fixture.accountIDs[0], fixture.config.ID, "fresh-0", fixture.config.Host, fixture.config.UpdatedAt)

	require.NoError(t, repo.ReactivateProvider(ctx, fixture.config, []service.ManagedProxyProviderCandidate{candidate}, map[int64]string{fixture.accountIDs[1]: "no residential exit"}, observedAt))

	readyAccount, err := client.Account.Get(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.True(t, readyAccount.ManagedProxyReady)
	readyLease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "fresh-0", readyLease.SessionID)
	require.NotEqual(t, fixture.proxyIDs[0], readyLease.ProxyID)

	failedAccount, err := client.Account.Get(ctx, fixture.accountIDs[1])
	require.NoError(t, err)
	require.False(t, failedAccount.ManagedProxyReady)
	failedLease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[1])).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, fixture.sessions[1], failedLease.SessionID)
	require.Equal(t, fixture.proxyIDs[1], failedLease.ProxyID)
	require.Equal(t, managedproxylease.StateFailed, failedLease.State)
	require.Equal(t, observedAt.Add(time.Minute), *failedLease.NextRotationAt)
	require.Equal(t, "no residential exit", *failedLease.LastError)

	provider, err := client.CatProxyProviderConfig.Get(ctx, fixture.config.ID)
	require.NoError(t, err)
	require.Equal(t, service.CatProxiesStatusActive, string(provider.Status))
	require.Equal(t, message, *provider.LastError)
}

func TestManagedProxyRuntimeRepositoryRateLimitBackoffKeepsSession(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	until := now.Add(5 * time.Second)

	require.NoError(t, repo.RecordRateLimitBackoff(ctx, fixture.accountIDs[0], now, until))
	account, err := client.Account.Get(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.NotNil(t, account.RateLimitedAt)
	require.NotNil(t, account.RateLimitResetAt)
	require.Equal(t, now, *account.RateLimitedAt)
	require.Equal(t, until, *account.RateLimitResetAt)
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, fixture.sessions[0], lease.SessionID)
}

func TestManagedProxyRuntimeRepositoryIgnoresCredentialErrorFromStaleProxyGeneration(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()

	original, err := client.Proxy.Get(ctx, fixture.proxyIDs[0])
	require.NoError(t, err)
	updated, err := client.Proxy.UpdateOneID(original.ID).
		SetPassword("new-secret").
		SetUpdatedAt(original.UpdatedAt.Add(time.Second)).
		Save(ctx)
	require.NoError(t, err)

	marked, err := repo.MarkProviderCredentialErrorByAccount(ctx, fixture.accountIDs[0], original.ID, original.UpdatedAt, "stale 407")
	require.NoError(t, err)
	require.False(t, marked)
	provider, err := client.CatProxyProviderConfig.Get(ctx, fixture.config.ID)
	require.NoError(t, err)
	require.Equal(t, catproxyproviderconfig.StatusActive, provider.Status)

	marked, err = repo.MarkProviderCredentialErrorByAccount(ctx, fixture.accountIDs[0], updated.ID, updated.UpdatedAt, "current 407")
	require.NoError(t, err)
	require.True(t, marked)
	provider, err = client.CatProxyProviderConfig.Get(ctx, fixture.config.ID)
	require.NoError(t, err)
	require.Equal(t, catproxyproviderconfig.StatusCredentialError, provider.Status)
	accounts, err := client.Account.Query().Where(dbaccount.IDIn(fixture.accountIDs...)).All(ctx)
	require.NoError(t, err)
	for _, account := range accounts {
		require.False(t, account.ManagedProxyReady)
	}
}

func TestManagedProxyRuntimeRepositoryIgnoresFaultFromReplacedProxy(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	recorded, trigger, err := repo.RecordTransportFailure(ctx, fixture.accountIDs[0], fixture.proxyIDs[1], now, "stale reset")
	require.NoError(t, err)
	require.False(t, recorded)
	require.False(t, trigger)
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.Zero(t, lease.ConsecutiveFailureCount)
	require.Nil(t, lease.LastErrorAt)
}

func TestAccountRepositoryMarksManagedProxyForDTOBoundary(t *testing.T) {
	runtimeRepo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	accountRepo := newAccountRepositoryWithSQL(client, runtimeRepo.db, nil)

	loaded, err := accountRepo.GetByID(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.NotNil(t, loaded.Proxy)
	require.True(t, loaded.Proxy.Managed)

	loadedMany, err := accountRepo.GetByIDs(ctx, []int64{fixture.accountIDs[0]})
	require.NoError(t, err)
	require.Len(t, loadedMany, 1)
	require.NotNil(t, loadedMany[0].Proxy)
	require.True(t, loadedMany[0].Proxy.Managed)
}

func TestManagedProxyRuntimeRepositorySchedulesBindingsChangedRetryBeforeHardExpiry(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	expiresAt := now.Add(30 * time.Second)
	_, err := client.ManagedProxyLease.Update().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).
		SetNextRotationAt(now.Add(-time.Minute)).SetExpiresAt(expiresAt).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, repo.RecordBindingsChangedRetry(ctx, fixture.accountIDs[0], fixture.proxyIDs[0], now.Add(time.Minute)))
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, lease.NextRotationAt)
	require.Equal(t, expiresAt, *lease.NextRotationAt)
}

func TestManagedProxyRuntimeRepositoryTransportTriggerKeepsLastRotatedAt(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	lastRotated := now.Add(-10 * time.Minute)
	_, err := client.ManagedProxyLease.Update().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).
		SetLastRotatedAt(lastRotated).SetLastErrorAt(now.Add(-time.Minute)).SetConsecutiveFailureCount(1).Save(ctx)
	require.NoError(t, err)

	recorded, trigger, err := repo.RecordTransportFailure(ctx, fixture.accountIDs[0], fixture.proxyIDs[0], now, "connection reset")
	require.NoError(t, err)
	require.True(t, recorded)
	require.True(t, trigger)
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, lease.LastRotatedAt)
	require.Equal(t, lastRotated, *lease.LastRotatedAt)
}

func TestManagedProxyRuntimeRepositoryImmediateTransportFailureHonorsRotationCooldown(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	_, err := client.ManagedProxyLease.Update().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).
		SetLastRotatedAt(now.Add(-10 * time.Minute)).Save(ctx)
	require.NoError(t, err)
	recorded, trigger, err := repo.RecordImmediateTransportFailure(ctx, fixture.accountIDs[0], fixture.proxyIDs[0], now, "proxyconnect tcp: 503")
	require.NoError(t, err)
	require.True(t, recorded)
	require.True(t, trigger)

	_, err = client.ManagedProxyLease.Update().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).
		SetLastRotatedAt(now.Add(-time.Minute)).Save(ctx)
	require.NoError(t, err)
	recorded, trigger, err = repo.RecordImmediateTransportFailure(ctx, fixture.accountIDs[0], fixture.proxyIDs[0], now, "proxyconnect tcp: 503")
	require.NoError(t, err)
	require.True(t, recorded)
	require.False(t, trigger)
}

func TestManagedProxyRuntimeRepositoryScheduledFailureKeepsOldLeaseReadyBeforeHardExpiry(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	lastRotated := now.Add(-10 * time.Minute)
	_, err := client.ManagedProxyLease.Update().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).
		SetLastRotatedAt(lastRotated).SetExpiresAt(now.Add(time.Hour)).Save(ctx)
	require.NoError(t, err)
	_, err = client.Account.UpdateOneID(fixture.accountIDs[0]).SetManagedProxyReady(true).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, repo.RecordScheduledRotationFailure(ctx, fixture.accountIDs[0], now, false, "probe failed"))
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, lease.LastRotatedAt)
	require.NotNil(t, lease.NextRotationAt)
	require.Equal(t, lastRotated, *lease.LastRotatedAt)
	require.Equal(t, now.Add(time.Minute), *lease.NextRotationAt)
	require.Equal(t, managedproxylease.StateFailed, lease.State)
	account, err := client.Account.Get(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.True(t, account.ManagedProxyReady)
}

func TestManagedProxyRuntimeRepositoryScheduledFailureAtHardExpiryClosesGate(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	_, err := client.Account.UpdateOneID(fixture.accountIDs[0]).SetManagedProxyReady(true).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, repo.RecordScheduledRotationFailure(ctx, fixture.accountIDs[0], now, true, "probe failed"))
	lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(fixture.accountIDs[0])).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, managedproxylease.StateExpired, lease.State)
	account, err := client.Account.Get(ctx, fixture.accountIDs[0])
	require.NoError(t, err)
	require.False(t, account.ManagedProxyReady)
	var outboxCount int
	require.NoError(t, repo.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = ?", fixture.accountIDs[0]).Scan(&outboxCount))
	require.Equal(t, 1, outboxCount)
}

func TestManagedProxyRuntimeRepositoryOutboxFailureRollsBackGateChange(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	_, err := client.Account.Update().SetManagedProxyReady(true).Save(ctx)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, "DROP TABLE scheduler_outbox")
	require.NoError(t, err)

	err = repo.SetProviderReady(ctx, fixture.config.ID, false)
	require.Error(t, err)
	for _, accountID := range fixture.accountIDs {
		account, getErr := client.Account.Get(ctx, accountID)
		require.NoError(t, getErr)
		require.True(t, account.ManagedProxyReady)
	}
}

func TestManagedProxyRuntimeRepositoryOutboxFailureRollsBackProviderStatusAndGates(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	_, err := client.Account.Update().SetManagedProxyReady(true).Save(ctx)
	require.NoError(t, err)
	_, err = repo.db.ExecContext(ctx, "DROP TABLE scheduler_outbox")
	require.NoError(t, err)
	fixture.config.Status = service.CatProxiesStatusDisabled
	fixture.config.IsDefault = false

	err = repo.UpdateProviderStatus(ctx, fixture.config, false, true)
	require.Error(t, err)
	provider, getErr := client.CatProxyProviderConfig.Get(ctx, fixture.config.ID)
	require.NoError(t, getErr)
	require.Equal(t, service.CatProxiesStatusActive, string(provider.Status))
	for _, accountID := range fixture.accountIDs {
		account, accountErr := client.Account.Get(ctx, accountID)
		require.NoError(t, accountErr)
		require.True(t, account.ManagedProxyReady)
	}
}

func TestManagedProxyRuntimeRepositoryRoutingFailureRollsBackEverything(t *testing.T) {
	repo, client := newManagedProxyRuntimeRepositoryTest(t)
	fixture := createManagedProxyRuntimeFixture(t, client)
	ctx := context.Background()
	beforeProxyCount, err := client.Proxy.Query().Count(ctx)
	require.NoError(t, err)

	fixture.config.Protocol = service.CatProxiesProtocolSOCKS5H
	fixture.config.Host = "new.example.com"
	candidates := []service.ManagedProxyProviderCandidate{
		managedProxyRepositoryCandidate(fixture.accountIDs[0], fixture.config.ID, "fresh-0", fixture.config.Host, fixture.config.UpdatedAt),
		managedProxyRepositoryCandidate(fixture.accountIDs[1], fixture.config.ID, "fresh-1", "", fixture.config.UpdatedAt),
	}
	err = repo.MigrateProvider(ctx, fixture.config, candidates)
	require.Error(t, err)

	provider, err := client.CatProxyProviderConfig.Get(ctx, fixture.config.ID)
	require.NoError(t, err)
	require.Equal(t, "old.example.com", provider.Host)
	proxyCount, err := client.Proxy.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeProxyCount, proxyCount)
	for i, accountID := range fixture.accountIDs {
		lease, err := client.ManagedProxyLease.Query().Where(managedproxylease.AccountIDEQ(accountID)).Only(ctx)
		require.NoError(t, err)
		require.Equal(t, fixture.sessions[i], lease.SessionID)
		require.Equal(t, fixture.proxyIDs[i], lease.ProxyID)
		account, err := client.Account.Get(ctx, accountID)
		require.NoError(t, err)
		require.Equal(t, fixture.proxyIDs[i], *account.ProxyID)
	}
}
