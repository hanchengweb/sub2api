package service

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrAgentSettlementInvalid 结算入参不合法。
	ErrAgentSettlementInvalid = errors.New("invalid agent settlement")
	// ErrAgentSettlementNothingToDo 区间内没有可结算的消费。
	ErrAgentSettlementNothingToDo = errors.New("nothing to settle")
)

// AgentUsageAggregate 一个代理名下客户在某个区间的消费聚合。
//
// 按 billing_mode 分成两类，因为两类的返现规则不同：
// 文本按金额比例，多模态按计费单元数乘固定让利。
type AgentUsageAggregate struct {
	// TextCostCredits 文本类（billing_mode 为 token 或空）的消费额，积分。
	TextCostCredits float64
	// MultimodalUnits 多模态的计费单元数。
	//
	// 取 image_count 之和而不是请求条数：批发价那边让利是减在单价上的
	// （一次生成 4 张图就让 4 毛），按条数算两套口径在多图请求上会对不上。
	MultimodalUnits int64
	// CustomerCount 本区间内有消费的客户数。
	CustomerCount int
	// MaxUsageLogID 本次聚合覆盖到的最大 usage_logs.id，作为下次的起点。
	MaxUsageLogID int64
	PeriodStart   time.Time
	PeriodEnd     time.Time
}

// AgentSettlement 一次结算的结果。
type AgentSettlement struct {
	ID          int64 `json:"id"`
	AgentUserID int64 `json:"agent_user_id"`

	FromUsageLogID int64     `json:"from_usage_log_id"`
	ToUsageLogID   int64     `json:"to_usage_log_id"`
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`

	TextCostCredits         float64 `json:"text_cost_credits"`
	TextRebateCredits       float64 `json:"text_rebate_credits"`
	MultimodalUnits         int64   `json:"multimodal_units"`
	MultimodalRebateCredits float64 `json:"multimodal_rebate_credits"`
	TotalRebateCredits      float64 `json:"total_rebate_credits"`

	// 规则快照。方案随时可改，历史结算必须仍然解释得清当时按什么算的。
	TextDiscount           float64 `json:"text_discount"`
	MultimodalDeductionCNY float64 `json:"multimodal_deduction_cny"`
	CreditsPerCNY          float64 `json:"credits_per_cny"`

	CustomerCount int       `json:"customer_count"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// AgentSettlementRepository 结算批次的存取与消费聚合。
type AgentSettlementRepository interface {
	// LastSettledUsageLogID 该代理上次结算到的 usage_logs.id；没结算过返回 0。
	LastSettledUsageLogID(ctx context.Context, agentUserID int64) (int64, error)
	// AggregateUsage 聚合该代理名下所有客户在 (afterUsageLogID, maxID] 区间的消费。
	AggregateUsage(ctx context.Context, agentUserID, afterUsageLogID int64) (*AgentUsageAggregate, error)
	// Create 落一条结算批次，并把返现累加到代理的 aff_quota。
	// 两件事必须在同一个事务里：只落批次不加额度，代理白干；
	// 只加额度不落批次，下次重跑会再加一遍。
	Create(ctx context.Context, s *AgentSettlement) error
	ListByAgent(ctx context.Context, agentUserID int64, limit, offset int) ([]AgentSettlement, int, error)
}

// AgentSettlementService 代理返现结算。
type AgentSettlementService struct {
	repo   AgentSettlementRepository
	agents *AgentService
	plans  *AgentPricingPlanService
}

func NewAgentSettlementService(
	repo AgentSettlementRepository,
	agents *AgentService,
	plans *AgentPricingPlanService,
) *AgentSettlementService {
	return &AgentSettlementService{repo: repo, agents: agents, plans: plans}
}

// ComputeRebate 按规则算返现。纯函数，便于核对账目。
//
//	文本：消费额 × (1 - text_discount)
//	  客户花 100 积分在 deepseek 上，text_discount=0.8 → 返 20 积分。
//	  口径是「代理按 8 折拿货」，那 2 折的差价就是他的。
//	多模态：计费单元数 × 每单元让利积分
//	  让利金额按人民币配（￥0.1），乘积分汇率折算。
func ComputeRebate(agg *AgentUsageAggregate, plan *AgentPricingPlan, creditsPerCNY float64) (textRebate, multimodalRebate float64, err error) {
	if agg == nil || plan == nil {
		return 0, 0, ErrAgentSettlementInvalid
	}
	if err := plan.Validate(); err != nil {
		return 0, 0, err
	}
	if creditsPerCNY <= 0 {
		return 0, 0, fmt.Errorf("invalid credits per CNY rate: %v", creditsPerCNY)
	}

	// 文本：折扣是「代理付多少」，返现是剩下那部分。
	textRebate = roundTo(agg.TextCostCredits*(1-plan.TextDiscount), 8)
	if textRebate < 0 {
		textRebate = 0
	}

	multimodalRebate = roundTo(float64(agg.MultimodalUnits)*plan.MultimodalDeductionCNY*creditsPerCNY, 8)
	if multimodalRebate < 0 {
		multimodalRebate = 0
	}
	return textRebate, multimodalRebate, nil
}

// SettleAgent 给一个代理结算一次。
//
// creditsPerCNY 由调用方从 BALANCE_RECHARGE_MULTIPLIER 取并显式传入，
// 并且会连同规则一起存进批次快照——取错了算出来的钱看不出问题，
// 只有事后对账才会发现，那时候额度已经发出去了。
func (s *AgentSettlementService) SettleAgent(
	ctx context.Context,
	agentUserID int64,
	creditsPerCNY float64,
) (*AgentSettlement, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("agent settlement service is unavailable")
	}
	if agentUserID <= 0 {
		return nil, ErrAgentSettlementInvalid
	}

	// 停用的代理不再产生新返现。已结算的额度不动——那是他之前挣到的。
	active, profile, err := s.agents.IsActiveAgent(ctx, agentUserID)
	if err != nil {
		return nil, err
	}
	if !active || profile == nil {
		return nil, ErrAgentProfileNotFound
	}

	plan, err := s.resolvePlan(ctx, profile)
	if err != nil {
		return nil, err
	}

	lastID, err := s.repo.LastSettledUsageLogID(ctx, agentUserID)
	if err != nil {
		return nil, err
	}
	agg, err := s.repo.AggregateUsage(ctx, agentUserID, lastID)
	if err != nil {
		return nil, err
	}
	// 没有新消费就不落空批次：一堆零额批次会把结算历史刷得没法看。
	if agg == nil || agg.MaxUsageLogID <= lastID {
		return nil, ErrAgentSettlementNothingToDo
	}

	textRebate, multimodalRebate, err := ComputeRebate(agg, plan, creditsPerCNY)
	if err != nil {
		return nil, err
	}

	settlement := &AgentSettlement{
		AgentUserID:             agentUserID,
		FromUsageLogID:          lastID,
		ToUsageLogID:            agg.MaxUsageLogID,
		PeriodStart:             agg.PeriodStart,
		PeriodEnd:               agg.PeriodEnd,
		TextCostCredits:         agg.TextCostCredits,
		TextRebateCredits:       textRebate,
		MultimodalUnits:         agg.MultimodalUnits,
		MultimodalRebateCredits: multimodalRebate,
		TotalRebateCredits:      roundTo(textRebate+multimodalRebate, 8),
		TextDiscount:            plan.TextDiscount,
		MultimodalDeductionCNY:  plan.MultimodalDeductionCNY,
		CreditsPerCNY:           creditsPerCNY,
		CustomerCount:           agg.CustomerCount,
		Status:                  "settled",
	}
	if err := s.repo.Create(ctx, settlement); err != nil {
		return nil, err
	}
	return settlement, nil
}

