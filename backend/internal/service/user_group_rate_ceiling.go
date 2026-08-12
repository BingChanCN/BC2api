package service

import (
	"context"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const rateMultiplierCeilingExceededReason = "RATE_MULTIPLIER_CEILING_EXCEEDED"

// UserGroupRateCeilingRepository 用户自设分组倍率上限仓储。
// 与运营侧 user_group_rate_multipliers 分离，避免管理端同步清掉用户偏好。
type UserGroupRateCeilingRepository interface {
	GetByUserID(ctx context.Context, userID int64) (map[int64]float64, error)
	GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error)
	Upsert(ctx context.Context, userID, groupID int64, ceiling float64) error
	Delete(ctx context.Context, userID, groupID int64) error
	DeleteByGroupID(ctx context.Context, groupID int64) error
	DeleteByUserID(ctx context.Context, userID int64) error
}

// ResolveBillingBaseRate 解析与计费同源的 base_rate（用户组覆盖 ?? 组默认）× 峰时因子。
func ResolveBillingBaseRate(
	ctx context.Context,
	rateRepo UserGroupRateRepository,
	userID, groupID int64,
	groupDefaultMultiplier float64,
	peakMultiplier float64,
) float64 {
	base := groupDefaultMultiplier
	if rateRepo != nil && userID > 0 && groupID > 0 {
		if userRate, err := rateRepo.GetByUserAndGroup(ctx, userID, groupID); err == nil && userRate != nil {
			base = *userRate
		}
	}
	if peakMultiplier <= 0 {
		peakMultiplier = 1
	}
	return base * peakMultiplier
}

// CheckRateMultiplierCeiling 在进上游前校验用户自设上限。
// ceiling 未设置时放行；base_rate > ceiling 时返回 ErrRateMultiplierCeilingExceeded。
func CheckRateMultiplierCeiling(
	ctx context.Context,
	ceilingRepo UserGroupRateCeilingRepository,
	rateRepo UserGroupRateRepository,
	userID int64,
	group *Group,
	at time.Time,
) error {
	if ceilingRepo == nil || group == nil || !IsGroupContextValid(group) || userID <= 0 {
		return nil
	}
	ceiling, err := ceilingRepo.GetByUserAndGroup(ctx, userID, group.ID)
	if err != nil {
		// 读失败 fail-open：避免偏好存储抖动拖垮全站；上限是用户保护而非运营硬门。
		return nil
	}
	if ceiling == nil {
		return nil
	}
	if at.IsZero() {
		at = timezone.Now()
	}
	base := ResolveBillingBaseRate(ctx, rateRepo, userID, group.ID, group.RateMultiplier, group.PeakMultiplierAt(at))
	if base > *ceiling {
		msg := fmt.Sprintf(
			"current billing rate %.4f exceeds your ceiling %.4f for channel %q; raise the ceiling in channel settings or retry when the rate is lower",
			base,
			*ceiling,
			group.Name,
		)
		return infraerrors.Forbidden(rateMultiplierCeilingExceededReason, msg).WithMetadata(map[string]string{
			"group_id":   fmt.Sprintf("%d", group.ID),
			"group_name": group.Name,
			"base_rate":  fmt.Sprintf("%.4f", base),
			"ceiling":    fmt.Sprintf("%.4f", *ceiling),
		})
	}
	return nil
}

// IsRateMultiplierCeilingExceeded reports whether err is a ceiling rejection.
func IsRateMultiplierCeilingExceeded(err error) bool {
	return infraerrors.Reason(err) == rateMultiplierCeilingExceededReason
}
