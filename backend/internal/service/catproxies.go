package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrCatProxyProviderConfigNotFound    = infraerrors.NotFound("CATPROXIES_PROVIDER_NOT_FOUND", "catproxies provider config not found")
	ErrCatProxyProviderConfigExists      = infraerrors.Conflict("CATPROXIES_PROVIDER_EXISTS", "catproxies provider config already exists")
	ErrCatProxyProviderDefaultExists     = infraerrors.Conflict("CATPROXIES_DEFAULT_EXISTS", "a default catproxies provider already exists")
	ErrCatProxyProviderConfigInUse       = infraerrors.Conflict("CATPROXIES_PROVIDER_IN_USE", "catproxies provider config is bound to managed proxy leases")
	ErrManagedProxyLeaseNotFound         = infraerrors.NotFound("MANAGED_PROXY_LEASE_NOT_FOUND", "managed proxy lease not found")
	ErrManagedProxyLeaseExists           = infraerrors.Conflict("MANAGED_PROXY_LEASE_EXISTS", "managed proxy lease already exists")
	ErrManagedProxyBindingsChanged       = infraerrors.Conflict("MANAGED_PROXY_BINDINGS_CHANGED", "managed proxy bindings changed during provider update")
	ErrManagedProxyAccountProxyImmutable = infraerrors.Conflict("MANAGED_PROXY_ACCOUNT_PROXY_IMMUTABLE", "managed proxy account proxy cannot be changed directly; release or migrate the managed proxy")
	ErrCatProxiesRegionMismatch          = infraerrors.Conflict("CATPROXIES_REGION_MISMATCH", "catproxies exit region does not match the strict target")
)

const (
	CatProxiesProviderType = "catproxies"

	CatProxiesStatusActive          = "active"
	CatProxiesStatusRetiring        = "retiring"
	CatProxiesStatusDisabled        = "disabled"
	CatProxiesStatusCredentialError = "credential_error"

	CatProxiesProtocolHTTP    = "http"
	CatProxiesProtocolSOCKS5H = "socks5h"

	CatProxiesHTTPStickyPort    = 10000
	CatProxiesSOCKS5HStickyPort = 12000

	CatProxiesMinLifetimeMinutes = 15
	CatProxiesMaxLifetimeMinutes = 1440

	ManagedProxyLeaseStatePending  = "pending"
	ManagedProxyLeaseStateActive   = "active"
	ManagedProxyLeaseStateRotating = "rotating"
	ManagedProxyLeaseStateExpired  = "expired"
	ManagedProxyLeaseStateFailed   = "failed"
	ManagedProxyLeaseStateReleased = "released"

	ManagedProxyLeaseHealthUnknown   = "unknown"
	ManagedProxyLeaseHealthHealthy   = "healthy"
	ManagedProxyLeaseHealthDegraded  = "degraded"
	ManagedProxyLeaseHealthUnhealthy = "unhealthy"
)

