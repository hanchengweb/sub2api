package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
)

type organizationVoiceAssetRepository struct{ db *sql.DB }

func NewOrganizationVoiceAssetRepository(db *sql.DB) voiceassets.Store {
	return &organizationVoiceAssetRepository{db}
}

const voiceAssetColumns = `id::text,user_id,actor_id::text,group_id,kind,name,model,speaker_id,account_id,enabled,status,COALESCE(request_id::text,''),digest,error_code,deleted_at IS NOT NULL,created_at,updated_at`

func scanVoiceAsset(row interface{ Scan(...any) error }) (*voiceassets.Asset, error) {
	a := new(voiceassets.Asset)
	var code string
	e := row.Scan(&a.ID, &a.Owner.UserID, &a.Owner.ActorID, &a.Owner.GroupID, &a.Kind, &a.Name, &a.Model, &a.SpeakerID, &a.AccountID, &a.Enabled, &a.Status, &a.RequestID, &a.Digest, &code, &a.Deleted, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, mediatasks.ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if code != "" {
		a.Error = &mediatasks.TaskError{Code: code, Message: "克隆结果需要核对，请查询原声音资产，勿重复上传。"}
	}
	return a, nil
}
func (r *organizationVoiceAssetRepository) List(ctx context.Context, o mediatasks.Owner) ([]*voiceassets.Asset, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT `+voiceAssetColumns+` FROM organization_voice_assets WHERE user_id=$1 AND actor_id=$2 AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 500`, o.UserID, o.ActorID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := make([]*voiceassets.Asset, 0)
	for rows.Next() {
		a, e := scanVoiceAsset(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *organizationVoiceAssetRepository) Get(ctx context.Context, o mediatasks.Owner, id string) (*voiceassets.Asset, error) {
	return scanVoiceAsset(r.db.QueryRowContext(ctx, `SELECT `+voiceAssetColumns+` FROM organization_voice_assets WHERE id=$1 AND user_id=$2 AND actor_id=$3 AND deleted_at IS NULL`, id, o.UserID, o.ActorID))
}
func (r *organizationVoiceAssetRepository) CreateOfficial(ctx context.Context, a *voiceassets.Asset) error {
	_, e := r.db.ExecContext(ctx, `INSERT INTO organization_voice_assets(id,user_id,actor_id,group_id,kind,name,model,speaker_id,account_id,enabled,status) VALUES($1,$2,$3,$4,'official',$5,$6,$7,$8,TRUE,'ready')`, a.ID, a.Owner.UserID, a.Owner.ActorID, a.Owner.GroupID, a.Name, a.Model, a.SpeakerID, a.AccountID)
	return e
}
func (r *organizationVoiceAssetRepository) ClaimClone(ctx context.Context, a *voiceassets.Asset, slots []voiceassets.Slot) (*voiceassets.Asset, bool, error) {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, false, e
	}
	defer tx.Rollback()
	// Serialize retries for this organization before checking their durable claim.
	var owner int64
	if e = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND account_type='organization_service' AND deleted_at IS NULL FOR UPDATE`, a.Owner.UserID).Scan(&owner); e != nil {
		return nil, false, e
	}
	old, e := scanVoiceAsset(tx.QueryRowContext(ctx, `SELECT `+voiceAssetColumns+` FROM organization_voice_assets WHERE user_id=$1 AND actor_id=$2 AND request_id=$3`, a.Owner.UserID, a.Owner.ActorID, a.RequestID))
	if e == nil {
		if old.Digest != a.Digest {
			return nil, false, mediatasks.ErrConflict
		}
		if old.Deleted {
			return nil, false, mediatasks.ErrNotFound
		}
		return old, false, nil
	}
	if !errors.Is(e, mediatasks.ErrNotFound) {
		return nil, false, e
	}
	slots, e = configuredVoiceSlots(ctx, tx, a.Owner.UserID, slots, true)
	if e != nil {
		return nil, false, e
	}
	var managed bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_voice_slot_policies WHERE user_id=$1 AND managed)`, a.Owner.UserID).Scan(&managed); e != nil {
		return nil, false, e
	}
	found := false
	for _, slot := range slots {
		if _, e = tx.ExecContext(ctx, `INSERT INTO organization_voice_speaker_slots(account_id,speaker_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, slot.AccountID, slot.SpeakerID); e != nil {
			return nil, false, e
		}
		var account int64
		var speaker string
		e = tx.QueryRowContext(ctx, `SELECT account_id,speaker_id FROM organization_voice_speaker_slots
            WHERE account_id=$1 AND speaker_id=$2 AND asset_id IS NULL
            AND (($4 AND allocated_user_id=$3) OR (NOT $4 AND allocated_user_id IS NULL))
            FOR UPDATE SKIP LOCKED`, slot.AccountID, slot.SpeakerID, a.Owner.UserID, managed).Scan(&account, &speaker)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			return nil, false, e
		}
		a.AccountID, a.SpeakerID = account, speaker
		found = true
		break
	}
	if !found {
		return nil, false, voiceassets.ErrNoSlot
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO organization_voice_assets(id,user_id,actor_id,group_id,kind,name,model,speaker_id,account_id,enabled,status,request_id,digest,error_code) VALUES($1,$2,$3,$4,'clone',$5,$6,$7,$8,TRUE,'indeterminate',$9,$10,'voice_clone_verification_pending')`, a.ID, a.Owner.UserID, a.Owner.ActorID, a.Owner.GroupID, a.Name, a.Model, a.SpeakerID, a.AccountID, a.RequestID, a.Digest)
	if e != nil {
		return nil, false, e
	}
	result, e := tx.ExecContext(ctx, `UPDATE organization_voice_speaker_slots SET asset_id=$3,allocated_user_id=$4 WHERE account_id=$1 AND speaker_id=$2 AND asset_id IS NULL`, a.AccountID, a.SpeakerID, a.ID, a.Owner.UserID)
	if e != nil {
		return nil, false, e
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		return nil, false, voiceassets.ErrNoSlot
	}
	if e = tx.Commit(); e != nil {
		return nil, false, e
	}
	return a, true, nil
}
func (r *organizationVoiceAssetRepository) Patch(ctx context.Context, o mediatasks.Owner, id string, name *string, enabled *bool) (*voiceassets.Asset, error) {
	return scanVoiceAsset(r.db.QueryRowContext(ctx, `UPDATE organization_voice_assets SET name=COALESCE($4,name),enabled=COALESCE($5,enabled),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND actor_id=$3 AND deleted_at IS NULL RETURNING `+voiceAssetColumns, id, o.UserID, o.ActorID, name, enabled))
}
func (r *organizationVoiceAssetRepository) Delete(ctx context.Context, o mediatasks.Owner, id string) error {
	v, e := r.db.ExecContext(ctx, `UPDATE organization_voice_assets SET enabled=FALSE,deleted_at=NOW(),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND actor_id=$3 AND deleted_at IS NULL`, id, o.UserID, o.ActorID)
	if e != nil {
		return e
	}
	n, e := v.RowsAffected()
	if e == nil && n == 0 {
		return mediatasks.ErrNotFound
	}
	return e
}
func (r *organizationVoiceAssetRepository) UpdateTraining(ctx context.Context, a *voiceassets.Asset) error {
	code := ""
	if a.Error != nil {
		code = a.Error.Code
	}
	_, e := r.db.ExecContext(ctx, `UPDATE organization_voice_assets SET status=$2,error_code=$3,updated_at=NOW() WHERE id=$1 AND kind='clone' AND deleted_at IS NULL AND (status<>'ready' OR $2='ready')`, a.ID, a.Status, code)
	return e
}
func (r *organizationVoiceAssetRepository) HasFreeSlot(ctx context.Context, o mediatasks.Owner, slots []voiceassets.Slot) (bool, error) {
	var free bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(
        SELECT 1 FROM jsonb_to_recordset($1::jsonb) AS c(account_id BIGINT,speaker_id TEXT)
        LEFT JOIN organization_voice_speaker_slots s USING(account_id,speaker_id)
        WHERE s.asset_id IS NULL AND (
            (EXISTS(SELECT 1 FROM organization_voice_slot_policies WHERE user_id=$2 AND managed) AND s.allocated_user_id=$2)
            OR (NOT EXISTS(SELECT 1 FROM organization_voice_slot_policies WHERE user_id=$2 AND managed) AND s.allocated_user_id IS NULL)))`, voiceSlotsJSON(voiceassets.SortedSlots(slots)), o.UserID).Scan(&free)
	return free, err
}
