SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE digital_teacher_assets DROP CONSTRAINT digital_teacher_assets_provider_check;
ALTER TABLE digital_teacher_assets ADD CONSTRAINT digital_teacher_assets_provider_check CHECK(provider IN ('liveavatar','simli','anam'));
ALTER TABLE digital_teacher_avatar_jobs DROP CONSTRAINT digital_teacher_avatar_jobs_generation_check;
ALTER TABLE digital_teacher_avatar_jobs ADD CONSTRAINT digital_teacher_avatar_jobs_generation_check CHECK(generation IN ('trinity','legacy','cara-4'));
ALTER TABLE digital_teacher_studio_sessions ADD COLUMN provider VARCHAR(16) NOT NULL DEFAULT 'simli' CHECK(provider IN ('simli','anam'));
ALTER TABLE digital_teacher_studio_sessions ADD COLUMN duration_seconds DOUBLE PRECISION;
ALTER TABLE digital_teacher_studio_sessions ADD COLUMN checked_at TIMESTAMPTZ;
