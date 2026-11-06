SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- Nullable pricing retains existing cache-read behavior for every old model.
ALTER TABLE channel_model_pricing ADD COLUMN IF NOT EXISTS image_cache_read_price NUMERIC(20,12) NULL;
ALTER TABLE channel_account_stats_model_pricing ADD COLUMN IF NOT EXISTS image_input_price NUMERIC(20,12) NULL;
ALTER TABLE channel_account_stats_model_pricing ADD COLUMN IF NOT EXISTS image_cache_read_price NUMERIC(20,12) NULL;
-- These are subsets of existing cache totals, not additional billable totals.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS cache_read_image_tokens INTEGER NULL;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS cache_read_image_cost DECIMAL(20,10) NOT NULL DEFAULT 0;
