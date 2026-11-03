SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- Keep the optional Ent JSON field nullable and without a default.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS video_model_prices JSONB;
