package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func agentPtrFloat(v float64) *float64 { return &v }

// defaultPlan 是 2026-09-16 定的口径：文本八折 / 多模态每次让一毛。
func defaultPlan() *AgentPricingPlan {
	return &AgentPricingPlan{
		ID:                     1,
		Name:                   "默认代理方案",
		TextDiscount:           0.8,
		MultimodalDeductionCNY: 0.1,
		EnforceCostFloor:       true,
		Status:                 AgentPricingPlanActive,
	}
}

// 计费模式是模型类别的唯一依据，别处不该再维护一张分类表。
func TestCategoryForBillingMode(t *testing.T) {
	require.Equal(t, AgentCategoryText, CategoryForBillingMode(BillingModeToken))
	require.Equal(t, AgentCategoryText, CategoryForBillingMode(""), "空值按 token 处理，与计费侧一致")
	require.Equal(t, AgentCategoryMultimodal, CategoryForBillingMode(BillingModePerRequest))
	require.Equal(t, AgentCategoryMultimodal, CategoryForBillingMode(BillingModeImage))
	require.Equal(t, AgentCategoryMultimodal, CategoryForBillingMode(BillingModeVideo))
}

// 文本类：每个价格字段都要跟着打折。
// 只折输入价不折输出价的话，代理在输出密集的场景下拿不到说好的八折。
func TestBuildPreviewTextAppliesDiscountToEveryField(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-opus-4-6"},
		BillingMode: BillingModeToken,
		InputPrice:  agentPtrFloat(0.001),
		OutputPrice: agentPtrFloat(0.005),
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 2)

	byField := map[string]AgentPriceChange{}
	for _, c := range preview.Changes {
		byField[c.Field] = c
	}
	require.InDelta(t, 0.0008, byField["input"].AgentPrice, 1e-12)
	require.InDelta(t, 0.004, byField["output"].AgentPrice, 1e-12)
	require.Equal(t, AgentCategoryText, byField["input"].Category)
}

// 多模态：减固定金额，且金额要按积分汇率换算。
// ￥0.1 在「每元 100 积分」下是 10 积分——这一步取错的话，
// 「让一毛」会变成「让一分」或者「让一块」，而数字本身看不出问题。
func TestBuildPreviewMultimodalDeductsConvertedCredits(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:        "openai",
		Models:          []string{"gpt-image-2"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: agentPtrFloat(50),
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.InDelta(t, 10, preview.DeductionCredits, 1e-9, "￥0.1 × 100 积分/元 = 10 积分")

	require.Len(t, preview.Changes, 1)
	require.InDelta(t, 40, preview.Changes[0].AgentPrice, 1e-9, "50 积分零售价减 10 积分让利")
	require.Equal(t, AgentCategoryMultimodal, preview.Changes[0].Category)
}

// 汇率不是 100 时也得跟着走：这个值是可配置的（BALANCE_RECHARGE_MULTIPLIER），
// 写死 100 的话改汇率那天所有代理价会静默变错。
func TestBuildPreviewHonorsNonDefaultCreditRate(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:        "openai",
		Models:          []string{"sora-2"},
		BillingMode:     BillingModeVideo,
		PerRequestPrice: agentPtrFloat(500),
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 1000, nil)
	require.NoError(t, err)
	require.InDelta(t, 100, preview.DeductionCredits, 1e-9, "￥0.1 × 1000 积分/元 = 100 积分")
	require.InDelta(t, 400, preview.Changes[0].AgentPrice, 1e-9)
}

// 零售价低于让利金额：按方案算出来是负价，等于平台倒贴还要送钱。
// 退回零售价而不是写 0（0 是免费送），也不是不写（不写会回退 LiteLLM 官方价）。
func TestBuildPreviewSkipsWhenDeductionExceedsRetail(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:        "openai",
		Models:          []string{"cheap-tts"},
		BillingMode:     BillingModePerRequest,
		PerRequestPrice: agentPtrFloat(6), // 6 积分 = ￥0.06，比一毛还便宜
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	require.True(t, preview.Changes[0].Skipped)
	require.NotEmpty(t, preview.Changes[0].Warning)
	require.Equal(t, 1, preview.SkippedRows)
	require.InDelta(t, 6, preview.Changes[0].AgentPrice, 1e-9, "跳过的条目退回零售价")
	require.InDelta(t, 6, *preview.Rows[0].PerRequestPrice, 1e-9, "落库的行也必须是零售价，不能留空")
}

