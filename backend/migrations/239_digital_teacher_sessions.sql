SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- Money remains in users.balance/frozen_balance. Minutes are a separate,
-- optional contractual limit; an organization must receive an explicit grant.
CREATE TABLE IF NOT EXISTS digital_teacher_plans (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    sandbox_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    price_per_minute NUMERIC(20,10) NOT NULL DEFAULT 0 CHECK(price_per_minute >= 0),
    max_minutes INTEGER NOT NULL DEFAULT 2 CHECK(max_minutes BETWEEN 1 AND 30),
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS digital_teacher_minutes (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    granted NUMERIC(20,6) NOT NULL DEFAULT 0 CHECK(granted >= 0),
    reserved NUMERIC(20,6) NOT NULL DEFAULT 0 CHECK(reserved >= 0),
    consumed NUMERIC(20,6) NOT NULL DEFAULT 0 CHECK(consumed >= 0),
    CHECK(reserved + consumed <= granted)
);
CREATE TABLE IF NOT EXISTS digital_teacher_grants (
    user_id BIGINT NOT NULL REFERENCES users(id),
    request_id VARCHAR(100) NOT NULL,
    minutes NUMERIC(20,6) NOT NULL CHECK(minutes > 0),
    reason VARCHAR(500) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, request_id)
);
CREATE TABLE IF NOT EXISTS digital_teacher_assets (
    user_id BIGINT NOT NULL REFERENCES users(id),
    kind VARCHAR(10) NOT NULL CHECK(kind IN ('avatar','voice')),
    asset_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, kind, asset_id)
);
CREATE TABLE IF NOT EXISTS digital_teachers (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    client_id UUID NOT NULL,
    config JSONB NOT NULL,
    context_id UUID,
    context_hash VARCHAR(64),
    context_pending BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, client_id)
);
CREATE TABLE IF NOT EXISTS digital_teacher_sessions (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
    group_id BIGINT NOT NULL REFERENCES groups(id),
    request_id VARCHAR(100) NOT NULL,
    digest VARCHAR(64) NOT NULL,
    teacher_id UUID REFERENCES digital_teachers(id),
    sandbox BOOLEAN NOT NULL,
    max_seconds INTEGER NOT NULL CHECK(max_seconds BETWEEN 60 AND 1800),
    plan_version BIGINT NOT NULL,
    price_per_minute NUMERIC(20,10) NOT NULL CHECK(price_per_minute >= 0),
    held_amount NUMERIC(20,10) NOT NULL DEFAULT 0 CHECK(held_amount >= 0),
    minute_hold NUMERIC(20,6) NOT NULL DEFAULT 0 CHECK(minute_hold >= 0),
    phase VARCHAR(24) NOT NULL DEFAULT 'preparing',
    upstream_id UUID UNIQUE,
    upstream_token TEXT,
    start_response TEXT,
    start_requested_at TIMESTAMPTZ,
    sdk_nonce VARCHAR(64) NOT NULL,
    stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
    provider_credits NUMERIC(20,6),
    provider_seconds NUMERIC(20,6),
    charged_amount NUMERIC(20,10),
    receipt JSONB,
    receipt_seen_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    error_code VARCHAR(80),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deadline TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, api_key_id, request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS digital_teacher_one_active_per_user
 ON digital_teacher_sessions(user_id) WHERE phase NOT IN ('completed','failed');
CREATE INDEX IF NOT EXISTS digital_teacher_pending ON digital_teacher_sessions(updated_at)
 WHERE settled_at IS NULL;
