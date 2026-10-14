SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- Auto does not expose a session-linked usage receipt. These bounded evaluation
-- sessions are deliberately outside the money/minute ledger, never billable.
CREATE TABLE digital_teacher_studio_sessions (
 id UUID PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES users(id),
 api_key_id BIGINT NOT NULL REFERENCES api_keys(id),
 request_id VARCHAR(100) NOT NULL,
 digest VARCHAR(64) NOT NULL,
 phase VARCHAR(24) NOT NULL DEFAULT 'creating',
 upstream_id UUID,
 room_sealed TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 deadline TIMESTAMPTZ NOT NULL,
 closed_at TIMESTAMPTZ,
 UNIQUE(user_id,api_key_id,request_id)
);
CREATE UNIQUE INDEX digital_teacher_one_studio_session ON digital_teacher_studio_sessions(user_id) WHERE closed_at IS NULL;
ALTER TABLE digital_teacher_avatar_jobs ADD COLUMN generation VARCHAR(12) NOT NULL DEFAULT 'trinity' CHECK(generation IN ('trinity','legacy'));