// 分层定价必须一起折。生产上 10 条定价配了 54 个层级，实际计费命中的是层级价：
// 只折行级默认价的话，代理照零售层级价被扣费，一分钱折扣都拿不到。
func TestBuildPreviewAppliesToIntervals(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:        "openai",
		Models:          []string{"t-gpt-image-2-vip"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: agentPtrFloat(32.68),
		Intervals: []PricingInterval{
			{TierLabel: "1K·低", PerRequestPrice: agentPtrFloat(29.33)},
			{TierLabel: "4K·高", PerRequestPrice: agentPtrFloat(161)},
		},
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 3, "行级 1 条 + 分层 2 条")

	require.Len(t, preview.Rows, 1)
	require.InDelta(t, 22.68, *preview.Rows[0].PerRequestPrice, 1e-9)
	require.InDelta(t, 19.33, *preview.Rows[0].Intervals[0].PerRequestPrice, 1e-9)
	require.InDelta(t, 151, *preview.Rows[0].Intervals[1].PerRequestPrice, 1e-9)

	byTier := map[string]AgentPriceChange{}
	for _, c := range preview.Changes {
		byTier[c.TierLabel] = c
	}
	require.Contains(t, byTier, "1K·低")
	require.Contains(t, byTier, "4K·高")
}

// 只有分层价、行级价为 NULL 的定价（生产的 nano_banana_2 就是这样）
// 也必须拿到折扣，否则它一条都不会被处理。
func TestBuildPreviewHandlesIntervalOnlyPricing(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:    "gemini",
		Models:      []string{"nano_banana_2"},
		BillingMode: BillingModeImage,
		Intervals: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: agentPtrFloat(50)},
			{TierLabel: "4K", PerRequestPrice: agentPtrFloat(71)},
		},
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 2)
	require.Nil(t, preview.Rows[0].PerRequestPrice, "行级价本来就是 NULL，不该被凭空造出来")
	require.InDelta(t, 40, *preview.Rows[0].Intervals[0].PerRequestPrice, 1e-9)
	require.InDelta(t, 61, *preview.Rows[0].Intervals[1].PerRequestPrice, 1e-9)
}

// 生成器不能改到调用方手里的零售价——那是从渠道缓存里拿出来的对象，
// 被就地改掉的话，全站零售价会跟着变成代理价。
func TestBuildPreviewDoesNotMutateRetailInput(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:        "openai",
		Models:          []string{"gpt-image-2"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: agentPtrFloat(46.5),
		Intervals: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: agentPtrFloat(39.5)},
		},
	}}

	_, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.InDelta(t, 46.5, *retail[0].PerRequestPrice, 1e-9, "零售价被就地改写了")
	require.InDelta(t, 39.5, *retail[0].Intervals[0].PerRequestPrice, 1e-9, "零售分层价被就地改写了")
}

// 文本类的 token 区间也要折，且没有 tier_label 时用区间边界做标识，
// 否则预览里几条会长得一模一样，运营分不出改的是哪一段。
func TestBuildPreviewLabelsTokenIntervalsByRange(t *testing.T) {
	maxTokens := 200000
	retail := []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"deepseek-v4-flash"},
		BillingMode: BillingModeToken,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: &maxTokens, InputPrice: agentPtrFloat(0.001)},
			{MinTokens: 200000, InputPrice: agentPtrFloat(0.002)},
		},
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 2)

	tiers := []string{preview.Changes[0].TierLabel, preview.Changes[1].TierLabel}
	require.Contains(t, tiers, "0-200000")
	require.Contains(t, tiers, "200000+")
	require.InDelta(t, 0.0008, *preview.Rows[0].Intervals[0].InputPrice, 1e-12)
	require.InDelta(t, 0.0016, *preview.Rows[0].Intervals[1].InputPrice, 1e-12)
}

