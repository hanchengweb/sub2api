SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- Historical assets get a full grace period from this migration, not creation.
ALTER TABLE organization_voice_assets
    ADD COLUMN last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX organization_voice_assets_reclaim ON organization_voice_assets(user_id,last_used_at,id)
    WHERE kind='clone' AND deleted_at IS NULL AND status IN ('ready','failed');

CREATE TABLE organization_voice_member_states (
    user_id BIGINT NOT NULL REFERENCES users(id),
    actor_id UUID NOT NULL,
    state VARCHAR(12) NOT NULL CHECK (state IN ('active','disabled','deleted')),
    version BIGINT NOT NULL CHECK (version > 0),
    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id,actor_id)
);

CREATE TABLE organization_voice_reclaims (
    asset_id UUID PRIMARY KEY REFERENCES organization_voice_assets(id),
    user_id BIGINT NOT NULL REFERENCES users(id),
    actor_id UUID NOT NULL,
    account_id BIGINT NOT NULL,
    speaker_id VARCHAR(160) NOT NULL,
    reason VARCHAR(24) NOT NULL CHECK (reason IN ('member_disabled','member_deleted','idle_expired')),
    reclaimed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX organization_voice_reclaims_owner ON organization_voice_reclaims(user_id,reclaimed_at);
