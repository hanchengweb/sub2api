package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func bindCompShareH3Charge(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) error {
	snap := *cmd.CompShareH3
	billing := *cmd
	billing.CompShareH3 = nil
	snap.Billing = &billing
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO media_task_charges(task_key,user_id,api_key_id,group_id,credits,request_id,h3_snapshot)
 VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`, snap.RequestID, snap.UserID, snap.APIKeyID, snap.GroupID, snap.Credits, snap.RequestID, string(raw))
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
	result, err := r.db.ExecContext(ctx, `UPDATE media_task_charges SET dispatched_at=NOW()
 WHERE request_id=$1 AND h3_snapshot IS NOT NULL AND dispatched_at IS NULL AND settled_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *usageBillingRepository) BindCompShareH3(ctx context.Context, id, taskID string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE media_task_charges SET task_id=$2 WHERE request_id=$1
 AND h3_snapshot IS NOT NULL AND (task_id IS NULL OR task_id=$2)`, id, taskID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("H3 task binding conflict")
	}
	return err
}

// The row lock covers the terminal marker, all refunds and the usage receipt.
// A failed transaction can be retried on the same task; it never consumes the refund first.
func (r *usageBillingRepository) SettleCompShareH3(ctx context.Context, id string, userID, keyID int64, status, res string, seconds float64) (float64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
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
