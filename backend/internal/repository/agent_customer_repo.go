package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type agentCustomerRepository struct {
	db *sql.DB
}

// NewAgentCustomerRepository 代理名下客户与用量的读取。
//
// 这个文件里每一条 SQL 都必须带 `a.inviter_id = $1`。
// 那不是筛选条件，是作用域根——漏掉任何一处，代理就能看到别人的客户。
func NewAgentCustomerRepository(sqlDB *sql.DB) service.AgentCustomerRepository {
	return &agentCustomerRepository{db: sqlDB}
}

// ListCustomers 代理名下的客户 + 消费汇总。
func (r *agentCustomerRepository) ListCustomers(ctx context.Context, agentUserID int64, limit, offset int) ([]service.AgentCustomer, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("agent customer repository db is nil")
	}
	const countQ = `SELECT COUNT(*) FROM user_affiliates a
		JOIN users u ON u.id = a.user_id
		WHERE a.inviter_id = $1 AND u.deleted_at IS NULL`
	var total int
	if err := r.db.QueryRowContext(ctx, countQ, agentUserID).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 汇总用 LATERAL 子查询而不是 GROUP BY 主表：客户数远小于用量条数，
	// 先定位客户再分别聚合，比把 usage_logs 整个拉进来分组便宜得多。
	const q = `
		SELECT u.id, u.email, u.status, a.created_at,
		       COALESCE(s.total_cost, 0), COALESCE(s.request_count, 0), s.last_active_at
		FROM user_affiliates a
		JOIN users u ON u.id = a.user_id
		LEFT JOIN LATERAL (
			SELECT SUM(l.actual_cost) AS total_cost,
			       COUNT(*)           AS request_count,
			       MAX(l.created_at)  AS last_active_at
			FROM usage_logs l WHERE l.user_id = a.user_id
		) s ON TRUE
		WHERE a.inviter_id = $1 AND u.deleted_at IS NULL
		ORDER BY a.created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryContext(ctx, q, agentUserID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AgentCustomer, 0, limit)
	for rows.Next() {
		var c service.AgentCustomer
		var lastActive sql.NullTime
		if err := rows.Scan(&c.UserID, &c.Email, &c.Status, &c.BoundAt,
			&c.TotalCostCredits, &c.RequestCount, &lastActive); err != nil {
			return nil, 0, err
		}
		if lastActive.Valid {
			t := lastActive.Time
			c.LastActiveAt = &t
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// ListUsage 代理名下客户的用量明细。
//
// 只选代理该看的列：没有 prompt、没有 token 明细、没有上游账号信息。
// 代理需要知道客户花了多少钱在什么模型上，不需要看到客户具体问了什么。
func (r *agentCustomerRepository) ListUsage(ctx context.Context, agentUserID int64, f service.AgentCustomerUsageFilters) ([]service.AgentCustomerUsage, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("agent customer repository db is nil")
	}
	// customer_id 来自请求参数，但这里同时要求 inviter_id 匹配——
	// 填别人的客户 id 进来查不到任何东西，不需要额外校验归属。
	const whereClause = `
		WHERE a.inviter_id = $1
		  AND ($2 = 0 OR l.user_id = $2)
		  AND ($3 = '' OR l.model = $3)`

	const countQ = `SELECT COUNT(*) FROM usage_logs l
		JOIN user_affiliates a ON a.user_id = l.user_id` + whereClause
	var total int
	if err := r.db.QueryRowContext(ctx, countQ, agentUserID, f.CustomerID, f.Model).Scan(&total); err != nil {
		return nil, 0, err
	}

	const q = `
		SELECT l.id, l.user_id, COALESCE(u.email, ''), l.model,
		       COALESCE(l.billing_mode, ''), COALESCE(l.actual_cost, 0),
		       COALESCE(l.image_count, 0), l.created_at
		FROM usage_logs l
		JOIN user_affiliates a ON a.user_id = l.user_id
		LEFT JOIN users u ON u.id = l.user_id` + whereClause + `
		ORDER BY l.id DESC
		LIMIT $4 OFFSET $5`
	rows, err := r.db.QueryContext(ctx, q, agentUserID, f.CustomerID, f.Model, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AgentCustomerUsage, 0, f.Limit)
	for rows.Next() {
		var u service.AgentCustomerUsage
		if err := rows.Scan(&u.ID, &u.CustomerID, &u.CustomerEmail, &u.Model,
			&u.BillingMode, &u.ActualCost, &u.ImageCount, &u.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}
