SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE organization_voice_speaker_slots
    ADD COLUMN allocated_user_id BIGINT REFERENCES users(id);

-- Historical claims, including deleted assets, permanently retain their owner.
UPDATE organization_voice_speaker_slots s
SET allocated_user_id = a.user_id
FROM organization_voice_assets a
WHERE s.asset_id = a.id;

CREATE INDEX organization_voice_slots_allocation
    ON organization_voice_speaker_slots(allocated_user_id, account_id, speaker_id);

CREATE TABLE organization_voice_slot_policies (
    user_id BIGINT PRIMARY KEY REFERENCES users(id),
    managed BOOLEAN NOT NULL DEFAULT TRUE CHECK (managed),
    total_slots BIGINT NOT NULL CHECK (total_slots >= 0 AND total_slots <= 1000000000),
    version BIGINT NOT NULL CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE organization_voice_slot_changes (
    user_id BIGINT NOT NULL REFERENCES users(id),
    request_id UUID NOT NULL,
    digest CHAR(64) NOT NULL,
    actor_user_id BIGINT NOT NULL CHECK (actor_user_id > 0),
    reason VARCHAR(500) NOT NULL,
    before_total BIGINT NOT NULL,
    after_total BIGINT NOT NULL,
    version BIGINT NOT NULL,
    receipt JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(user_id, request_id),
    UNIQUE(user_id, version)
);
