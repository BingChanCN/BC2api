package service

import (
	"context"
	"fmt"
)

// ManagedAccountProvisionRepository atomically persists an account, its group
// bindings, the derived proxy, and its managed lease.
type ManagedAccountProvisionRepository interface {
	Create(ctx context.Context, account *Account, groups []AccountGroup, candidate ManagedProxyCandidate) error
}

// ManagedAccountProvisionService owns the account-data import boundary for
// CatProxies. Network probing happens before Create starts its short database
// transaction.
type CatProxyProviderBackup struct {
	Name            string
	Protocol        string
	Host            string
	BaseUsername    string
	Password        string
	DefaultCountry  *string
	DefaultState    *string
	DefaultCity     *string
	LifetimeMinutes int
	Strict          bool
	Status          string
	IsDefault       bool
}

type ManagedAccountProvisionService struct {
	repo       ManagedAccountProvisionRepository
	groupRepo  GroupRepository
	runtime    *ManagedProxyRuntimeService
	catproxies *CatProxiesManagedProxyService
}

func NewManagedAccountProvisionService(
	repo ManagedAccountProvisionRepository,
	groupRepo GroupRepository,
	runtime *ManagedProxyRuntimeService,
	catproxies *CatProxiesManagedProxyService,
) *ManagedAccountProvisionService {
	return &ManagedAccountProvisionService{repo: repo, groupRepo: groupRepo, runtime: runtime, catproxies: catproxies}
}

func (s *ManagedAccountProvisionService) ValidateProviderForAssignment(ctx context.Context, providerConfigID int64) error {
	if s == nil || s.catproxies == nil {
		return ErrCatProxyProviderConfigNotFound
	}
	config, err := s.catproxies.getConfigForBackup(ctx, providerConfigID)
	if err != nil {
		return err
	}
	if err := config.Validate(); err != nil {
		return err
	}
	if config.Status != CatProxiesStatusActive {
		return fmt.Errorf("catproxies provider config is not active")
	}
	return nil
}

func (s *ManagedAccountProvisionService) Prepare(
	ctx context.Context,
	providerConfigID int64,
	target CatProxiesProxyTarget,
) (ManagedProxyCandidate, error) {
	if s == nil || s.runtime == nil {
		return ManagedProxyCandidate{}, fmt.Errorf("managed proxy provisioning is not configured")
	}
	return s.runtime.PrepareCandidate(ctx, providerConfigID, target)
}

func (s *ManagedAccountProvisionService) Create(
	ctx context.Context,
	input *CreateAccountInput,
	candidate ManagedProxyCandidate,
) (*Account, error) {
	if s == nil || s.repo == nil || input == nil {
		return nil, ErrAccountNilInput
	}
	if input.ProxyID != nil {
		return nil, fmt.Errorf("managed account import cannot also specify a static proxy")
	}

	accountExtra, err := normalizeOpenAILongContextBillingExtra(input.Platform, input.Extra)
	if err != nil {
		return nil, err
	}
	accountExtra, err = normalizeGrokMediaEligibilityExtra(input.Platform, accountExtra)
	if err != nil {
		return nil, err
	}
	if err := NormalizeHeaderOverrideCredentials(input.Credentials); err != nil {
		return nil, err
	}
	input.Credentials = SanitizeStoredCredentials(input.Platform, input.Credentials)
	account, err := buildAccountForCreate(input, accountExtra)
	if err != nil {
		return nil, err
	}
	managedProxyReady := true
	account.ManagedProxyReady = &managedProxyReady

	groups, err := s.defaultGroups(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, account, groups, candidate); err != nil {
		return nil, err
	}
	return account, nil
}

func (s *ManagedAccountProvisionService) defaultGroups(ctx context.Context, input *CreateAccountInput) ([]AccountGroup, error) {
	if input.SkipDefaultGroupBind || s.groupRepo == nil {
		return nil, nil
	}
	groups, err := s.groupRepo.ListActiveByPlatform(ctx, input.Platform)
	if err != nil {
		return nil, err
	}
	defaultName := input.Platform + "-default"
	for _, group := range groups {
		if group.Name == defaultName {
			return []AccountGroup{{GroupID: group.ID}}, nil
		}
	}
	return nil, nil
}

func (s *ManagedAccountProvisionService) GetManagedAccount(ctx context.Context, accountID int64) (*ManagedProxyAccountDTO, error) {
	if s == nil || s.runtime == nil {
		return nil, ErrManagedProxyLeaseNotFound
	}
	return s.runtime.Get(ctx, accountID)
}

func (s *ManagedAccountProvisionService) ListManagedAccounts(ctx context.Context) ([]ManagedProxyAccountDTO, error) {
	if s == nil || s.runtime == nil {
		return nil, fmt.Errorf("managed proxy provisioning is not configured")
	}
	return s.runtime.List(ctx)
}

// GetProviderForBackup is deliberately not exposed by the management API. It
// is used only by the existing credential-bearing account-data export.
func (s *ManagedAccountProvisionService) GetProviderForBackup(ctx context.Context, id int64) (*CatProxyProviderConfig, error) {
	if s == nil || s.catproxies == nil {
		return nil, ErrCatProxyProviderConfigNotFound
	}
	return s.catproxies.getConfigForBackup(ctx, id)
}

func (s *ManagedAccountProvisionService) RestoreProvider(ctx context.Context, backup CatProxyProviderBackup) (*CatProxyProviderConfigDTO, error) {
	if s == nil || s.catproxies == nil {
		return nil, ErrCatProxyProviderConfigNotFound
	}
	strict := backup.Strict
	created, err := s.catproxies.CreateConfig(ctx, CreateCatProxyProviderConfigInput{
		Name: backup.Name, IsDefault: backup.IsDefault, Protocol: backup.Protocol,
		Host: backup.Host, BaseUsername: backup.BaseUsername, Password: backup.Password,
		DefaultCountry: backup.DefaultCountry, DefaultState: backup.DefaultState,
		DefaultCity: backup.DefaultCity, LifetimeMinutes: backup.LifetimeMinutes, Strict: &strict,
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *ManagedAccountProvisionService) SetRestoredProviderStatus(ctx context.Context, id int64, status string) error {
	if status == "" || status == CatProxiesStatusActive {
		return nil
	}
	_, err := s.catproxies.UpdateConfigStatus(ctx, id, status)
	return err
}
