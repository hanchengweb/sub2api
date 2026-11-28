SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '15s';

-- released_at: this platform trained the speaker and later released it, so a new
-- clone may overwrite it. retired_at: the supplier can never train it again.
ALTER TABLE organization_voice_speaker_slots
    ADD COLUMN released_at TIMESTAMPTZ,
    ADD COLUMN retired_at TIMESTAMPTZ,
    ADD COLUMN remaining_trainings INTEGER CHECK (remaining_trainings >= 0);

-- Remaining supplier trainings observed before overwriting a released speaker.
ALTER TABLE organization_voice_assets
    ADD COLUMN training_baseline INTEGER CHECK (training_baseline >= 0);

-- Deleted clones whose training finished return their speaker. A managed
-- organization keeps the reservation; a legacy one returns it to the shared pool.
UPDATE organization_voice_speaker_slots s
SET asset_id = NULL,
    allocated_user_id = CASE WHEN EXISTS (
        SELECT 1 FROM organization_voice_slot_policies p WHERE p.user_id = a.user_id AND p.managed
    ) THEN a.user_id END,
    released_at = CASE WHEN a.status = 'ready'
        OR a.error_code IN ('voice_clone_training_failed', 'voice_clone_rejected')
        THEN NOW() ELSE s.released_at END
FROM organization_voice_assets a
WHERE s.asset_id = a.id
  AND a.kind = 'clone'
  AND a.deleted_at IS NOT NULL
  AND a.status IN ('ready', 'failed');
