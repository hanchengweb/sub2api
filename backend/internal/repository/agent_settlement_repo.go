package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type agentSettlementRepository struct {
	db *sql.DB
}

// NewAgentSettlementRepository 代理返现结算仓储。
func NewAgentSettlementRepository(sqlDB *sql.DB) service.AgentSettlementRepository {
	return &agentSettlementRepository{db: sqlDB}
}

func (r *agentSettlementRepository) LastSettledUsageLogID(ctx context.Context, agentUserID int64) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("agent settlement repository db is nil")
	}
	// 只看 settled 的：被冲正的批次不该挡住重新结算那段区间。
	const q = `SELECT COALESCE(MAX(to_usage_log_id), 0) FROM agent_settlements
		WHERE agent_user_id = $1 AND status = 'settled'`
	var last int64
	err := r.db.QueryRowContext(ctx, q, agentUserID).Scan(&last)
	return last, err
}

// AggregateUsage 聚合该代理名下所有客户的消费。
//
// 归属关系走 user_affiliates.inviter_id——客户通过代理的邀请链接注册时
// 一次性锁定，事后不变。
//
// 区间是左开右闭 (afterUsageLogID, MAX(id)]：上一批已经算过 afterUsageLogID
// 那条了，再算一次就是重复发钱。
func (r *agentSettlementRepository) AggregateUsage(ctx context.Context, agentUserID, afterUsageLogID int64) (*service.AgentUsageAggregate, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("agent settlement repository db is nil")
	}
	// billing_mode 为空按 token 处理，与计费侧 CategoryForBillingMode 一致。
	//
	// 多模态取 GREATEST(image_count, 1)：视频和按次计费的条目 image_count
	// 可能是 0，按 0 算等于这些调用白送给代理，代理一分返现拿不到。
	//
	// actual_cost 是客户实际被扣的金额（已含分组倍率），不是 total_cost——
	// 返现要按客户真付了多少算，不是按牌价算。
	const q = `
		SELECT
			COALESCE(SUM(u.actual_cost) FILTER (
				WHERE u.billing_mode IS NULL OR u.billing_mode = '' OR u.billing_mode = 'token'), 0) AS text_cost,
			COALESCE(SUM(GREATEST(COALESCE(u.image_count, 1), 1)) FILTER (
				WHERE u.billing_mode IS NOT NULL AND u.billing_mode <> '' AND u.billing_mode <> 'token'), 0) AS multimodal_units,
			COUNT(DISTINCT u.user_id) AS customer_count,
			COALESCE(MAX(u.id), $2) AS max_id,
			COALESCE(MIN(u.created_at), NOW()) AS period_start,
			COALESCE(MAX(u.created_at), NOW()) AS period_end
		FROM usage_logs u
		JOIN user_affiliates a ON a.user_id = u.user_id
		WHERE a.inviter_id = $1 AND u.id > $2`
	var agg service.AgentUsageAggregate
	err := r.db.QueryRowContext(ctx, q, agentUserID, afterUsageLogID).Scan(
		&agg.TextCostCredits, &agg.MultimodalUnits, &agg.CustomerCount,
		&agg.MaxUsageLogID, &agg.PeriodStart, &agg.PeriodEnd)
	if err != nil {
		return nil, err
	}
	return &agg, nil
}

// Create 落结算批次并把返现累加到代理的 aff_quota。
//
// 两件事在同一个事务里：只落批次不加额度，代理白干一场；
// 只加额度不落批次，游标没前进，下次重跑会再发一遍钱。
func (r *agentSettlementRepository) Create(ctx context.Context, s *service.AgentSettlement) error {
	if r == nil || r.db == nil {
		return errors.New("agent settlement repository db is nil")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	const insertQ = `INSERT INTO agent_settlements
		(agent_user_id, from_usage_log_id, to_usage_log_id, period_start, period_end,
		 text_cost_credits, text_rebate_credits, multimodal_units, multimodal_rebate_credits,
		 total_rebate_credits, text_discount, multimodal_deduction_cny, credits_per_cny,
		 customer_count, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, created_at, updated_at`
	if err := tx.QueryRowContext(ctx, insertQ,
		s.AgentUserID, s.FromUsageLogID, s.ToUsageLogID, s.PeriodStart, s.PeriodEnd,
		s.TextCostCredits, s.TextRebateCredits, s.MultimodalUnits, s.MultimodalRebateCredits,
		s.TotalRebateCredits, s.TextDiscount, s.MultimodalDeductionCNY, s.CreditsPerCNY,
		s.CustomerCount, s.Status,
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return err
	}

	// 返现落到 aff_quota，代理用现成的「转余额」把它变成可用积分。
	// 零额批次也照落（留下"这段时间没产生返现"的记录），但不动额度。
	if s.TotalRebateCredits > 0 {
		const quotaQ = `UPDATE user_affiliates
			SET aff_quota = aff_quota + $2,
			    aff_history_quota = aff_history_quota + $2,
			    updated_at = NOW()
			WHERE user_id = $1`
		res, err := tx.ExecContext(ctx, quotaQ, s.AgentUserID, s.TotalRebateCredits)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		// 代理还没有 user_affiliates 行（从没生成过邀请码）时更新不到任何行。
		// 静默放过的话钱就凭空蒸发了，必须让这次结算整个失败。
		if err == nil && affected == 0 {
			return errors.New("agent has no affiliate profile to credit")
		}
	}
	return tx.Commit()
}

const agentSettlementColumns = `id, agent_user_id, from_usage_log_id, to_usage_log_id,
	period_start, period_end, text_cost_credits, text_rebate_credits,
	multimodal_units, multimodal_rebate_credits, total_rebate_credits,
	text_discount, multimodal_deduction_cny, credits_per_cny,
	customer_count, status, created_at, updated_at`

func (r *agentSettlementRepository) ListByAgent(ctx context.Context, agentUserID int64, limit, offset int) ([]service.AgentSettlement, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("agent settlement repository db is nil")
	}
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_settlements WHERE agent_user_id = $1`, agentUserID).Scan(&total); err != nil {
		return nil, 0, err
	}

	const q = `SELECT ` + agentSettlementColumns + ` FROM agent_settlements
		WHERE agent_user_id = $1 ORDER BY id DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.QueryContext(ctx, q, agentUserID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.AgentSettlement, 0, limit)
	for rows.Next() {
		var s service.AgentSettlement
		if err := rows.Scan(&s.ID, &s.AgentUserID, &s.FromUsageLogID, &s.ToUsageLogID,
			&s.PeriodStart, &s.PeriodEnd, &s.TextCostCredits, &s.TextRebateCredits,
			&s.MultimodalUnits, &s.MultimodalRebateCredits, &s.TotalRebateCredits,
			&s.TextDiscount, &s.MultimodalDeductionCNY, &s.CreditsPerCNY,
			&s.CustomerCount, &s.Status, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}
