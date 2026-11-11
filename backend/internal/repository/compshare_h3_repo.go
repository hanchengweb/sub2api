package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func bindCompShareH3Charge(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	snap := *cmd.CompShareH3
	// This only runs for a newly claimed billing receipt. Replays and historical
	// snapshots retain their original billing basis, even after a quota grant.
	var organizationQuota bool
	if err := tx.QueryRowContext(ctx, `SELECT account_type='organization_service' AND EXISTS(
 SELECT 1 FROM organization_media_accounts WHERE user_id=$1)
 FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, snap.UserID).Scan(&organizationQuota); err != nil {
		return err
	}
	snap.MediaQuotaRequestID = ""
	if organizationQuota {
		if snap.Duration < 4 || snap.Duration > 30 || snap.RequestID != cmd.RequestID || snap.UserID != cmd.UserID || snap.APIKeyID != cmd.APIKeyID {
			return service.ErrMediaQuotaConflict
		}
		reservation, err := reserveOrganizationMediaQuota(ctx, tx, service.MediaQuotaClaim{
			UserID: snap.UserID, APIKeyID: snap.APIKeyID, RequestID: snap.RequestID,
			Digest: snap.PayloadHash, Kind: "video", Model: service.CompShareH3Model, Units: int64(snap.Duration),
		})
		if err != nil {
			return err
		}
		if reservation == nil || reservation.Replay {
			return service.ErrMediaQuotaConflict
		}
		snap.MediaQuotaRequestID = snap.RequestID
	}
	billing := *cmd
	billing.CompShareH3 = nil
	snap.Billing = &billing
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO media_task_charges(task_key,user_id,api_key_id,group_id,credits,request_id,h3_snapshot)
 VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`, snap.RequestID, snap.UserID, snap.APIKeyID, snap.GroupID, snap.Credits, snap.RequestID, string(raw))
	if err == nil {
		cmd.CompShareH3.MediaQuotaRequestID = snap.MediaQuotaRequestID
	}
	return err
}

func (r *usageBillingRepository) GetCompShareH3(ctx context.Context, id string, userID, keyID int64) (*service.CompShareH3Snapshot, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("billing DB unavailable")
	}
	var raw []byte
	var taskID string
	var dispatched, settled bool
	var terminalStatus string
	err := r.db.QueryRowContext(ctx, `SELECT h3_snapshot,COALESCE(task_id,''),dispatched_at IS NOT NULL,settled_at IS NOT NULL,COALESCE(settled_status,'')
 FROM media_task_charges WHERE (request_id=$1 OR task_id=$1) AND user_id=$2 AND api_key_id=$3 AND h3_snapshot IS NOT NULL`, id, userID, keyID).Scan(&raw, &taskID, &dispatched, &settled, &terminalStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var snap service.CompShareH3Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	snap.TaskID, snap.Dispatched, snap.Settled, snap.TerminalStatus = taskID, dispatched, settled, terminalStatus
	return &snap, nil
}

