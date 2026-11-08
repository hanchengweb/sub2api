-- H3 uses the existing balance and billing ledger, with a durable price/owner receipt.
ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS h3_snapshot JSONB;
ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS dispatched_at TIMESTAMPTZ;
ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS settled_status VARCHAR(32);
ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS settled_at TIMESTAMPTZ;
ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS settled_credits NUMERIC(20,10);
ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS refunded_credits NUMERIC(20,10) NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_task_h3_request
    ON media_task_charges (request_id, api_key_id) WHERE h3_snapshot IS NOT NULL;
