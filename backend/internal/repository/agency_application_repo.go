package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type agencyApplicationRepository struct {
	db *sql.DB
}

// NewAgencyApplicationRepository 合作申请仓储。
func NewAgencyApplicationRepository(sqlDB *sql.DB) service.AgencyApplicationRepository {
	return &agencyApplicationRepository{db: sqlDB}
}

const agencyColumns = `id, user_id, direction, contact_name, email, company, scenario, status, admin_note, created_at, updated_at`

func scanAgencyApplication(row interface{ Scan(...any) error }) (service.AgencyApplication, error) {
	var a service.AgencyApplication
	err := row.Scan(&a.ID, &a.UserID, &a.Direction, &a.ContactName, &a.Email,
		&a.Company, &a.Scenario, &a.Status, &a.AdminNote, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (r *agencyApplicationRepository) Create(ctx context.Context, app *service.AgencyApplication) error {
	if r == nil || r.db == nil {
		return errors.New("agency application repository db is nil")
	}
	const q = `INSERT INTO agency_applications
		(user_id, direction, contact_name, email, company, scenario, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, q, app.UserID, app.Direction, app.ContactName,
		app.Email, app.Company, app.Scenario, app.Status).
		Scan(&app.ID, &app.CreatedAt, &app.UpdatedAt)
}

// ListByUser 用户自己的申请。
//
// admin_note 也一并返回——那是平台给申请人的处理说明，本来就该让他看到；
// 真要藏内部备注，应该另开一列，而不是靠调用方记得别返回。
func (r *agencyApplicationRepository) ListByUser(ctx context.Context, userID int64, limit int) ([]service.AgencyApplication, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("agency application repository db is nil")
	}
	const q = `SELECT ` + agencyColumns + `
		FROM agency_applications WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2`
	rows, err := r.db.QueryContext(ctx, q, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AgencyApplication, 0, limit)
	for rows.Next() {
		a, err := scanAgencyApplication(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountPending 待处理总数 + 其中落在 direction 上的条数。
//
// 用 FILTER 在一条 SQL 里数两次：提交路径上多一次往返没有意义，
// 而且两次查询之间表可能已经变了，拿到的两个数对不上同一时刻。
func (r *agencyApplicationRepository) CountPending(ctx context.Context, userID int64, direction string) (int, int, error) {
	if r == nil || r.db == nil {
		return 0, 0, errors.New("agency application repository db is nil")
	}
	const q = `
		SELECT count(*), count(*) FILTER (WHERE direction = $2)
		FROM agency_applications
		WHERE user_id = $1 AND status = 'pending'`
	var total, sameDirection int
	err := r.db.QueryRowContext(ctx, q, userID, direction).Scan(&total, &sameDirection)
	return total, sameDirection, err
}

// List 管理员分页列表，同时返回总数用于翻页。
func (r *agencyApplicationRepository) List(ctx context.Context, f service.AgencyApplicationFilters) ([]service.AgencyApplication, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("agency application repository db is nil")
	}
	// status 为空表示不筛选。用 ($1 = '' OR status = $1) 而不是拼 SQL：
	// 拼字符串迟早会有人把用户输入接进去。
	const countQ = `SELECT count(*) FROM agency_applications WHERE ($1 = '' OR status = $1)`
	var total int
	if err := r.db.QueryRowContext(ctx, countQ, f.Status).Scan(&total); err != nil {
		return nil, 0, err
	}
	const q = `SELECT ` + agencyColumns + `
		FROM agency_applications
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryContext(ctx, q, f.Status, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AgencyApplication, 0, f.Limit)
	for rows.Next() {
		a, err := scanAgencyApplication(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (r *agencyApplicationRepository) UpdateStatus(ctx context.Context, id int64, status, note string) error {
	if r == nil || r.db == nil {
		return errors.New("agency application repository db is nil")
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE agency_applications SET status = $2, admin_note = $3, updated_at = NOW() WHERE id = $1`,
		id, status, note)
	return err
}
