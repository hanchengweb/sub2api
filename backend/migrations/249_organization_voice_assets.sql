SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE organization_media_tasks ADD COLUMN actor_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE organization_media_tasks DROP CONSTRAINT organization_media_tasks_user_id_kind_request_id_key;
ALTER TABLE organization_media_tasks ADD UNIQUE (user_id,actor_id,kind,request_id);

-- Samples, provider keys and expiring preview URLs are never persisted here.
CREATE TABLE organization_voice_assets (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    actor_id UUID NOT NULL,
    group_id BIGINT NOT NULL,
    kind VARCHAR(12) NOT NULL CHECK(kind IN ('official','clone')),
    name VARCHAR(120) NOT NULL,
    model VARCHAR(160) NOT NULL,
    speaker_id VARCHAR(160) NOT NULL,
    account_id BIGINT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    status VARCHAR(20) NOT NULL CHECK(status IN ('ready','training','failed','indeterminate')),
    request_id UUID,
    digest CHAR(64) NOT NULL DEFAULT '',
    error_code VARCHAR(80) NOT NULL DEFAULT '',
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id,actor_id,request_id)
);
CREATE INDEX organization_voice_assets_owner ON organization_voice_assets(user_id,actor_id,created_at DESC);
CREATE TABLE organization_voice_speaker_slots (
    account_id BIGINT NOT NULL,
    speaker_id VARCHAR(160) NOT NULL,
    asset_id UUID UNIQUE REFERENCES organization_voice_assets(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(account_id,speaker_id)
);
-- A soft-deleted asset deliberately continues to own its slot. No reset/retrain.
