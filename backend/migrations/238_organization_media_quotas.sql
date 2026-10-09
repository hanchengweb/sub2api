SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

CREATE TABLE organization_media_accounts (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    version BIGINT NOT NULL DEFAULT 0,
    blocked BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE TABLE organization_media_grants (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES organization_media_accounts(user_id),
    request_id VARCHAR(180) NOT NULL,
    digest CHAR(64) NOT NULL,
    contract_ref VARCHAR(160) NOT NULL,
    reason VARCHAR(500) NOT NULL,
    effective_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > effective_at),
    version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, request_id)
);
CREATE TABLE organization_media_lots (
    id BIGSERIAL PRIMARY KEY,
    grant_id BIGINT NOT NULL REFERENCES organization_media_grants(id),
    kind VARCHAR(8) NOT NULL CHECK(kind IN ('image','video')),
    amount BIGINT NOT NULL CHECK(amount > 0),
    reserved BIGINT NOT NULL DEFAULT 0 CHECK(reserved >= 0),
    consumed BIGINT NOT NULL DEFAULT 0 CHECK(consumed >= 0),
    CHECK(reserved + consumed <= amount),
    UNIQUE(grant_id, kind)
);
CREATE TABLE organization_media_reservations (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES organization_media_accounts(user_id),
    api_key_id BIGINT NOT NULL,
    request_id VARCHAR(180) NOT NULL,
    digest CHAR(64) NOT NULL,
    kind VARCHAR(8) NOT NULL CHECK(kind IN ('image','video')),
    model VARCHAR(160) NOT NULL,
    units BIGINT NOT NULL CHECK(units > 0),
    actual_units BIGINT,
    task_id VARCHAR(256),
    status VARCHAR(20) NOT NULL DEFAULT 'reserved',
    measurement VARCHAR(32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE(user_id, request_id)
);
CREATE INDEX organization_media_reservations_task ON organization_media_reservations(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX organization_media_reservations_user_time ON organization_media_reservations(user_id, created_at DESC);
CREATE TABLE organization_media_task_bindings (
    user_id BIGINT NOT NULL,
    task_id VARCHAR(256) NOT NULL,
    reservation_id BIGINT NOT NULL REFERENCES organization_media_reservations(id),
    PRIMARY KEY(user_id, task_id)
);
CREATE TABLE organization_media_allocations (
    reservation_id BIGINT NOT NULL REFERENCES organization_media_reservations(id),
    lot_id BIGINT NOT NULL REFERENCES organization_media_lots(id),
    units BIGINT NOT NULL CHECK(units > 0),
    PRIMARY KEY(reservation_id, lot_id)
);
