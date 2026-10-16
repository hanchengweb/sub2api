SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- The billing account remains the WindHub account. A trusted organization
-- gateway supplies an opaque member scope; personal requests retain ''.
ALTER TABLE digital_teachers ADD COLUMN caller_scope VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE digital_teacher_assets ADD COLUMN caller_scope VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE digital_teacher_avatar_jobs ADD COLUMN caller_scope VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE digital_teacher_studio_sessions ADD COLUMN caller_scope VARCHAR(64) NOT NULL DEFAULT '';

ALTER TABLE digital_teachers DROP CONSTRAINT digital_teachers_user_id_client_id_key;
ALTER TABLE digital_teachers ADD UNIQUE(user_id, caller_scope, client_id);
ALTER TABLE digital_teacher_assets DROP CONSTRAINT digital_teacher_assets_pkey;
ALTER TABLE digital_teacher_assets ADD PRIMARY KEY(user_id, caller_scope, kind, asset_id);
ALTER TABLE digital_teacher_avatar_jobs DROP CONSTRAINT digital_teacher_avatar_jobs_user_id_request_id_key;
ALTER TABLE digital_teacher_avatar_jobs ADD UNIQUE(user_id, caller_scope, request_id);
DO $$
DECLARE old_constraint TEXT;
BEGIN
 SELECT conname INTO STRICT old_constraint FROM pg_constraint
 WHERE conrelid='digital_teacher_studio_sessions'::regclass
 AND contype='u' AND pg_get_constraintdef(oid)='UNIQUE (user_id, api_key_id, request_id)';
 EXECUTE format('ALTER TABLE digital_teacher_studio_sessions DROP CONSTRAINT %I', old_constraint);
END $$;
ALTER TABLE digital_teacher_studio_sessions ADD UNIQUE(user_id, caller_scope, api_key_id, request_id);
DROP INDEX digital_teacher_one_studio_session;
CREATE UNIQUE INDEX digital_teacher_one_studio_session
 ON digital_teacher_studio_sessions(user_id, caller_scope) WHERE closed_at IS NULL;
CREATE INDEX digital_teacher_studio_caller_usage
 ON digital_teacher_studio_sessions(user_id, caller_scope, created_at);
