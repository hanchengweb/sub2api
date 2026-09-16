package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 文本：客户花 100 积分在 deepseek 上，8 折口径 → 代理拿 20。
// 返的是「零售价与代理进货价的差」，不是消费额的全部。
func TestComputeRebateText(t *testing.T) {
	agg := &AgentUsageAggregate{TextCostCredits: 100}
	text, multi, err := ComputeRebate(agg, defaultPlan(), 100)
	require.NoError(t, err)
	require.InDelta(t, 20, text, 1e-9)
	require.InDelta(t, 0, multi, 1e-9)
}

// 多模态：按计费单元数 × ￥0.1 折算积分。
// ￥1 = 100 积分时，一个单元返 10 积分。
func TestComputeRebateMultimodal(t *testing.T) {
	agg := &AgentUsageAggregate{MultimodalUnits: 7}
	text, multi, err := ComputeRebate(agg, defaultPlan(), 100)
	require.NoError(t, err)
	require.InDelta(t, 0, text, 1e-9)
	require.InDelta(t, 70, multi, 1e-9, "7 个单元 × 10 积分")
}

// 汇率不是 100 时要跟着走：写死的话改汇率那天所有返现会静默变错。
func TestComputeRebateHonorsCreditRate(t *testing.T) {
	agg := &AgentUsageAggregate{MultimodalUnits: 3}
	_, multi, err := ComputeRebate(agg, defaultPlan(), 1000)
	require.NoError(t, err)
	require.InDelta(t, 300, multi, 1e-9, "￥0.1 × 1000 积分/元 × 3 个单元")
}

// 两类混在一起时各算各的，不能互相串。
func TestComputeRebateMixed(t *testing.T) {
	agg := &AgentUsageAggregate{TextCostCredits: 250, MultimodalUnits: 4}
	text, multi, err := ComputeRebate(agg, defaultPlan(), 100)
	require.NoError(t, err)
	require.InDelta(t, 50, text, 1e-9)
	require.InDelta(t, 40, multi, 1e-9)
}

// 折扣改成 9 折，代理就只拿 10%。口径变了返现要跟着变。
func TestComputeRebateFollowsPlanDiscount(t *testing.T) {
	plan := defaultPlan()
	plan.TextDiscount = 0.9
	agg := &AgentUsageAggregate{TextCostCredits: 100}
	text, _, err := ComputeRebate(agg, plan, 100)
	require.NoError(t, err)
	require.InDelta(t, 10, text, 1e-9)
}

// 折扣为 1（平价给代理）时返现是 0，不该出现负数。
func TestComputeRebateNoNegativeAtFullPrice(t *testing.T) {
	plan := defaultPlan()
	plan.TextDiscount = 1
	agg := &AgentUsageAggregate{TextCostCredits: 100}
	text, _, err := ComputeRebate(agg, plan, 100)
	require.NoError(t, err)
	require.InDelta(t, 0, text, 1e-9)
}

// 没有消费就没有返现，不该凭空算出金额。
func TestComputeRebateZeroUsage(t *testing.T) {
	text, multi, err := ComputeRebate(&AgentUsageAggregate{}, defaultPlan(), 100)
	require.NoError(t, err)
	require.InDelta(t, 0, text, 1e-9)
	require.InDelta(t, 0, multi, 1e-9)
}

// 汇率取不到时必须失败，不能用兜底值把错的钱算出来发出去。
func TestComputeRebateRejectsBadCreditRate(t *testing.T) {
	agg := &AgentUsageAggregate{TextCostCredits: 100}
	_, _, err := ComputeRebate(agg, defaultPlan(), 0)
	require.Error(t, err)

	_, _, err = ComputeRebate(agg, defaultPlan(), -100)
	require.Error(t, err)
}

// 方案本身不合法（折扣越界）时不能算，否则错账要真金白银发出去。
func TestComputeRebateRejectsInvalidPlan(t *testing.T) {
	plan := defaultPlan()
	plan.TextDiscount = 1.5
	_, _, err := ComputeRebate(&AgentUsageAggregate{TextCostCredits: 100}, plan, 100)
	require.ErrorIs(t, err, ErrAgentPricingPlanInvalid)
}

func TestComputeRebateRejectsNilInput(t *testing.T) {
	_, _, err := ComputeRebate(nil, defaultPlan(), 100)
	require.ErrorIs(t, err, ErrAgentSettlementInvalid)

	_, _, err = ComputeRebate(&AgentUsageAggregate{}, nil, 100)
	require.ErrorIs(t, err, ErrAgentSettlementInvalid)
}

// 生产口径对照：文本 8 折、多模态每次一毛、￥1=100 积分。
// 客户在 deepseek 上花 500 积分（￥5），另外生成了 12 张图。
func TestComputeRebateProductionScenario(t *testing.T) {
	agg := &AgentUsageAggregate{TextCostCredits: 500, MultimodalUnits: 12}
	text, multi, err := ComputeRebate(agg, defaultPlan(), 100)
	require.NoError(t, err)
	require.InDelta(t, 100, text, 1e-9, "￥5 的文本消费返 ￥1")
	require.InDelta(t, 120, multi, 1e-9, "12 张图每张让一毛 = ￥1.2")
	require.InDelta(t, 220, text+multi, 1e-9, "合计 ￥2.2")
}
