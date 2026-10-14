SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

CREATE TABLE digital_teacher_avatar_jobs (
    id UUID PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    request_id UUID NOT NULL,
    name VARCHAR(60) NOT NULL,
    digest CHAR(64) NOT NULL,
    preview BYTEA NOT NULL,
    state VARCHAR(16) NOT NULL CHECK(state IN ('submitting','pending','processing','ready','failed','unknown')),
    face_id UUID,
    message VARCHAR(300) NOT NULL DEFAULT '',
    queue_position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    checked_at TIMESTAMPTZ,
    UNIQUE(user_id,request_id)
);
CREATE INDEX digital_teacher_avatar_jobs_owner ON digital_teacher_avatar_jobs(user_id,created_at DESC);
CREATE UNIQUE INDEX digital_teacher_avatar_jobs_face ON digital_teacher_avatar_jobs(face_id) WHERE face_id IS NOT NULL;
