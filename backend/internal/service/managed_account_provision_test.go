package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type managedAccountProvisionRepoStub struct {
	account   *Account
	groups    []AccountGroup
	candidate ManagedProxyCandidate
	err       error
}

func (s *managedAccountProvisionRepoStub) Create(_ context.Context, account *Account, groups []AccountGroup, candidate ManagedProxyCandidate) error {
	s.account = account
	s.groups = append([]AccountGroup(nil), groups...)
	s.candidate = candidate
	return s.err
}

func TestManagedAccountProvisionServicePreparesThenAtomicallyCreates(t *testing.T) {
	configRepo := &catProxiesConfigRepoStub{config: &CatProxyProviderConfig{
		ID: 7, Name: "order-a", Status: CatProxiesStatusActive, Protocol: CatProxiesProtocolHTTP,
		Host: "proxy.example.com", BaseUsername: "customer-type-residential", Password: "secret", LifetimeMinutes: 60,
	}}
	managed := NewCatProxiesManagedProxyService(
		configRepo,
		&catProxiesLeaseRepoStub{},
		&catProxiesProbeStub{info: &ProxyExitInfo{IP: "203.0.113.8", CountryCode: "US"}, latency: 31},
		catProxiesTargetingForTest(t),
		nil,
	)
	runtime := NewManagedProxyRuntimeService(managed, nil)
	repo := &managedAccountProvisionRepoStub{}
	svc := NewManagedAccountProvisionService(repo, nil, runtime, managed)
	country := "us"
	strict := true

	candidate, err := svc.Prepare(context.Background(), 7, CatProxiesProxyTarget{Country: &country, Strict: &strict})
	require.NoError(t, err)
	created, err := svc.Create(context.Background(), &CreateAccountInput{
		Name: "imported", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "token"}, SkipDefaultGroupBind: true,
	}, candidate)

	require.NoError(t, err)
	require.Same(t, repo.account, created)
	require.True(t, created.IsManagedProxyReady())
	require.Empty(t, repo.groups)
	require.Equal(t, int64(7), repo.candidate.ProviderConfigID)
	require.Equal(t, "203.0.113.8", *repo.candidate.Probe.ExitIP)
	require.Equal(t, FallbackModeNone, repo.candidate.Derived.Proxy.FallbackMode)
	require.Contains(t, repo.candidate.Derived.Proxy.Username, "-lifetime-60-session-")
	require.Contains(t, repo.candidate.Derived.Proxy.Username, "-strict-on")
	require.Equal(t, 5*time.Minute, repo.candidate.Derived.Timing.HardExpiresAt.Sub(repo.candidate.Derived.Timing.RotateAt))
}

func TestManagedAccountProvisionServiceDoesNotMaskAtomicRepositoryFailure(t *testing.T) {
	writeErr := errors.New("transaction rolled back")
	repo := &managedAccountProvisionRepoStub{err: writeErr}
	svc := NewManagedAccountProvisionService(repo, nil, nil, nil)

	created, err := svc.Create(context.Background(), &CreateAccountInput{
		Name: "imported", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "token"}, SkipDefaultGroupBind: true,
	}, ManagedProxyCandidate{})

	require.Nil(t, created)
	require.ErrorIs(t, err, writeErr)
	require.NotNil(t, repo.account)
}