// 成本下限：算出来低于成本就别生成，否则代理每调一次平台亏一次。
func TestBuildPreviewEnforcesCostFloor(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-haiku-4-5"},
		BillingMode: BillingModeToken,
		InputPrice:  agentPtrFloat(0.001),
	}}
	// 成本 0.0009 > 八折后的 0.0008。
	cost := func(string, string, string) (float64, bool) { return 0.0009, true }

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, cost)
	require.NoError(t, err)
	require.True(t, preview.Changes[0].Skipped)
	require.NotNil(t, preview.Changes[0].CostPrice)
	require.Contains(t, preview.Changes[0].Warning, "低于成本")
}

// 关掉保护时仍然告警：明知故犯和不知情是两回事，但都得让人看见。
func TestBuildPreviewWarnsButKeepsRowWhenFloorDisabled(t *testing.T) {
	plan := defaultPlan()
	plan.EnforceCostFloor = false
	retail := []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-haiku-4-5"},
		BillingMode: BillingModeToken,
		InputPrice:  agentPtrFloat(0.001),
	}}
	cost := func(string, string, string) (float64, bool) { return 0.0009, true }

	preview, err := BuildAgentPricingPreview(plan, retail, 100, cost)
	require.NoError(t, err)
	require.False(t, preview.Changes[0].Skipped)
	require.NotEmpty(t, preview.Changes[0].Warning)
	require.Equal(t, 0, preview.SkippedRows)
	require.Equal(t, 1, preview.WarningRows)
}

// 成本查不到时要说出来，不能静默当作「通过了保护」。
func TestBuildPreviewWarnsWhenCostUnknown(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"unpriced-model"},
		BillingMode: BillingModeToken,
		InputPrice:  agentPtrFloat(0.001),
	}}
	missing := func(string, string, string) (float64, bool) { return 0, false }

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, missing)
	require.NoError(t, err)
	require.Contains(t, preview.Changes[0].Warning, "成本表缺该模型")
	require.False(t, preview.Changes[0].Skipped, "查不到成本不等于亏本，不该直接拦掉")
}

// 没配价的字段不该凭空生成一条 0 价。
func TestBuildPreviewSkipsNilPrices(t *testing.T) {
	retail := []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-opus-4-6"},
		BillingMode: BillingModeToken,
		InputPrice:  agentPtrFloat(0.001),
		// OutputPrice 等字段为 nil：表示沿用默认定价，不是 0。
	}}

	preview, err := BuildAgentPricingPreview(defaultPlan(), retail, 100, nil)
	require.NoError(t, err)
	require.Len(t, preview.Changes, 1)
	require.Equal(t, "input", preview.Changes[0].Field)
}

func TestPricingPlanValidateRejectsBadDiscount(t *testing.T) {
	// 大于 1 是给代理加价，几乎一定是把「加价 20%」和「打八折」搞混了。
	over := defaultPlan()
	over.TextDiscount = 1.2
	require.ErrorIs(t, over.Validate(), ErrAgentPricingPlanInvalid)

	zero := defaultPlan()
	zero.TextDiscount = 0
	require.ErrorIs(t, zero.Validate(), ErrAgentPricingPlanInvalid)

	negative := defaultPlan()
	negative.MultimodalDeductionCNY = -0.1
	require.ErrorIs(t, negative.Validate(), ErrAgentPricingPlanInvalid)

	require.NoError(t, defaultPlan().Validate())
}

// 汇率取不到（0 或负数）时必须失败，不能用一个兜底值把错的价算出来。
func TestBuildPreviewRejectsInvalidCreditRate(t *testing.T) {
	_, err := BuildAgentPricingPreview(defaultPlan(), nil, 0, nil)
	require.Error(t, err)

	_, err = BuildAgentPricingPreview(defaultPlan(), nil, -100, nil)
	require.Error(t, err)
}
