SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE organization_media_lots DROP CONSTRAINT organization_media_lots_kind_check;
ALTER TABLE organization_media_lots ADD CONSTRAINT organization_media_lots_kind_check CHECK (kind IN ('image','video','audio','model3d'));
ALTER TABLE organization_media_reservations DROP CONSTRAINT organization_media_reservations_kind_check;
ALTER TABLE organization_media_reservations ADD CONSTRAINT organization_media_reservations_kind_check CHECK (kind IN ('image','video','audio','model3d'));

-- Only task metadata, provider receipts and private output bytes are retained.
-- Input text, credentials and provider signed URLs never enter this table.
CREATE TABLE organization_media_tasks (
    id VARCHAR(64) PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    api_key_id BIGINT NOT NULL,
    group_id BIGINT NOT NULL,
    request_id UUID NOT NULL,
    digest CHAR(64) NOT NULL,
    quota_request_id VARCHAR(180) NOT NULL,
    kind VARCHAR(8) NOT NULL CHECK (kind IN ('audio','model3d')),
    model VARCHAR(160) NOT NULL,
    account_id BIGINT NOT NULL,
    provider_id VARCHAR(256) NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL CHECK (status IN ('queued','running','succeeded','failed','indeterminate')),
    progress SMALLINT NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    quantity BIGINT NOT NULL CHECK (quantity > 0),
    settlement_units BIGINT,
    provider_usage JSONB,
    error_code VARCHAR(80) NOT NULL DEFAULT '',
    content_type VARCHAR(40) NOT NULL DEFAULT '',
    content BYTEA CHECK (octet_length(content) <= 134217728),
    lease_until TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id,kind,request_id),
    CHECK (status <> 'succeeded' OR (content IS NOT NULL AND octet_length(content)>0))
);
CREATE INDEX organization_media_tasks_owner ON organization_media_tasks(user_id,api_key_id,created_at DESC);
