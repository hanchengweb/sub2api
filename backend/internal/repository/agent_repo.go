package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type agentProfileRepository struct {
	db *sql.DB
}

// NewAgentProfileRepository 代理档案仓储。
func NewAgentProfileRepository(sqlDB *sql.DB) service.AgentProfileRepository {
	return &agentProfileRepository{db: sqlDB}
}

const agentProfileColumns = `user_id, application_id, mode, direction, status,
	pricing_plan_id, reseller_group_id, note, activated_at, created_at, updated_at`

func scanAgentProfile(row interface{ Scan(...any) error }) (service.AgentProfile, error) {
	var p service.AgentProfile
	err := row.Scan(&p.UserID, &p.ApplicationID, &p.Mode, &p.Direction, &p.Status,
		&p.PricingPlanID, &p.ResellerGroupID, &p.Note,
		&p.ActivatedAt, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// Upsert 按 user_id 落档。
//
// 冲突时不覆盖 activated_at：重复审核同一个人（或者他第二个方向的申请也通过了）
// 不该把首次生效时间抹掉——那是对外承诺的代理起始日期，也是结算周期的起点。
func (r *agentProfileRepository) Upsert(ctx context.Context, p *service.AgentProfile) error {
	if r == nil || r.db == nil {
		return errors.New("agent profile repository db is nil")
	}
	const q = `INSERT INTO agent_profiles
		(user_id, application_id, mode, direction, status, pricing_plan_id, reseller_group_id, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE SET
			application_id    = EXCLUDED.application_id,
			mode              = EXCLUDED.mode,
			direction         = EXCLUDED.direction,
			status            = EXCLUDED.status,
			pricing_plan_id   = EXCLUDED.pricing_plan_id,
			reseller_group_id = EXCLUDED.reseller_group_id,
			note              = EXCLUDED.note,
			updated_at        = NOW()
		RETURNING activated_at, created_at, updated_at`
	return r.db.QueryRowContext(ctx, q, p.UserID, p.ApplicationID, p.Mode, p.Direction,
		p.Status, p.PricingPlanID, p.ResellerGroupID, p.Note).
		Scan(&p.ActivatedAt, &p.CreatedAt, &p.UpdatedAt)
}

// GetByUserID 读一份档案。不是代理时回 ErrAgentProfileNotFound，
// 让调用方能把「不是代理」和「库挂了」分开处理。
func (r *agentProfileRepository) GetByUserID(ctx context.Context, userID int64) (*service.AgentProfile, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("agent profile repository db is nil")
	}
	const q = `SELECT ` + agentProfileColumns + ` FROM agent_profiles WHERE user_id = $1`
	p, err := scanAgentProfile(r.db.QueryRowContext(ctx, q, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAgentProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// List 管理员分页。status / mode 留空表示不筛选。
func (r *agentProfileRepository) List(ctx context.Context, filters service.AgentProfileFilters) ([]service.AgentProfile, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("agent profile repository db is nil")
	}
	// 两个筛选条件都用 ($n = '' OR col = $n) 的写法，避免拼 SQL。
	const countQ = `SELECT COUNT(*) FROM agent_profiles
		WHERE ($1 = '' OR status = $1) AND ($2 = '' OR mode = $2)`
	var total int
	if err := r.db.QueryRowContext(ctx, countQ, filters.Status, filters.Mode).Scan(&total); err != nil {
		return nil, 0, err
	}

	// 带上邮箱：后台只显示 user_id 的话运营认不出谁是谁，
	// 而停用代理这种操作靠数字 ID 对人是要出事的。
	// LEFT JOIN 而不是 INNER：用户被删了档案还在，那条记录更该被看见。
	const q = `SELECT a.user_id, a.application_id, a.mode, a.direction, a.status,
			a.pricing_plan_id, a.reseller_group_id, a.note, a.activated_at,
			a.created_at, a.updated_at, COALESCE(u.email, '')
		FROM agent_profiles a
		LEFT JOIN users u ON u.id = a.user_id
		WHERE ($1 = '' OR a.status = $1) AND ($2 = '' OR a.mode = $2)
		ORDER BY a.created_at DESC LIMIT $3 OFFSET $4`
	rows, err := r.db.QueryContext(ctx, q, filters.Status, filters.Mode, filters.Limit, filters.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AgentProfile, 0, filters.Limit)
	for rows.Next() {
		var p service.AgentProfile
		if err := rows.Scan(&p.UserID, &p.ApplicationID, &p.Mode, &p.Direction, &p.Status,
			&p.PricingPlanID, &p.ResellerGroupID, &p.Note,
			&p.ActivatedAt, &p.CreatedAt, &p.UpdatedAt, &p.Email); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

func (r *agentProfileRepository) UpdateStatus(ctx context.Context, userID int64, status, note string) error {
	if r == nil || r.db == nil {
		return errors.New("agent profile repository db is nil")
	}
	const q = `UPDATE agent_profiles SET status = $2, note = $3, updated_at = NOW()
		WHERE user_id = $1`
	res, err := r.db.ExecContext(ctx, q, userID, status, note)
	if err != nil {
		return err
	}
	return requireAgentRowAffected(res)
}

// UpdatePricing 改定价方案与专属分组。两个都允许置空（传 nil）：
// 把代理从批发价上摘下来是个正常运营动作，不该只能靠删档案实现。
func (r *agentProfileRepository) UpdatePricing(ctx context.Context, userID int64, planID, groupID *int64) error {
	if r == nil || r.db == nil {
		return errors.New("agent profile repository db is nil")
	}
	const q = `UPDATE agent_profiles
		SET pricing_plan_id = $2, reseller_group_id = $3, updated_at = NOW()
		WHERE user_id = $1`
	res, err := r.db.ExecContext(ctx, q, userID, planID, groupID)
	if err != nil {
		return err
	}
	return requireAgentRowAffected(res)
}

// requireAgentRowAffected 把「更新了 0 行」翻译成 not found。
//
// 不这么做的话，改一个不存在的代理会静默成功，管理员以为改好了。
func requireAgentRowAffected(res sql.Result) error {
	affected, err := res.RowsAffected()
	if err != nil {
		// 驱动不支持 RowsAffected 时不当作失败：语句本身已经执行成功了。
		return nil
	}
	if affected == 0 {
		return service.ErrAgentProfileNotFound
	}
	return nil
}

type agentPricingPlanRepository struct {
	db *sql.DB
}

// NewAgentPricingPlanRepository 代理定价方案仓储。
func NewAgentPricingPlanRepository(sqlDB *sql.DB) service.AgentPricingPlanRepository {
	return &agentPricingPlanRepository{db: sqlDB}
}

const agentPlanColumns = `id, name, text_discount, multimodal_deduction_cny,
	enforce_cost_floor, status, created_at, updated_at`

func scanAgentPricingPlan(row interface{ Scan(...any) error }) (service.AgentPricingPlan, error) {
	var p service.AgentPricingPlan
	err := row.Scan(&p.ID, &p.Name, &p.TextDiscount, &p.MultimodalDeductionCNY,
		&p.EnforceCostFloor, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (r *agentPricingPlanRepository) Create(ctx context.Context, p *service.AgentPricingPlan) error {
	if r == nil || r.db == nil {
		return errors.New("agent pricing plan repository db is nil")
	}
	const q = `INSERT INTO agent_pricing_plans
		(name, text_discount, multimodal_deduction_cny, enforce_cost_floor, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, updated_at`
	return r.db.QueryRowContext(ctx, q, p.Name, p.TextDiscount, p.MultimodalDeductionCNY,
		p.EnforceCostFloor, p.Status).
		Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *agentPricingPlanRepository) Update(ctx context.Context, p *service.AgentPricingPlan) error {
	if r == nil || r.db == nil {
		return errors.New("agent pricing plan repository db is nil")
	}
	const q = `UPDATE agent_pricing_plans SET
			name = $2, text_discount = $3, multimodal_deduction_cny = $4,
			enforce_cost_floor = $5, status = $6, updated_at = NOW()
		WHERE id = $1
		RETURNING created_at, updated_at`
	err := r.db.QueryRowContext(ctx, q, p.ID, p.Name, p.TextDiscount,
		p.MultimodalDeductionCNY, p.EnforceCostFloor, p.Status).
		Scan(&p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrAgentPricingPlanNotFound
	}
	return err
}

func (r *agentPricingPlanRepository) GetByID(ctx context.Context, id int64) (*service.AgentPricingPlan, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("agent pricing plan repository db is nil")
	}
	const q = `SELECT ` + agentPlanColumns + ` FROM agent_pricing_plans WHERE id = $1`
	p, err := scanAgentPricingPlan(r.db.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAgentPricingPlanNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// List 方案数量是个位数，不分页。archived 的也一并返回：
// 历史方案要能被看到，否则没法解释某个代理当初拿的是什么价。
func (r *agentPricingPlanRepository) List(ctx context.Context) ([]service.AgentPricingPlan, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("agent pricing plan repository db is nil")
	}
	const q = `SELECT ` + agentPlanColumns + ` FROM agent_pricing_plans ORDER BY id ASC`
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AgentPricingPlan, 0, 8)
	for rows.Next() {
		p, err := scanAgentPricingPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
