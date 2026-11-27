package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
)

var _ voiceassets.ReclaimStore = (*organizationVoiceAssetRepository)(nil)

func lockVoiceOrganization(ctx context.Context, tx *sql.Tx, userID int64) error {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND account_type='organization_service' AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return mediatasks.ErrNotFound
	}
	return err
}

func (r *organizationVoiceAssetRepository) RecordMemberState(ctx context.Context, userID int64, state voiceassets.MemberState) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockVoiceOrganization(ctx, tx, userID); err != nil {
		return err
	}
	var version int64
	err = tx.QueryRowContext(ctx, `INSERT INTO organization_voice_member_states(user_id,actor_id,state,version) VALUES($1,$2,$3,$4)
	ON CONFLICT(user_id,actor_id) DO UPDATE SET state=EXCLUDED.state,version=EXCLUDED.version,observed_at=NOW()
	WHERE organization_voice_member_states.version < EXCLUDED.version RETURNING version`, userID, state.ActorID, state.State, state.Version).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		var current string
		if err = tx.QueryRowContext(ctx, `SELECT state,version FROM organization_voice_member_states WHERE user_id=$1 AND actor_id=$2`, userID, state.ActorID).Scan(&current, &version); err != nil {
			return err
		}
		if version != state.Version || current != state.State {
			return mediatasks.ErrConflict
		}
	} else if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *organizationVoiceAssetRepository) TouchVoiceUse(ctx context.Context, o mediatasks.Owner, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockVoiceOrganization(ctx, tx, o.UserID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE organization_voice_assets SET last_used_at=GREATEST(last_used_at,clock_timestamp())
	WHERE id=$1 AND user_id=$2 AND actor_id=$3 AND kind='clone' AND enabled AND status='ready' AND deleted_at IS NULL`, id, o.UserID, o.ActorID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return mediatasks.ErrNotFound
	}
	return tx.Commit()
}

func (r *organizationVoiceAssetRepository) ReclaimOrganizations(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT a.user_id FROM organization_voice_assets a JOIN users u ON u.id=a.user_id
	WHERE u.account_type='organization_service' AND u.deleted_at IS NULL AND a.kind='clone' AND a.deleted_at IS NULL AND a.status IN ('ready','failed') ORDER BY a.user_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		users = append(users, id)
	}
	return users, rows.Err()
}

func (r *organizationVoiceAssetRepository) Reclaim(ctx context.Context, userID int64, c voiceassets.ReclaimConfig) ([]voiceassets.ReclaimCandidate, error) {
	items := make([]voiceassets.ReclaimCandidate, 0)
	if !c.MemberEnabled && !c.ExpiryEnabled {
		return items, nil
	}
	if c.ExpiryEnabled && (c.IdleDays < 1 || c.IdleDays > 36500) {
		return nil, mediatasks.ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockVoiceOrganization(ctx, tx, userID); err != nil {
		return nil, err
	}
	// A preview-only category must not fill the batch and starve the other executor.
	rows, err := tx.QueryContext(ctx, `SELECT a.id::text,a.user_id,a.actor_id::text,a.account_id,a.speaker_id,a.last_used_at,
	CASE WHEN $2 AND m.state IN ('disabled','deleted') THEN 'member_'||m.state ELSE 'idle_expired' END
	FROM organization_voice_assets a LEFT JOIN organization_voice_member_states m ON m.user_id=a.user_id AND m.actor_id=a.actor_id
	WHERE a.user_id=$1 AND a.kind='clone' AND a.deleted_at IS NULL AND a.status IN ('ready','failed')
	AND (($2 AND m.state IN ('disabled','deleted')) OR ($3 AND a.last_used_at < NOW()-$4::INTEGER*INTERVAL '1 day'))
	ORDER BY CASE WHEN $2 AND m.state IN ('disabled','deleted') THEN $6::BOOLEAN ELSE $7::BOOLEAN END DESC,a.last_used_at,a.id
	LIMIT $5 FOR UPDATE OF a`, userID, c.MemberEnabled, c.ExpiryEnabled, c.IdleDays, voiceassets.ReclaimBatchLimit, c.MemberExecute, c.ExpiryExecute)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var item voiceassets.ReclaimCandidate
		if err = rows.Scan(&item.AssetID, &item.UserID, &item.ActorID, &item.AccountID, &item.SpeakerID, &item.LastUsedAt, &item.Reason); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Slot administration locks the shared inventory in this same global order.
	sort.Slice(items, func(i, j int) bool {
		if items[i].AccountID != items[j].AccountID {
			return items[i].AccountID < items[j].AccountID
		}
		return items[i].SpeakerID < items[j].SpeakerID
	})
	for i := range items {
		item := &items[i]
		execute := c.MemberEnabled && c.MemberExecute
		if item.Reason == "idle_expired" {
			execute = c.ExpiryEnabled && c.ExpiryExecute
		}
		if !execute {
			continue
		}
		if err = deleteVoiceAsset(ctx, tx, mediatasks.Owner{UserID: userID, ActorID: item.ActorID}, item.AssetID); err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO organization_voice_reclaims(asset_id,user_id,actor_id,account_id,speaker_id,reason) VALUES($1,$2,$3,$4,$5,$6)`, item.AssetID, userID, item.ActorID, item.AccountID, item.SpeakerID, item.Reason)
		if err != nil {
			return nil, err
		}
		item.Reclaimed = true
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return items, nil
}
