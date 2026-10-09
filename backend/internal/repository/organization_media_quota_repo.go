package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type organizationMediaQuotaRepository struct{ db *sql.DB }

func NewOrganizationMediaQuotaRepository(db *sql.DB) service.OrganizationMediaQuotaRepository {
	return &organizationMediaQuotaRepository{db: db}
}
func (r *organizationMediaQuotaRepository) Enabled(ctx context.Context, userID int64) (bool, error) {
	var yes bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_media_accounts WHERE user_id=$1)`, userID).Scan(&yes)
	return yes, err
}

func (r *organizationMediaQuotaRepository) lock(ctx context.Context, userID int64) (*sql.Tx, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND account_type='organization_service' AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&id)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (r *organizationMediaQuotaRepository) Grant(ctx context.Context, userID int64, g service.MediaQuotaGrant) (int64, error) {
	if g.Images < 0 || g.VideoSeconds < 0 || g.Images+g.VideoSeconds <= 0 || g.Images > 1000000000 || g.VideoSeconds > 1000000000 || !g.ExpiresAt.After(g.EffectiveAt) {
		return 0, service.ErrMediaQuotaConflict
	}
	body, _ := json.Marshal(g)
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	tx, err := r.lock(ctx, userID)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var oldDigest string
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT digest,version FROM organization_media_grants WHERE user_id=$1 AND request_id=$2`, userID, g.RequestID).Scan(&oldDigest, &version)
	if err == nil {
		if oldDigest != digest {
			return 0, service.ErrMediaQuotaConflict
		}
		return version, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO organization_media_accounts(user_id) VALUES($1) ON CONFLICT DO NOTHING`, userID)
	if err != nil {
		return 0, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE organization_media_accounts SET version=version+1 WHERE user_id=$1 AND version=$2 RETURNING version`, userID, g.ExpectedVersion).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrMediaQuotaConflict
	}
	if err != nil {
		return 0, err
	}
	var grantID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO organization_media_grants(user_id,request_id,digest,contract_ref,reason,effective_at,expires_at,version) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, userID, g.RequestID, digest, g.ContractRef, g.Reason, g.EffectiveAt, g.ExpiresAt, version).Scan(&grantID)
	if err != nil {
		return 0, err
	}
	for kind, amount := range map[string]int64{"image": g.Images, "video": g.VideoSeconds} {
		if amount > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO organization_media_lots(grant_id,kind,amount) VALUES($1,$2,$3)`, grantID, kind, amount); err != nil {
				return 0, err
			}
		}
	}
	return version, tx.Commit()
}

const mediaReservationColumns = `id,request_id,kind,model,units,actual_units,COALESCE(task_id,''),status,COALESCE(measurement,''),created_at,completed_at`

func scanMediaReservation(row interface{ Scan(...any) error }) (*service.MediaQuotaReservation, error) {
	v := new(service.MediaQuotaReservation)
	err := row.Scan(&v.ID, &v.RequestID, &v.Kind, &v.Model, &v.Units, &v.ActualUnits, &v.TaskID, &v.Status, &v.Measurement, &v.CreatedAt, &v.CompletedAt)
	return v, err
}

func (r *organizationMediaQuotaRepository) Reserve(ctx context.Context, c service.MediaQuotaClaim) (*service.MediaQuotaReservation, error) {
	if c.Units <= 0 || c.RequestID == "" || (c.Kind != "image" && c.Kind != "video") {
		return nil, service.ErrMediaQuotaConflict
	}
	tx, err := r.lock(ctx, c.UserID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var blocked bool
	err = tx.QueryRowContext(ctx, `SELECT blocked FROM organization_media_accounts WHERE user_id=$1`, c.UserID).Scan(&blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var digest string
	err = tx.QueryRowContext(ctx, `SELECT digest FROM organization_media_reservations WHERE user_id=$1 AND request_id=$2`, c.UserID, c.RequestID).Scan(&digest)
	if err == nil {
		if digest != c.Digest {
			return nil, service.ErrMediaQuotaConflict
		}
		v, e := scanMediaReservation(tx.QueryRowContext(ctx, `SELECT `+mediaReservationColumns+` FROM organization_media_reservations WHERE user_id=$1 AND request_id=$2`, c.UserID, c.RequestID))
		if e != nil {
			return nil, e
		}
		v.Replay = true
		return v, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if blocked {
		return nil, service.ErrMediaQuotaReview
	}
	rows, err := tx.QueryContext(ctx, `SELECT l.id,l.amount-l.reserved-l.consumed FROM organization_media_lots l JOIN organization_media_grants g ON g.id=l.grant_id WHERE g.user_id=$1 AND l.kind=$2 AND g.effective_at<=NOW() AND g.expires_at>NOW() AND l.amount>l.reserved+l.consumed ORDER BY g.expires_at,l.id`, c.UserID, c.Kind)
	if err != nil {
		return nil, err
	}
	type lot struct{ id, amount int64 }
	var lots []lot
	remaining := c.Units
	for rows.Next() {
		var l lot
		if err = rows.Scan(&l.id, &l.amount); err != nil {
			rows.Close()
			return nil, err
		}
		if remaining > 0 {
			if l.amount > remaining {
				l.amount = remaining
			}
			lots = append(lots, l)
			remaining -= l.amount
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if remaining > 0 {
		return nil, service.ErrMediaQuotaExceeded
	}
	v, err := scanMediaReservation(tx.QueryRowContext(ctx, `INSERT INTO organization_media_reservations(user_id,api_key_id,request_id,digest,kind,model,units) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+mediaReservationColumns, c.UserID, c.APIKeyID, c.RequestID, c.Digest, c.Kind, c.Model, c.Units))
	if err != nil {
		return nil, err
	}
	for _, l := range lots {
		if _, err = tx.ExecContext(ctx, `UPDATE organization_media_lots SET reserved=reserved+$2 WHERE id=$1`, l.id, l.amount); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO organization_media_allocations(reservation_id,lot_id,units) VALUES($1,$2,$3)`, v.ID, l.id, l.amount); err != nil {
			return nil, err
		}
	}
	return v, tx.Commit()
}

func (r *organizationMediaQuotaRepository) Bind(ctx context.Context, userID int64, requestID, taskID string) error {
	if taskID == "" || len(taskID) > 256 {
		return service.ErrMediaQuotaConflict
	}
	tx, err := r.lock(ctx, userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `UPDATE organization_media_reservations SET task_id=COALESCE(task_id,$3),status=CASE WHEN status='reserved' THEN 'submitted' ELSE status END WHERE user_id=$1 AND request_id=$2 RETURNING id`, userID, requestID, taskID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var bound int64
	err = tx.QueryRowContext(ctx, `INSERT INTO organization_media_task_bindings(user_id,task_id,reservation_id) VALUES($1,$2,$3) ON CONFLICT(user_id,task_id) DO UPDATE SET task_id=EXCLUDED.task_id RETURNING reservation_id`, userID, taskID, id).Scan(&bound)
	if err != nil {
		return err
	}
	if bound != id {
		return service.ErrMediaQuotaConflict
	}
	return tx.Commit()
}
func (r *organizationMediaQuotaRepository) ValidateDispatch(ctx context.Context, userID int64, requestID string) error {
	var expired bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_media_reservations r JOIN organization_media_allocations a ON a.reservation_id=r.id JOIN organization_media_lots l ON l.id=a.lot_id JOIN organization_media_grants g ON g.id=l.grant_id WHERE r.user_id=$1 AND r.request_id=$2 AND g.expires_at<=NOW())`, userID, requestID).Scan(&expired)
	if err != nil {
		return err
	}
	if expired {
		return service.ErrMediaQuotaExceeded
	}
	return nil
}