func (r *usageBillingRepository) ClaimCompShareH3(ctx context.Context, id string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	userID, err := lockCompShareH3RequestOwner(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var raw []byte
	var dispatched, settled bool
	if err = tx.QueryRowContext(ctx, `SELECT h3_snapshot,dispatched_at IS NOT NULL,settled_at IS NOT NULL
 FROM media_task_charges WHERE request_id=$1 AND h3_snapshot IS NOT NULL FOR UPDATE`, id).Scan(&raw, &dispatched, &settled); err != nil {
		return false, err
	}
	if dispatched || settled {
		return false, nil
	}
	var snap service.CompShareH3Snapshot
	if err = json.Unmarshal(raw, &snap); err != nil {
		return false, err
	}
	if snap.MediaQuotaRequestID != "" {
		if snap.RequestID != id || snap.UserID != userID {
			return false, service.ErrMediaQuotaConflict
		}
		if err = validateCompShareH3QuotaReservation(ctx, tx, &snap, true); err != nil {
			return false, err
		}
		if err = validateOrganizationMediaQuotaDispatch(ctx, tx, userID, snap.MediaQuotaRequestID); err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE media_task_charges SET dispatched_at=NOW() WHERE request_id=$1`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *usageBillingRepository) BindCompShareH3(ctx context.Context, id, taskID string) error {
	if taskID == "" || len(taskID) > 256 {
		return service.ErrMediaQuotaConflict
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	userID, err := lockCompShareH3RequestOwner(ctx, tx, id)
	if err != nil {
		return err
	}
	var raw []byte
	var boundTask string
	if err = tx.QueryRowContext(ctx, `SELECT h3_snapshot,COALESCE(task_id,'') FROM media_task_charges
 WHERE request_id=$1 AND h3_snapshot IS NOT NULL FOR UPDATE`, id).Scan(&raw, &boundTask); err != nil {
		return err
	}
	if boundTask != "" && boundTask != taskID {
		return errors.New("H3 task binding conflict")
	}
	var snap service.CompShareH3Snapshot
	if err = json.Unmarshal(raw, &snap); err != nil {
		return err
	}
	if snap.MediaQuotaRequestID != "" {
		if snap.RequestID != id || snap.UserID != userID {
			return service.ErrMediaQuotaConflict
		}
		if err = validateCompShareH3QuotaReservation(ctx, tx, &snap, false); err != nil {
			return err
		}
		if err = bindOrganizationMediaQuota(ctx, tx, userID, snap.MediaQuotaRequestID, taskID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE media_task_charges SET task_id=$2 WHERE request_id=$1`, id, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// Lock user before receipt, matching quantity grants and reservations. Selecting
// the immutable owner does not lock the receipt in the opposite order.
func lockCompShareH3RequestOwner(ctx context.Context, tx *sql.Tx, id string) (int64, error) {
	var userID int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=(
 SELECT user_id FROM media_task_charges WHERE request_id=$1 AND h3_snapshot IS NOT NULL)
 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&userID)
	return userID, err
}

// A marked H3 receipt must retain its original quantity reservation. The
// generic quota methods intentionally permit missing rows for legacy tasks;
// that compatibility must never turn a marked H3 task into balance-only work.
func validateCompShareH3QuotaReservation(ctx context.Context, tx *sql.Tx, snap *service.CompShareH3Snapshot, dispatch bool) error {
	if snap.MediaQuotaRequestID == "" || snap.MediaQuotaRequestID != snap.RequestID {
		return service.ErrMediaQuotaConflict
	}
	var keyID, units int64
	var kind, model, status string
	err := tx.QueryRowContext(ctx, `SELECT api_key_id,kind,model,units,status
 FROM organization_media_reservations WHERE user_id=$1 AND request_id=$2`, snap.UserID, snap.MediaQuotaRequestID).Scan(&keyID, &kind, &model, &units, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrMediaQuotaConflict
	}
	if err != nil {
		return err
	}
	if keyID != snap.APIKeyID || units != int64(snap.Duration) || kind != "video" || model != service.CompShareH3Model || (dispatch && status != "reserved") {
		return service.ErrMediaQuotaConflict
	}
	return nil
}

// The row lock covers the terminal marker, all refunds and the usage receipt.
// A failed transaction can be retried on the same task; it never consumes the refund first.
func (r *usageBillingRepository) SettleCompShareH3(ctx context.Context, id string, userID, keyID int64, status, res string, seconds float64) (float64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var lockedUser int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&lockedUser); err != nil {
		return 0, err
	}
	var raw []byte
	var settled bool
	var createdAt string
	err = tx.QueryRowContext(ctx, `SELECT h3_snapshot,settled_at IS NOT NULL,created_at::text FROM media_task_charges
 WHERE request_id=$1 AND user_id=$2 AND api_key_id=$3 AND h3_snapshot IS NOT NULL FOR UPDATE`, id, userID, keyID).Scan(&raw, &settled, &createdAt)
	if err != nil {
		return 0, err
	}
	if settled {
		return 0, nil
	}
	var snap service.CompShareH3Snapshot
	if err = json.Unmarshal(raw, &snap); err != nil {
		return 0, err
	}
	amount, err := service.CompShareH3SettlementAmount(&snap, status, res, seconds)
	if err != nil {
		return 0, err
	}
	refund := snap.Credits - amount
	if refund < 0 {
		return 0, errors.New("invalid H3 refund")
	}
	if snap.MediaQuotaRequestID != "" {
		if snap.RequestID != id || snap.UserID != userID || snap.APIKeyID != keyID {
			return 0, service.ErrMediaQuotaConflict
		}
		if err = validateCompShareH3QuotaReservation(ctx, tx, &snap, false); err != nil {
			return 0, err
		}
		actual, measurement := int64(0), "provider_failure"
		if status == "succeeded" {
			actual, measurement = int64(math.Ceil(seconds)), "provider_output"
		} else if status == "submission_rejected" {
			measurement = "submission_rejected"
		}
		if err = settleOrganizationMediaQuota(ctx, tx, userID, snap.MediaQuotaRequestID, "", actual, measurement); err != nil {
			return 0, err
		}
	}
	if refund > 0 {
		result, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$1,updated_at=NOW() WHERE id=$2 AND deleted_at IS NULL`, refund, userID)
		if err != nil {
			return 0, err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return 0, service.ErrUserNotFound
		}
		cmd := snap.Billing
		if cmd == nil {
			return 0, errors.New("H3 billing receipt missing")
		}
		fraction := refund / snap.Credits
		if cmd.APIKeyQuotaCost > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE api_keys SET quota_used=GREATEST(quota_used-$1,0),
    status=CASE WHEN status='quota_exhausted' AND quota_used-$1<quota THEN 'active' ELSE status END,updated_at=NOW()
    WHERE id=$2`, cmd.APIKeyQuotaCost*fraction, keyID)
			if err != nil {
				return 0, err
			}
		}
		if cmd.APIKeyRateLimitCost > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE api_keys SET
    usage_5h=CASE WHEN window_5h_start<=$3::timestamptz THEN GREATEST(usage_5h-$1,0) ELSE usage_5h END,
    usage_1d=CASE WHEN window_1d_start<=$3::timestamptz THEN GREATEST(usage_1d-$1,0) ELSE usage_1d END,
    usage_7d=CASE WHEN window_7d_start<=$3::timestamptz THEN GREATEST(usage_7d-$1,0) ELSE usage_7d END,updated_at=NOW() WHERE id=$2`, cmd.APIKeyRateLimitCost*fraction, keyID, createdAt)
			if err != nil {
				return 0, err
			}
		}
		if cmd.AccountQuotaCost > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE accounts SET extra=COALESCE(extra,'{}'::jsonb)||jsonb_build_object(
    'quota_used',GREATEST(COALESCE((extra->>'quota_used')::numeric,0)-$1,0),
    'quota_daily_used',CASE WHEN COALESCE(extra->>'quota_daily_start','9999-01-01')::timestamptz<=$3::timestamptz THEN GREATEST(COALESCE((extra->>'quota_daily_used')::numeric,0)-$1,0) ELSE COALESCE((extra->>'quota_daily_used')::numeric,0) END,
    'quota_weekly_used',CASE WHEN COALESCE(extra->>'quota_weekly_start','9999-01-01')::timestamptz<=$3::timestamptz THEN GREATEST(COALESCE((extra->>'quota_weekly_used')::numeric,0)-$1,0) ELSE COALESCE((extra->>'quota_weekly_used')::numeric,0) END),updated_at=NOW() WHERE id=$2`, cmd.AccountQuotaCost*fraction, snap.AccountID, createdAt)
			if err != nil {
				return 0, err
			}
		}
	}
	ratio := 0.0
	if snap.Credits > 0 {
		ratio = amount / snap.Credits
	}
	_, err = tx.ExecContext(ctx, `UPDATE usage_logs SET refunded_at=CASE WHEN $3::numeric>0 THEN NOW() ELSE refunded_at END,
 refunded_credits=$3,total_cost=$4,actual_cost=$5,output_cost=output_cost*$6,account_stats_cost=account_stats_cost*$6,
 video_resolution=CASE WHEN $7='' THEN video_resolution ELSE LOWER($7) END,
 video_duration_seconds=CASE WHEN $8::numeric>0 THEN CEIL($8::numeric)::int ELSE video_duration_seconds END
 WHERE request_id=$1 AND api_key_id=$2`, id, keyID, refund, snap.BaseCost*ratio, amount, ratio, res, seconds)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE media_task_charges SET settled_at=NOW(),settled_credits=$2,refunded_credits=$3,settled_status=$5,
 refunded_at=CASE WHEN $3::numeric>0 THEN NOW() ELSE refunded_at END WHERE request_id=$1 AND api_key_id=$4`, id, amount, refund, keyID, status)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return refund, nil
}
