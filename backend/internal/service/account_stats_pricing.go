package service

import (
	"context"
	"strings"
)

// mediaStatsContext 媒体请求的成本计算上下文。
//
// 按次/图片模式下，成本不是一个数：图片分 1K/2K/4K 档，视频按秒。
// 不传这些就只能给每个模型配一个扁平值，4K 图会少算、短视频会多算。
type mediaStatsContext struct {
	SizeTier        string // 图片档位标签（1K/2K/4K），空则不按档查
	DurationSeconds int    // 视频时长（秒）；>0 时按秒计费，与客户侧 CalculateVideoCost 口径一致
}

// resolveAccountStatsCost 计算账号统计定价费用。
// 返回 nil 表示不覆盖，使用默认公式（total_cost × account_rate_multiplier）。
//
// 优先级（先命中为准）：
//  1. 自定义规则（始终尝试，不依赖 ApplyPricingToAccountStats 开关）
//  2. ApplyPricingToAccountStats 启用时，直接使用本次请求的客户计费（倍率前的 totalCost）
//  3. 模型定价文件（LiteLLM）中上游模型的默认价格
//  4. nil → 走默认公式（total_cost × account_rate_multiplier）
//
// upstreamModel 是最终发往上游的模型 ID。
// totalCost 是本次请求的客户计费（倍率前），用于优先级 2。
func resolveAccountStatsCost(
	ctx context.Context,
	channelService *ChannelService,
	billingService *BillingService,
	accountID int64,
	groupID int64,
	upstreamModel string,
	tokens UsageTokens,
	requestCount int,
	totalCost float64,
	media mediaStatsContext,
) *float64 {
	if channelService == nil || upstreamModel == "" {
		return nil
	}
	channel, err := channelService.GetChannelForGroup(ctx, groupID)
	if err != nil || channel == nil {
		return nil
	}

	platform := channelService.GetGroupPlatform(ctx, groupID)

	// 优先级 1：自定义规则（始终尝试）
	if cost := tryCustomRules(channel, accountID, groupID, platform, upstreamModel, tokens, requestCount, media); cost != nil {
		return cost
	}

	// 优先级 2：渠道开启"应用模型定价到账号统计"时，直接使用客户计费（倍率前）
	if channel.ApplyPricingToAccountStats {
		cost := totalCost
		if cost <= 0 {
			return nil
		}
		return &cost
	}

	// 优先级 3：模型定价文件（LiteLLM）默认价格
	if billingService != nil {
		return tryModelFilePricing(billingService, upstreamModel, tokens)
	}

	return nil
}

// tryModelFilePricing 使用模型定价文件（LiteLLM/fallback）中的标准价格计算费用。
func tryModelFilePricing(billingService *BillingService, model string, tokens UsageTokens) *float64 {
	pricing, err := billingService.GetModelPricing(model)
	if err != nil || pricing == nil {
		return nil
	}
	if billingService.shouldApplySessionLongContextPricing(tokens, pricing) {
		breakdown, err := billingService.CalculateCost(model, tokens, 1)
		if err != nil || breakdown == nil || breakdown.TotalCost <= 0 {
			return nil
		}
		return &breakdown.TotalCost
	}
	cost := float64(tokens.InputTokens)*pricing.InputPricePerToken +
		float64(tokens.OutputTokens)*pricing.OutputPricePerToken +
		float64(tokens.CacheCreationTokens)*pricing.CacheCreationPricePerToken +
		float64(tokens.CacheReadTokens)*pricing.CacheReadPricePerToken +
		float64(tokens.ImageOutputTokens)*pricing.ImageOutputPricePerToken
	if cost <= 0 {
		return nil
	}
	return &cost
}

// tryCustomRules 遍历自定义规则，按数组顺序先命中为准。
func tryCustomRules(
	channel *Channel, accountID, groupID int64,
	platform, model string, tokens UsageTokens, requestCount int,
	media mediaStatsContext,
) *float64 {
	modelLower := strings.ToLower(model)
	for _, rule := range channel.AccountStatsPricingRules {
		if !matchAccountStatsRule(&rule, accountID, groupID) {
			continue
		}
		pricing := findPricingForModel(rule.Pricing, platform, modelLower)
		if pricing == nil {
			continue // 规则匹配但模型不在规则定价中，继续下一条
		}
		return calculateStatsCost(pricing, tokens, requestCount, media)
	}
	return nil
}

