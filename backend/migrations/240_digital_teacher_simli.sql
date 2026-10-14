SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

ALTER TABLE digital_teacher_sessions ADD COLUMN IF NOT EXISTS provider VARCHAR(16) NOT NULL DEFAULT 'liveavatar' CHECK(provider IN ('liveavatar','simli'));
ALTER TABLE digital_teacher_assets ADD COLUMN IF NOT EXISTS provider VARCHAR(16) NOT NULL DEFAULT 'liveavatar' CHECK(provider IN ('liveavatar','simli'));
