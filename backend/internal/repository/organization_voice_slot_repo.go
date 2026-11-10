package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
)

// Recheck only non-secret inventory metadata while normal account updates are locked.
func configuredVoiceSlots(ctx context.Context, tx *sql.Tx, userID int64, candidates []voiceassets.Slot, lock bool) ([]voiceassets.Slot, error) {
	candidates = voiceassets.SortedSlots(candidates)
	out := make([]voiceassets.Slot, 0, len(candidates))
	var accountID int64
	allowed := map[voiceassets.Slot]bool{}
	for _, slot := range candidates {
		if slot.AccountID != accountID {
			accountID = slot.AccountID
			allowed = map[voiceassets.Slot]bool{}
			query := `SELECT extra FROM accounts WHERE id=$1 AND status='active' AND deleted_at IS NULL`
			if lock {
				query += ` FOR SHARE`
			}
			var raw []byte
			err := tx.QueryRowContext(ctx, query, accountID).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var extra map[string]any
			if json.Unmarshal(raw, &extra) != nil {
				continue
			}
			for _, configured := range voiceassets.ConfiguredSlots(accountID, userID, extra) {
				allowed[configured] = true
			}
		}
		if allowed[slot] {
			out = append(out, slot)
		}
	}
	return out, nil
}

func voiceSlotsJSON(slots []voiceassets.Slot) []byte {
	if slots == nil {
		slots = []voiceassets.Slot{}
	}
	raw, _ := json.Marshal(slots)
	return raw
}

func voiceSlotSummary(ctx context.Context, tx *sql.Tx, userID int64, slots []voiceassets.Slot) (*voiceassets.SlotSummary, error) {
	value := &voiceassets.SlotSummary{UserID: userID}
	err := tx.QueryRowContext(ctx, `SELECT managed,version,total_slots FROM organization_voice_slot_policies WHERE user_id=$1`, userID).Scan(&value.Managed, &value.Version, &value.Total)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Deleted, failed and indeterminate assets still own supplier capacity.
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM organization_voice_assets WHERE user_id=$1 AND kind='clone'`, userID).Scan(&value.Used); err != nil {
		return nil, err
	}
	if !value.Managed {
		value.Total = value.Used
	}
	if value.Total < value.Used {
		return nil, mediatasks.ErrUnavailable
	}
	value.Available = value.Total - value.Used
	err = tx.QueryRowContext(ctx, `
        SELECT COUNT(*) FROM jsonb_to_recordset($1::jsonb) AS c(account_id BIGINT,speaker_id TEXT)
        LEFT JOIN organization_voice_speaker_slots s USING(account_id,speaker_id)
        WHERE s.asset_id IS NULL AND s.allocated_user_id IS NULL`, voiceSlotsJSON(slots)).Scan(&value.Allocatable)
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (r *organizationVoiceAssetRepository) SlotSummary(ctx context.Context, userID int64, slots []voiceassets.Slot) (*voiceassets.SlotSummary, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND account_type='organization_service' AND deleted_at IS NULL)`, userID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, mediatasks.ErrNotFound
	}
	slots, err = configuredVoiceSlots(ctx, tx, userID, slots, false)
	if err != nil {
		return nil, err
	}
	value, err := voiceSlotSummary(ctx, tx, userID, slots)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return value, nil
}

type lockedVoiceSlot struct {
	voiceassets.Slot
	Claimed bool
	Owner   int64
}

