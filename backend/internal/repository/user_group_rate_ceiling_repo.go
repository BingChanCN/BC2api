package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type userGroupRateCeilingRepository struct {
	sql sqlExecutor
}

// NewUserGroupRateCeilingRepository 创建用户分组倍率上限仓储。
func NewUserGroupRateCeilingRepository(sqlDB *sql.DB) service.UserGroupRateCeilingRepository {
	return &userGroupRateCeilingRepository{sql: sqlDB}
}

func (r *userGroupRateCeilingRepository) GetByUserID(ctx context.Context, userID int64) (map[int64]float64, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT group_id, rate_ceiling
		FROM user_group_rate_ceilings
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64]float64)
	for rows.Next() {
		var groupID int64
		var ceiling float64
		if err := rows.Scan(&groupID, &ceiling); err != nil {
			return nil, err
		}
		result[groupID] = ceiling
	}
	return result, rows.Err()
}

func (r *userGroupRateCeilingRepository) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	var ceiling sql.NullFloat64
	err := scanSingleRow(ctx, r.sql, `
		SELECT rate_ceiling
		FROM user_group_rate_ceilings
		WHERE user_id = $1 AND group_id = $2
	`, []any{userID, groupID}, &ceiling)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !ceiling.Valid {
		return nil, nil
	}
	value := ceiling.Float64
	return &value, nil
}

func (r *userGroupRateCeilingRepository) Upsert(ctx context.Context, userID, groupID int64, ceiling float64) error {
	now := time.Now()
	_, err := r.sql.ExecContext(ctx, `
		INSERT INTO user_group_rate_ceilings (user_id, group_id, rate_ceiling, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (user_id, group_id)
		DO UPDATE SET rate_ceiling = EXCLUDED.rate_ceiling, updated_at = EXCLUDED.updated_at
	`, userID, groupID, ceiling, now)
	return err
}

func (r *userGroupRateCeilingRepository) Delete(ctx context.Context, userID, groupID int64) error {
	_, err := r.sql.ExecContext(ctx, `
		DELETE FROM user_group_rate_ceilings
		WHERE user_id = $1 AND group_id = $2
	`, userID, groupID)
	return err
}

func (r *userGroupRateCeilingRepository) DeleteByGroupID(ctx context.Context, groupID int64) error {
	_, err := r.sql.ExecContext(ctx, `DELETE FROM user_group_rate_ceilings WHERE group_id = $1`, groupID)
	return err
}

func (r *userGroupRateCeilingRepository) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := r.sql.ExecContext(ctx, `DELETE FROM user_group_rate_ceilings WHERE user_id = $1`, userID)
	return err
}