// resolvePlan 取该代理适用的返现规则。
//
// 代理没单独指定方案时用默认方案（id=1，迁移 235 种下的）。
// 取不到就报错而不是用内置默认值兜底：兜底会让「方案配错了」
// 变成一次静默的错账，而错账是要真金白银发出去的。
func (s *AgentSettlementService) resolvePlan(ctx context.Context, profile *AgentProfile) (*AgentPricingPlan, error) {
	if s.plans == nil {
		return nil, errors.New("agent pricing plan service is unavailable")
	}
	planID := int64(1)
	if profile.PricingPlanID != nil && *profile.PricingPlanID > 0 {
		planID = *profile.PricingPlanID
	}
	plan, err := s.plans.Get(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("resolve pricing plan %d: %w", planID, err)
	}
	if plan.Status != AgentPricingPlanActive {
		return nil, fmt.Errorf("pricing plan %d is %s", planID, plan.Status)
	}
	return plan, nil
}

// ListByAgent 代理查自己的结算历史。
func (s *AgentSettlementService) ListByAgent(ctx context.Context, agentUserID int64, limit, offset int) ([]AgentSettlement, int, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("agent settlement service is unavailable")
	}
	if agentUserID <= 0 {
		return nil, 0, ErrAgentSettlementInvalid
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListByAgent(ctx, agentUserID, limit, offset)
}