func (r *organizationVoiceAssetRepository) SetSlots(ctx context.Context, userID int64, slots []voiceassets.Slot, change voiceassets.SlotChange, actorID int64) (*voiceassets.SlotSummary, error) {
	if err := change.Validate(); err != nil {
		return nil, err
	}
	if change.ExpectedUserID != userID {
		return nil, voiceassets.ErrSlotIdentity
	}
	if actorID <= 0 {
		return nil, voiceassets.ErrSlotRequest
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND account_type='organization_service' AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, mediatasks.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	var digest string
	var receipt []byte
	err = tx.QueryRowContext(ctx, `SELECT digest,receipt FROM organization_voice_slot_changes WHERE user_id=$1 AND request_id=$2`, userID, change.RequestID).Scan(&digest, &receipt)
	if err == nil {
		if digest != change.Digest() {
			return nil, voiceassets.ErrSlotIdempotency
		}
		value := new(voiceassets.SlotSummary)
		if err = json.Unmarshal(receipt, value); err != nil {
			return nil, err
		}
		return value, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	slots, err = configuredVoiceSlots(ctx, tx, userID, slots, true)
	if err != nil {
		return nil, err
	}
	before, err := voiceSlotSummary(ctx, tx, userID, slots)
	if err != nil {
		return nil, err
	}
	if change.ExpectedVersion != before.Version {
		return nil, voiceassets.ErrSlotVersion
	}
	if change.Total < before.Used {
		return nil, voiceassets.ErrSlotBelowUsed
	}

	rows, err := tx.QueryContext(ctx, `SELECT s.account_id,s.speaker_id
        FROM organization_voice_speaker_slots s LEFT JOIN organization_voice_assets a ON a.id=s.asset_id
        WHERE s.allocated_user_id=$1 OR a.user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	all := append([]voiceassets.Slot{}, slots...)
	for rows.Next() {
		var slot voiceassets.Slot
		if err = rows.Scan(&slot.AccountID, &slot.SpeakerID); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, slot)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var locked []lockedVoiceSlot
	// Include historical claims before taking any inventory lock, so inserts and
	// existing rows use the same global order as every clone claim.
	for _, pair := range voiceassets.SortedSlots(all) {
		_, err = tx.ExecContext(ctx, `INSERT INTO organization_voice_speaker_slots(account_id,speaker_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, pair.AccountID, pair.SpeakerID)
		if err != nil {
			return nil, err
		}
		var slot lockedVoiceSlot
		slot.Slot = pair
		err = tx.QueryRowContext(ctx, `SELECT s.asset_id IS NOT NULL,COALESCE(s.allocated_user_id,a.user_id,0)
            FROM organization_voice_speaker_slots s LEFT JOIN organization_voice_assets a ON a.id=s.asset_id
            WHERE s.account_id=$1 AND s.speaker_id=$2 FOR UPDATE OF s`, pair.AccountID, pair.SpeakerID).Scan(&slot.Claimed, &slot.Owner)
		if err != nil {
			return nil, err
		}
		locked = append(locked, slot)
	}
	eligible := map[voiceassets.Slot]bool{}
	for _, slot := range slots {
		eligible[slot] = true
	}
	keep := map[voiceassets.Slot]bool{}
	var claimed int64
	for _, slot := range locked {
		if slot.Owner == userID && slot.Claimed {
			keep[slot.Slot] = true
			claimed++
		}
	}
	if claimed != before.Used {
		return nil, mediatasks.ErrUnavailable
	}
	for _, slot := range locked {
		if int64(len(keep)) < change.Total && slot.Owner == userID && !slot.Claimed {
			keep[slot.Slot] = true
		}
	}
	for _, slot := range locked {
		if int64(len(keep)) < change.Total && slot.Owner == 0 && !slot.Claimed && eligible[slot.Slot] {
			keep[slot.Slot] = true
		}
	}
	if int64(len(keep)) != change.Total {
		return nil, voiceassets.ErrSlotInventory
	}
	for _, slot := range locked {
		if keep[slot.Slot] {
			_, err = tx.ExecContext(ctx, `UPDATE organization_voice_speaker_slots SET allocated_user_id=$3 WHERE account_id=$1 AND speaker_id=$2`, slot.AccountID, slot.SpeakerID, userID)
		} else if slot.Owner == userID && !slot.Claimed {
			_, err = tx.ExecContext(ctx, `UPDATE organization_voice_speaker_slots SET allocated_user_id=NULL WHERE account_id=$1 AND speaker_id=$2 AND asset_id IS NULL`, slot.AccountID, slot.SpeakerID)
		}
		if err != nil {
			return nil, err
		}
	}
	version := before.Version + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_voice_slot_policies(user_id,managed,total_slots,version) VALUES($1,TRUE,$2,$3)
        ON CONFLICT(user_id) DO UPDATE SET total_slots=EXCLUDED.total_slots,version=EXCLUDED.version,updated_at=NOW()`, userID, change.Total, version)
	if err != nil {
		return nil, err
	}
	value, err := voiceSlotSummary(ctx, tx, userID, slots)
	if err != nil {
		return nil, err
	}
	value.RequestID = change.RequestID
	receipt, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_voice_slot_changes(user_id,request_id,digest,actor_user_id,reason,before_total,after_total,version,receipt) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb)`, userID, change.RequestID, change.Digest(), actorID, change.Reason, before.Total, value.Total, value.Version, receipt)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return value, nil
}
