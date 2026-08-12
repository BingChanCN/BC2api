package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ceilingRepoStub struct {
	ceiling *float64
	err     error
}

func (s *ceilingRepoStub) GetByUserID(context.Context, int64) (map[int64]float64, error) {
	return nil, nil
}
func (s *ceilingRepoStub) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	return s.ceiling, s.err
}
func (s *ceilingRepoStub) Upsert(context.Context, int64, int64, float64) error { return nil }
func (s *ceilingRepoStub) Delete(context.Context, int64, int64) error          { return nil }
func (s *ceilingRepoStub) DeleteByGroupID(context.Context, int64) error        { return nil }
func (s *ceilingRepoStub) DeleteByUserID(context.Context, int64) error         { return nil }

type rateRepoStubForCeiling struct {
	rate *float64
}

func (s *rateRepoStubForCeiling) GetByUserID(context.Context, int64) (map[int64]float64, error) {
	return nil, nil
}
func (s *rateRepoStubForCeiling) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	return s.rate, nil
}
func (s *rateRepoStubForCeiling) GetRPMOverrideByUserAndGroup(context.Context, int64, int64) (*int, error) {
	return nil, nil
}
func (s *rateRepoStubForCeiling) GetByGroupID(context.Context, int64) ([]UserGroupRateEntry, error) {
	return nil, nil
}
func (s *rateRepoStubForCeiling) SyncUserGroupRates(context.Context, int64, map[int64]*float64) error {
	return nil
}
func (s *rateRepoStubForCeiling) SyncGroupRateMultipliers(context.Context, int64, []GroupRateMultiplierInput) error {
	return nil
}
func (s *rateRepoStubForCeiling) SyncGroupRPMOverrides(context.Context, int64, []GroupRPMOverrideInput) error {
	return nil
}
func (s *rateRepoStubForCeiling) ClearGroupRPMOverrides(context.Context, int64) error { return nil }
func (s *rateRepoStubForCeiling) DeleteByGroupID(context.Context, int64) error        { return nil }
func (s *rateRepoStubForCeiling) DeleteByUserID(context.Context, int64) error         { return nil }

func TestCheckRateMultiplierCeiling_NoCeilingAllows(t *testing.T) {
	group := &Group{ID: 7, Name: "Claude", RateMultiplier: 1.5, Status: StatusActive, Platform: PlatformAnthropic, Hydrated: true}
	err := CheckRateMultiplierCeiling(context.Background(), &ceilingRepoStub{}, &rateRepoStubForCeiling{}, 1, group, time.Now())
	require.NoError(t, err)
}

func TestCheckRateMultiplierCeiling_EqualAllows(t *testing.T) {
	ceiling := 1.5
	group := &Group{ID: 7, Name: "Claude", RateMultiplier: 1.5, Status: StatusActive, Platform: PlatformAnthropic, Hydrated: true}
	err := CheckRateMultiplierCeiling(context.Background(), &ceilingRepoStub{ceiling: &ceiling}, &rateRepoStubForCeiling{}, 1, group, time.Now())
	require.NoError(t, err)
}

func TestCheckRateMultiplierCeiling_ExceedsRejects(t *testing.T) {
	ceiling := 1.2
	userRate := 1.5
	group := &Group{ID: 7, Name: "Claude", RateMultiplier: 1.0, Status: StatusActive, Platform: PlatformAnthropic, Hydrated: true}
	err := CheckRateMultiplierCeiling(context.Background(), &ceilingRepoStub{ceiling: &ceiling}, &rateRepoStubForCeiling{rate: &userRate}, 1, group, time.Now())
	require.Error(t, err)
	require.True(t, IsRateMultiplierCeilingExceeded(err))
	require.Contains(t, err.Error(), "1.5000")
	require.Contains(t, err.Error(), "1.2000")
	require.Contains(t, err.Error(), "Claude")
}

func TestCheckRateMultiplierCeiling_IncludesPeak(t *testing.T) {
	ceiling := 1.5
	group := &Group{
		ID:                 7,
		Name:               "Claude",
		Platform:           PlatformAnthropic,
		Hydrated:           true,
		RateMultiplier:     1.0,
		Status:             StatusActive,
		SubscriptionType:   SubscriptionTypeSubscription,
		PeakRateEnabled:    true,
		PeakStart:          "00:00",
		PeakEnd:            "23:59",
		PeakRateMultiplier: 2.0,
	}
	// base = 1.0 * 2.0 = 2.0 > 1.5
	err := CheckRateMultiplierCeiling(context.Background(), &ceilingRepoStub{ceiling: &ceiling}, &rateRepoStubForCeiling{}, 1, group, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	require.Error(t, err)
	require.True(t, IsRateMultiplierCeilingExceeded(err))
}
