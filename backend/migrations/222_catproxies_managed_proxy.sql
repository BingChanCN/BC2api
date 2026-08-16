-- CatProxies managed proxy provider configurations and per-account leases.
-- This migration is intentionally idempotent because the migration runner may
-- retry the whole file after a failed deployment.

ALTER TABLE proxies
    ALTER COLUMN username TYPE VARCHAR(255),
    ALTER COLUMN password TYPE VARCHAR(255);

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS managed_proxy_ready BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE IF NOT EXISTS catproxy_provider_configs (
    id                 BIGSERIAL PRIMARY KEY,
    name               VARCHAR(100) NOT NULL UNIQUE,
    provider_type      VARCHAR(20) NOT NULL DEFAULT 'catproxies'
                       CHECK (provider_type = 'catproxies'),
    status             VARCHAR(20) NOT NULL DEFAULT 'active'
                       CHECK (status IN ('active', 'retiring', 'disabled', 'credential_error')),
    is_default         BOOLEAN NOT NULL DEFAULT FALSE,
    protocol           VARCHAR(20) NOT NULL DEFAULT 'http'
                       CHECK (protocol IN ('http', 'socks5h')),
    host               VARCHAR(255) NOT NULL,
    base_username      VARCHAR(255) NOT NULL,
    password           VARCHAR(255) NOT NULL,
    default_country    VARCHAR(100),
    default_state      VARCHAR(100),
    default_city       VARCHAR(100),
    lifetime_minutes   INT NOT NULL DEFAULT 60
                       CHECK (lifetime_minutes BETWEEN 15 AND 1440),
    strict             BOOLEAN NOT NULL DEFAULT TRUE,
    last_probe_at      TIMESTAMPTZ,
    last_probe_latency_ms INT,
    last_error         TEXT,
    last_error_at      TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS catproxy_provider_configs_provider_type_idx
    ON catproxy_provider_configs (provider_type);
CREATE INDEX IF NOT EXISTS catproxy_provider_configs_status_idx
    ON catproxy_provider_configs (status);
CREATE INDEX IF NOT EXISTS catproxy_provider_configs_default_idx
    ON catproxy_provider_configs (is_default);
CREATE UNIQUE INDEX IF NOT EXISTS catproxy_provider_configs_default_uidx
    ON catproxy_provider_configs (is_default)
    WHERE is_default;

CREATE TABLE IF NOT EXISTS managed_proxy_leases (
    id                       BIGSERIAL PRIMARY KEY,
    account_id               BIGINT NOT NULL UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
    proxy_id                 BIGINT NOT NULL UNIQUE REFERENCES proxies(id) ON DELETE CASCADE,
    provider_config_id       BIGINT NOT NULL REFERENCES catproxy_provider_configs(id) ON DELETE RESTRICT,
    session_id               VARCHAR(255) NOT NULL,
    target_country            VARCHAR(100),
    target_state              VARCHAR(100),
    target_city              VARCHAR(100),
    strict                   BOOLEAN NOT NULL,
    lifetime_minutes         INT NOT NULL CHECK (lifetime_minutes BETWEEN 15 AND 1440),
    state                    VARCHAR(20) NOT NULL DEFAULT 'pending'
                             CHECK (state IN ('pending', 'active', 'rotating', 'expired', 'failed', 'released')),
    health_status            VARCHAR(20) NOT NULL DEFAULT 'unknown'
                             CHECK (health_status IN ('unknown', 'healthy', 'degraded', 'unhealthy')),
    health_checked_at        TIMESTAMPTZ,
    observed_exit_ip         VARCHAR(45),
    observed_country         VARCHAR(100),
    observed_state           VARCHAR(100),
    observed_city            VARCHAR(100),
    observed_latency_ms      INT,
    activated_at             TIMESTAMPTZ,
    last_rotated_at          TIMESTAMPTZ,
    next_rotation_at         TIMESTAMPTZ,
    expires_at               TIMESTAMPTZ,
    failure_count            INT NOT NULL DEFAULT 0,
    consecutive_failure_count INT NOT NULL DEFAULT 0,
    last_error               TEXT,
    last_error_at            TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS managed_proxy_leases_provider_config_idx
    ON managed_proxy_leases (provider_config_id);
CREATE INDEX IF NOT EXISTS managed_proxy_leases_state_idx
    ON managed_proxy_leases (state);
CREATE INDEX IF NOT EXISTS managed_proxy_leases_health_status_idx
    ON managed_proxy_leases (health_status);
CREATE INDEX IF NOT EXISTS managed_proxy_leases_next_rotation_at_idx
    ON managed_proxy_leases (next_rotation_at);
CREATE INDEX IF NOT EXISTS managed_proxy_leases_expires_at_idx
    ON managed_proxy_leases (expires_at);
CREATE INDEX IF NOT EXISTS managed_proxy_leases_provider_state_idx
    ON managed_proxy_leases (provider_config_id, state);
