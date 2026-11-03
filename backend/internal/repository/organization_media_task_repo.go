package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
)

type organizationMediaTaskRepository struct{ db *sql.DB }

func NewOrganizationMediaTaskRepository(db *sql.DB) mediatasks.Store {
	return &organizationMediaTaskRepository{db: db}
}

const organizationMediaTaskColumns = `id,user_id,api_key_id,group_id,request_id::text,digest,quota_request_id,kind,model,account_id,provider_id,status,progress,quantity,settlement_units,COALESCE(provider_usage,'null'::jsonb),error_code,content_type,lease_until,created_at,updated_at`

func scanOrganizationMediaTask(row interface{ Scan(...any) error }) (*mediatasks.Task, error) {
	t := new(mediatasks.Task)
	var code string
	err := row.Scan(&t.ID, &t.Owner.UserID, &t.Owner.APIKeyID, &t.Owner.GroupID, &t.RequestID, &t.Digest, &t.QuotaRequestID, &t.Kind, &t.Model, &t.AccountID, &t.ProviderID, &t.Status, &t.Progress, &t.Usage.Quantity, &t.SettlementUnits, &t.Usage.ProviderUsage, &code, &t.ContentType, &t.LeaseUntil, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, mediatasks.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if code != "" {
		t.Error = &mediatasks.TaskError{Code: code, Message: mediatasks.ErrorMessage(code)}
	}
	return t, nil
}
func (r *organizationMediaTaskRepository) Find(ctx context.Context, o mediatasks.Owner, kind, requestID string) (*mediatasks.Task, error) {
	return scanOrganizationMediaTask(r.db.QueryRowContext(ctx, `SELECT `+organizationMediaTaskColumns+` FROM organization_media_tasks WHERE user_id=$1 AND kind=$2 AND request_id=$3`, o.UserID, kind, requestID))
}
func (r *organizationMediaTaskRepository) Create(ctx context.Context, t *mediatasks.Task) (*mediatasks.Task, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var ownerID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND account_type='organization_service' AND deleted_at IS NULL FOR UPDATE`, t.Owner.UserID).Scan(&ownerID); err != nil {
		return nil, false, err
	}
	old, err := scanOrganizationMediaTask(tx.QueryRowContext(ctx, `SELECT `+organizationMediaTaskColumns+` FROM organization_media_tasks WHERE user_id=$1 AND kind=$2 AND request_id=$3`, t.Owner.UserID, t.Kind, t.RequestID))
	if err == nil {
		if old.Digest != t.Digest {
			return nil, false, mediatasks.ErrConflict
		}
		return old, false, nil
	}
	if !errors.Is(err, mediatasks.ErrNotFound) {
		return nil, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_media_tasks(id,user_id,api_key_id,group_id,request_id,digest,quota_request_id,kind,model,account_id,status,quantity,lease_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'queued',$11,$12)`, t.ID, t.Owner.UserID, t.Owner.APIKeyID, t.Owner.GroupID, t.RequestID, t.Digest, t.QuotaRequestID, t.Kind, t.Model, t.AccountID, t.Usage.Quantity, t.LeaseUntil)
	if err != nil {
		return nil, false, err
	}
	return t, true, tx.Commit()
}
func (r *organizationMediaTaskRepository) Get(ctx context.Context, o mediatasks.Owner, kind, id string) (*mediatasks.Task, error) {
	return scanOrganizationMediaTask(r.db.QueryRowContext(ctx, `SELECT `+organizationMediaTaskColumns+` FROM organization_media_tasks WHERE id=$1 AND user_id=$2 AND kind=$3`, id, o.UserID, kind))
}
func (r *organizationMediaTaskRepository) Start(ctx context.Context, id string) (bool, error) {
	v, err := r.db.ExecContext(ctx, `UPDATE organization_media_tasks SET status='running',updated_at=NOW() WHERE id=$1 AND status='queued' AND lease_until>NOW()`, id)
	if err != nil {
		return false, err
	}
	n, err := v.RowsAffected()
	return n == 1, err
}
func (r *organizationMediaTaskRepository) Update(ctx context.Context, t *mediatasks.Task, content []byte) error {
	code := ""
	if t.Error != nil {
		code = t.Error.Code
	}
	var usage any
	if len(t.Usage.ProviderUsage) > 0 {
		usage = string(t.Usage.ProviderUsage)
	}
	// Terminal results are immutable. Concurrent authenticated polls can neither
	// overwrite an output nor reverse a settled outcome.
	_, err := r.db.ExecContext(ctx, `UPDATE organization_media_tasks SET status=$2,progress=$3,provider_id=CASE WHEN $4<>'' THEN $4 ELSE provider_id END,provider_usage=COALESCE($5::jsonb,provider_usage),error_code=$6,content_type=CASE WHEN $7<>'' THEN $7 ELSE content_type END,content=COALESCE($8,content),settlement_units=COALESCE($9,settlement_units),updated_at=NOW() WHERE id=$1 AND status NOT IN ('succeeded','failed')`, t.ID, t.Status, t.Progress, t.ProviderID, usage, code, t.ContentType, content, t.SettlementUnits)
	return err
}
func (r *organizationMediaTaskRepository) Content(ctx context.Context, o mediatasks.Owner, kind, id string) ([]byte, string, error) {
	var content []byte
	var typ string
	err := r.db.QueryRowContext(ctx, `SELECT content,content_type FROM organization_media_tasks WHERE id=$1 AND user_id=$2 AND kind=$3 AND status='succeeded'`, id, o.UserID, kind).Scan(&content, &typ)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", mediatasks.ErrNotFound
	}
	return content, typ, err
}