// requestID is preferred; polling binds by (authenticated organization, taskID).
// Only a verified terminal outcome releases quota. An unknown outcome stays held.
func (r *organizationMediaQuotaRepository) Settle(ctx context.Context, userID int64, requestID, taskID string, actual int64, measurement string) error {
	if actual < 0 && !(actual == -1 && measurement == "fixed_duration") {
		return service.ErrMediaQuotaConflict
	}
	tx, err := r.lock(ctx, userID)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	v, err := scanMediaReservation(tx.QueryRowContext(ctx, `SELECT `+mediaReservationColumns+` FROM organization_media_reservations WHERE user_id=$1 AND (($2<>'' AND request_id=$2) OR ($2='' AND $3<>'' AND id IN(SELECT reservation_id FROM organization_media_task_bindings WHERE user_id=$1 AND task_id=$3))) ORDER BY id LIMIT 1`, userID, requestID, taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if v.Status == "completed" || v.Status == "failed" {
		return nil
	}
	if actual == -1 {
		if v.Kind != "video" {
			return service.ErrMediaQuotaConflict
		}
		actual = v.Units
	}
	if actual > v.Units {
		if _, err = tx.ExecContext(ctx, `UPDATE organization_media_accounts SET blocked=TRUE WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE organization_media_reservations SET status='review',actual_units=$2,measurement=$3 WHERE id=$1`, v.ID, actual, measurement); err != nil {
			return err
		}
		return tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.lot_id,a.units FROM organization_media_allocations a JOIN organization_media_lots l ON l.id=a.lot_id JOIN organization_media_grants g ON g.id=l.grant_id WHERE a.reservation_id=$1 ORDER BY g.expires_at,a.lot_id`, v.ID)
	if err != nil {
		return err
	}
	type allocation struct{ id, units int64 }
	var allocations []allocation
	for rows.Next() {
		var a allocation
		if err = rows.Scan(&a.id, &a.units); err != nil {
			rows.Close()
			return err
		}
		allocations = append(allocations, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	remaining := actual
	for _, a := range allocations {
		used := a.units
		if used > remaining {
			used = remaining
		}
		remaining -= used
		if _, err = tx.ExecContext(ctx, `UPDATE organization_media_lots SET reserved=reserved-$2,consumed=consumed+$3 WHERE id=$1`, a.id, a.units, used); err != nil {
			return err
		}
	}
	status := "completed"
	if actual == 0 {
		status = "failed"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organization_media_reservations SET status=$2,actual_units=$3,measurement=$4,completed_at=NOW() WHERE id=$1`, v.ID, status, actual, measurement); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *organizationMediaQuotaRepository) FailVerifiedTask(ctx context.Context, taskID string) error {
	// The signed provider callback must also match an existing billed task owner.
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT b.user_id FROM organization_media_task_bindings b JOIN media_task_charges c ON c.task_id=b.task_id AND c.user_id=b.user_id WHERE b.task_id=$1`, taskID)
	if err != nil {
		return err
	}
	var users []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		users = append(users, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range users {
		if err = r.Settle(ctx, id, "", taskID, 0, "provider_failure"); err != nil {
			return err
		}
	}
	return nil
}

func (r *organizationMediaQuotaRepository) Summary(ctx context.Context, userID int64, page int) (*service.MediaQuotaSummary, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s := &service.MediaQuotaSummary{Grants: []service.MediaQuotaGrant{}, Items: []service.MediaQuotaReservation{}, UpdatedAt: time.Now().UTC()}
	err = tx.QueryRowContext(ctx, `SELECT version,blocked FROM organization_media_accounts WHERE user_id=$1`, userID).Scan(&s.Version, &s.Blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.Configured = true
	rows, err := tx.QueryContext(ctx, `SELECT l.kind,COALESCE(SUM(l.amount),0),COALESCE(SUM(l.reserved),0),COALESCE(SUM(l.consumed),0) FROM organization_media_lots l JOIN organization_media_grants g ON g.id=l.grant_id WHERE g.user_id=$1 AND g.effective_at<=NOW() AND g.expires_at>NOW() GROUP BY l.kind`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind string
		var b service.MediaQuotaBalance
		if err = rows.Scan(&kind, &b.Granted, &b.Reserved, &b.Consumed); err != nil {
			rows.Close()
			return nil, err
		}
		b.Available = b.Granted - b.Reserved - b.Consumed
		if kind == "image" {
			s.Images = b
		} else {
			s.Videos = b
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT g.request_id,g.contract_ref,g.reason,g.effective_at,g.expires_at,COALESCE(SUM(l.amount) FILTER(WHERE l.kind='image'),0),COALESCE(SUM(l.amount) FILTER(WHERE l.kind='video'),0),g.version-1 FROM organization_media_grants g LEFT JOIN organization_media_lots l ON g.id=l.grant_id WHERE g.user_id=$1 GROUP BY g.id ORDER BY g.created_at DESC LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g service.MediaQuotaGrant
		if err = rows.Scan(&g.RequestID, &g.ContractRef, &g.Reason, &g.EffectiveAt, &g.ExpiresAt, &g.Images, &g.VideoSeconds, &g.ExpectedVersion); err != nil {
			rows.Close()
			return nil, err
		}
		s.Grants = append(s.Grants, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	rows, err = tx.QueryContext(ctx, `SELECT `+mediaReservationColumns+` FROM organization_media_reservations WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT 51 OFFSET $2`, userID, (page-1)*50)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanMediaReservation(rows)
		if e != nil {
			return nil, e
		}
		s.Items = append(s.Items, *v)
	}
	s.HasMore = len(s.Items) > 50
	if s.HasMore {
		s.Items = s.Items[:50]
	}
	return s, rows.Err()
}