// matchAccountStatsRule 检查规则是否匹配指定的 accountID 和 groupID。
// 匹配条件：accountID ∈ rule.AccountIDs 或 groupID ∈ rule.GroupIDs。
// 如果规则的 AccountIDs 和 GroupIDs 都为空，视为不匹配。
func matchAccountStatsRule(rule *AccountStatsPricingRule, accountID, groupID int64) bool {
	if len(rule.AccountIDs) == 0 && len(rule.GroupIDs) == 0 {
		return false
	}
	for _, id := range rule.AccountIDs {
		if id == accountID {
			return true
		}
	}
	for _, id := range rule.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// findPricingForModel 在定价列表中查找匹配的模型定价。
// 先精确匹配，再通配符匹配（按配置顺序，先匹配先使用）。
func findPricingForModel(pricingList []ChannelModelPricing, platform, modelLower string) *ChannelModelPricing {
	// 精确匹配优先
	for i := range pricingList {
		p := &pricingList[i]
		if !isPlatformMatch(platform, p.Platform) {
			continue
		}
		for _, m := range p.Models {
			if strings.ToLower(m) == modelLower {
				return p
			}
		}
	}
	// 通配符匹配：按配置顺序，先匹配先使用
	for i := range pricingList {
		p := &pricingList[i]
		if !isPlatformMatch(platform, p.Platform) {
			continue
		}
		for _, m := range p.Models {
			ml := strings.ToLower(m)
			if !strings.HasSuffix(ml, "*") {
				continue
			}
			prefix := strings.TrimSuffix(ml, "*")
			if strings.HasPrefix(modelLower, prefix) {
				return p
			}
		}
	}
	return nil
}

// isPlatformMatch 判断平台是否匹配（空平台视为不限平台）。
func isPlatformMatch(queryPlatform, pricingPlatform string) bool {
	if queryPlatform == "" || pricingPlatform == "" {
		return true
	}
	return queryPlatform == pricingPlatform
}

// calculateStatsCost 使用给定的定价计算费用（不含任何倍率，原始费用）。
func calculateStatsCost(pricing *ChannelModelPricing, tokens UsageTokens, requestCount int, media mediaStatsContext) *float64 {
	if pricing == nil {
		return nil
	}
	switch pricing.BillingMode {
	case BillingModePerRequest, BillingModeImage:
		return calculatePerRequestStatsCost(pricing, requestCount, media)
	default:
		return calculateTokenStatsCost(pricing, tokens)
	}
}

// calculatePerRequestStatsCost 按次/图片/视频计费。
//
// 先按档位标签取价（图片的 1K/2K/4K 成本不同），没配档位才回落到扁平价。
// 视频（DurationSeconds > 0）再乘时长，与客户侧 CalculateVideoCost
// （perSecondPrice × duration × count）口径一致。
func calculatePerRequestStatsCost(pricing *ChannelModelPricing, requestCount int, media mediaStatsContext) *float64 {
	unit := pricing.PerRequestPrice
	if media.SizeTier != "" {
		if tier := pricing.GetTierByLabel(media.SizeTier); tier != nil && tier.PerRequestPrice != nil {
			unit = tier.PerRequestPrice
		}
	}
	if unit == nil || *unit <= 0 {
		return nil
	}
	cost := *unit * float64(requestCount)
	if media.DurationSeconds > 0 {
		cost *= float64(media.DurationSeconds)
	}
	return &cost
}

// calculateTokenStatsCost Token 计费。
// If the pricing has intervals, find the matching interval by total token count
// and use its prices instead of the flat pricing fields.
func calculateTokenStatsCost(pricing *ChannelModelPricing, tokens UsageTokens) *float64 {
	p := pricing
	if len(pricing.Intervals) > 0 {
		totalTokens := tokens.InputTokens + tokens.OutputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
		if iv := FindMatchingInterval(pricing.Intervals, totalTokens); iv != nil {
			p = &ChannelModelPricing{
				InputPrice:      iv.InputPrice,
				OutputPrice:     iv.OutputPrice,
				CacheWritePrice: iv.CacheWritePrice,
				CacheReadPrice:  iv.CacheReadPrice,
				PerRequestPrice: iv.PerRequestPrice,
			}
		}
	}
	deref := func(ptr *float64) float64 {
		if ptr == nil {
			return 0
		}
		return *ptr
	}
	cost := float64(tokens.InputTokens)*deref(p.InputPrice) +
		float64(tokens.OutputTokens)*deref(p.OutputPrice) +
		float64(tokens.CacheCreationTokens)*deref(p.CacheWritePrice) +
		float64(tokens.CacheReadTokens)*deref(p.CacheReadPrice) +
		float64(tokens.ImageOutputTokens)*deref(p.ImageOutputPrice)
	if cost <= 0 {
		return nil
	}
	return &cost
}

// applyAccountStatsCost resolves the account stats cost for a usage log entry.
// It resolves the upstream model (falling back to the requested model) and calls
// the 4-level priority chain via resolveAccountStatsCost.
func applyAccountStatsCost(
	ctx context.Context,
	usageLog *UsageLog,
	cs *ChannelService, bs *BillingService,
	accountID int64, groupID int64,
	upstreamModel, requestedModel string,
	tokens UsageTokens,
	totalCost float64,
) {
	model := upstreamModel
	if model == "" {
		model = requestedModel
	}
	requestCount := 1
	if usageLog != nil && usageLog.ImageCount > 0 {
		requestCount = usageLog.ImageCount
	}
	media := mediaStatsContext{}
	if usageLog != nil {
		if usageLog.ImageSize != nil {
			media.SizeTier = strings.TrimSpace(*usageLog.ImageSize)
		}
		// 视频才有时长；有时长就按秒算。图片路径这个字段为空，不受影响。
		if usageLog.VideoDurationSeconds != nil && *usageLog.VideoDurationSeconds > 0 {
			media.DurationSeconds = *usageLog.VideoDurationSeconds
		}
	}
	usageLog.AccountStatsCost = resolveAccountStatsCost(
		ctx, cs, bs, accountID, groupID, model, tokens, requestCount, totalCost, media,
	)
}
