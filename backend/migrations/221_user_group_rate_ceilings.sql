-- 用户自设的分组计费倍率上限（偏好，非运营定价）
-- 请求时若 base_rate > ceiling 则拒绝；未设置表示不限制。
CREATE TABLE IF NOT EXISTS user_group_rate_ceilings (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id     BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    rate_ceiling DECIMAL(10, 4) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id),
    CONSTRAINT user_group_rate_ceilings_positive CHECK (rate_ceiling > 0)
);

CREATE INDEX IF NOT EXISTS idx_user_group_rate_ceilings_group_id
    ON user_group_rate_ceilings(group_id);

COMMENT ON TABLE user_group_rate_ceilings IS '用户自设分组计费倍率上限（偏好）';
COMMENT ON COLUMN user_group_rate_ceilings.user_id IS '用户ID';
COMMENT ON COLUMN user_group_rate_ceilings.group_id IS '分组/渠道ID';
COMMENT ON COLUMN user_group_rate_ceilings.rate_ceiling IS '用户可接受的最高计费倍率；请求 base_rate 超过则拒绝';