// CatProxyProviderConfig is the service-layer model for a CatProxies provider.
type CatProxyProviderConfig struct {
	ID                 int64
	Name               string
	ProviderType       string
	Status             string
	IsDefault          bool
	Protocol           string
	Host               string
	BaseUsername       string
	Password           string
	DefaultCountry     *string
	DefaultState       *string
	DefaultCity        *string
	LifetimeMinutes    int
	Strict             bool
	LastProbeAt        *time.Time
	LastProbeLatencyMs *int
	LastError          *string
	LastErrorAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ManagedProxyLease is the service-layer snapshot of one account's managed proxy.
type ManagedProxyLease struct {
	ID                      int64
	AccountID               int64
	ProxyID                 int64
	ProviderConfigID        int64
	SessionID               string
	TargetCountry           *string
	TargetState             *string
	TargetCity              *string
	Strict                  bool
	LifetimeMinutes         int
	State                   string
	HealthStatus            string
	HealthCheckedAt         *time.Time
	ObservedExitIP          *string
	ObservedCountry         *string
	ObservedState           *string
	ObservedCity            *string
	ObservedLatencyMs       *int
	ActivatedAt             *time.Time
	LastRotatedAt           *time.Time
	NextRotationAt          *time.Time
	ExpiresAt               *time.Time
	FailureCount            int
	ConsecutiveFailureCount int
	LastError               *string
	LastErrorAt             *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// CatProxyProviderConfigRepository persists provider configuration records.
type CatProxyProviderConfigRepository interface {
	Create(ctx context.Context, config *CatProxyProviderConfig) error
	GetByID(ctx context.Context, id int64) (*CatProxyProviderConfig, error)
	List(ctx context.Context) ([]CatProxyProviderConfig, error)
	Update(ctx context.Context, config *CatProxyProviderConfig) error
	DefaultExists(ctx context.Context, excludeID int64) (bool, error)
	Delete(ctx context.Context, id int64) error
}

// ManagedProxyLeaseRepository persists per-account managed proxy leases.
type ManagedProxyLeaseRepository interface {
	Create(ctx context.Context, lease *ManagedProxyLease) error
	GetByAccountID(ctx context.Context, accountID int64) (*ManagedProxyLease, error)
	GetByProxyID(ctx context.Context, proxyID int64) (*ManagedProxyLease, error)
	ListByProviderConfigID(ctx context.Context, providerConfigID int64) ([]ManagedProxyLease, error)
	ListDue(ctx context.Context, dueAt time.Time, limit int) ([]ManagedProxyLease, error)
	CountByProviderConfigID(ctx context.Context, providerConfigID int64) (int64, error)
	Update(ctx context.Context, lease *ManagedProxyLease) error
	Delete(ctx context.Context, accountID int64) error
}

// ValidateCatProxiesLifetime validates the provider and lease lifetime range.
func ValidateCatProxiesLifetime(minutes int) error {
	if minutes < CatProxiesMinLifetimeMinutes || minutes > CatProxiesMaxLifetimeMinutes {
		return fmt.Errorf("catproxies lifetime must be between %d and %d minutes", CatProxiesMinLifetimeMinutes, CatProxiesMaxLifetimeMinutes)
	}
	return nil
}

var (
	catProxiesCountryPattern     = regexp.MustCompile(`^[a-z]{2}$`)
	catProxiesRegionTokenPattern = regexp.MustCompile(`^[a-z0-9]+$`)
)

// ValidateCatProxiesRegion validates the provider's lowercase location tokens.
func ValidateCatProxiesRegion(country, state, city *string) error {
	if country != nil && !catProxiesCountryPattern.MatchString(*country) {
		return fmt.Errorf("catproxies country must be a lowercase ISO2 code")
	}
	for name, value := range map[string]*string{"state": state, "city": city} {
		if value != nil && !catProxiesRegionTokenPattern.MatchString(*value) {
			return fmt.Errorf("catproxies %s must be a lowercase token without spaces", name)
		}
	}
	if (state != nil || city != nil) && country == nil {
		return fmt.Errorf("catproxies state and city require a country")
	}
	if state != nil && *country != "us" {
		return fmt.Errorf("catproxies state targeting is only supported for country us")
	}
	return nil
}

// Validate checks the provider configuration before persistence.
func (c CatProxyProviderConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" || strings.TrimSpace(c.Host) == "" || strings.TrimSpace(c.BaseUsername) == "" || c.Password == "" {
		return fmt.Errorf("catproxies provider name, host, username, and password are required")
	}
	if len(c.Name) > 100 || len(c.Host) > 255 || len(c.BaseUsername) > 255 || len(c.Password) > 255 {
		return fmt.Errorf("catproxies provider fields exceed database limits")
	}
	if c.ProviderType != "" && c.ProviderType != CatProxiesProviderType {
		return fmt.Errorf("unsupported catproxies provider type %q", c.ProviderType)
	}
	if c.Status != "" && !isCatProxiesStatus(c.Status) {
		return fmt.Errorf("invalid catproxies provider status %q", c.Status)
	}
	if c.Protocol != "" && c.Protocol != CatProxiesProtocolHTTP && c.Protocol != CatProxiesProtocolSOCKS5H {
		return fmt.Errorf("invalid catproxies protocol %q", c.Protocol)
	}
	if err := ValidateCatProxiesRegion(c.DefaultCountry, c.DefaultState, c.DefaultCity); err != nil {
		return err
	}
	return ValidateCatProxiesLifetime(c.LifetimeMinutes)
}

// Validate checks lease snapshots and lifecycle fields before persistence.
func (l ManagedProxyLease) Validate() error {
	if l.AccountID <= 0 || l.ProxyID <= 0 || l.ProviderConfigID <= 0 || strings.TrimSpace(l.SessionID) == "" {
		return fmt.Errorf("managed proxy lease account, proxy, provider, and session are required")
	}
	if !isManagedProxyLeaseState(l.State) {
		return fmt.Errorf("invalid managed proxy lease state %q", l.State)
	}
	if !isManagedProxyLeaseHealth(l.HealthStatus) {
		return fmt.Errorf("invalid managed proxy lease health status %q", l.HealthStatus)
	}
	return ValidateCatProxiesLifetime(l.LifetimeMinutes)
}

func isCatProxiesStatus(status string) bool {
	switch status {
	case CatProxiesStatusActive, CatProxiesStatusRetiring, CatProxiesStatusDisabled, CatProxiesStatusCredentialError:
		return true
	default:
		return false
	}
}

func isManagedProxyLeaseState(state string) bool {
	switch state {
	case ManagedProxyLeaseStatePending, ManagedProxyLeaseStateActive, ManagedProxyLeaseStateRotating, ManagedProxyLeaseStateExpired, ManagedProxyLeaseStateFailed, ManagedProxyLeaseStateReleased:
		return true
	default:
		return false
	}
}

func isManagedProxyLeaseHealth(status string) bool {
	switch status {
	case ManagedProxyLeaseHealthUnknown, ManagedProxyLeaseHealthHealthy, ManagedProxyLeaseHealthDegraded, ManagedProxyLeaseHealthUnhealthy:
		return true
	default:
		return false
	}
}
