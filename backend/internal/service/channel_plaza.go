package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// PlazaOfficialPricing 模型广场展示用的 LiteLLM 官方参考价（USD per token）。
// 字段为 nil 表示官方数据中该项缺失（0 视为未配置）。
type PlazaOfficialPricing struct {
	InputPrice        *float64
	OutputPrice       *float64
	CacheWritePrice   *float64 // 5m 缓存写入（= LiteLLM cache_creation）
	CacheWrite1hPrice *float64 // 1h 缓存写入（LiteLLM cache_creation_above_1hr）
	CacheReadPrice    *float64
}

// PlazaModel 模型广场中单个模型条目：渠道定价 + 官方参考价。
type PlazaModel struct {
	Name            string
	Platform        string
	Pricing         *ChannelModelPricing
	OfficialPricing *PlazaOfficialPricing
}

// plazaModelKey 是组内模型去重键。带平台是必需的：Composite 分组会展开多个具体
// 平台，同名模型可能来自不同平台且定价不同，只按名字去重会互相吞掉。
type plazaModelKey struct {
	platform string
	name     string
}

// PlazaGroup 模型广场中以分组为顶层的条目。
//
// 与 AvailableGroupRef 相比多了 Description 与 Models；Models 来自该分组关联渠道的
// 支持模型（普通分组按分组平台隔离防跨平台泄漏，Composite 分组展开关联渠道已配置
// 的具体平台），与「可用渠道」页口径一致。
type PlazaGroup struct {
	ID                 int64
	Name               string
	Description        string
	Platform           string
	SubscriptionType   string
	RateMultiplier     float64
	PeakRateEnabled    bool
	PeakStart          string
	PeakEnd            string
	PeakRateMultiplier float64
	IsExclusive        bool
	Models             []PlazaModel
}

// ListPlazaGroups 返回模型广场数据：每个活跃分组附带其可用模型与定价。
//
// 聚合口径与 ListAvailable 一致（Active 渠道、SupportedModels ∪ 全局定价回落、
// 平台隔离），仅把顶层从渠道换成分组：
//   - 渠道按 lower(name) 排序后遍历，保证同名模型去重结果确定；
//   - 同分组同名模型「先见者胜」，仅当已存条目无定价而新条目有定价时升级替换；
//   - 每个模型附带 LiteLLM 官方参考价（查不到为 nil）；
//   - 只返回 Models 非空的分组；分组按 RateMultiplier 升序（同倍率按名称），
//     组内模型按名称排序。
//
// 可见性过滤（专属分组）不在此层做，由 handler 按登录态裁剪。
func (s *ChannelService) ListPlazaGroups(ctx context.Context) ([]PlazaGroup, error) {
	channels, err := s.repo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active groups: %w", err)
	}

	sort.SliceStable(channels, func(i, j int) bool {
		return strings.ToLower(channels[i].Name) < strings.ToLower(channels[j].Name)
	})

	byGroup := make(map[int64]*PlazaGroup, len(groups))
	order := make([]int64, 0, len(groups))
	for i := range groups {
		g := groups[i]
		byGroup[g.ID] = &PlazaGroup{
			ID:                 g.ID,
			Name:               g.Name,
			Description:        g.Description,
			Platform:           g.Platform,
			SubscriptionType:   g.SubscriptionType,
			RateMultiplier:     g.RateMultiplier,
			PeakRateEnabled:    g.PeakRateEnabled,
			PeakStart:          g.PeakStart,
			PeakEnd:            g.PeakEnd,
			PeakRateMultiplier: g.PeakRateMultiplier,
			IsExclusive:        g.IsExclusive,
		}
		order = append(order, g.ID)
	}

	// modelIdx[groupID][{platform,name}] = index into byGroup[groupID].Models
	//
	// 键必须带平台：Composite 分组会同时展开多个具体平台，同一个模型名可能来自
	// 不同平台且定价不同（如 deepseek-v4-flash 同时配在 openai 与 anthropic 上），
	// 只按模型名去重会让后者被前者吞掉。
	modelIdx := make(map[int64]map[plazaModelKey]int, len(groups))
	for i := range channels {
		ch := &channels[i]
		if ch.Status != StatusActive {
			continue
		}
		ch.normalizeBillingModelSource()
		supported := ch.SupportedModels()
		s.fillGlobalPricingFallback(supported)

		for _, gid := range ch.GroupIDs {
			pg, ok := byGroup[gid]
			if !ok {
				continue
			}
			idx := modelIdx[gid]
			if idx == nil {
				idx = make(map[plazaModelKey]int, len(supported))
				modelIdx[gid] = idx
			}
			for j := range supported {
				m := supported[j]
				// Composite 分组本身没有具体平台，按「渠道已配置的具体平台」展开；
				// 普通分组仍按分组平台隔离。与「可用渠道」页口径一致。
				if pg.Platform == PlatformComposite {
					if !isConcreteRequestPlatform(m.Platform) {
						continue
					}
				} else if m.Platform != pg.Platform {
					continue
				}
				key := plazaModelKey{platform: m.Platform, name: m.Name}
				if at, seen := idx[key]; seen {
					// 先见者胜；仅当已存条目无定价而新条目有定价时升级。
					if pg.Models[at].Pricing == nil && m.Pricing != nil {
						pg.Models[at].Pricing = m.Pricing
					}
					continue
				}
				idx[key] = len(pg.Models)
				pg.Models = append(pg.Models, PlazaModel{
					Name:     m.Name,
					Platform: m.Platform,
					Pricing:  m.Pricing,
				})
			}
		}
	}

	officialMemo := make(map[string]*PlazaOfficialPricing)
	out := make([]PlazaGroup, 0, len(order))
	for _, gid := range order {
		pg := byGroup[gid]
		if len(pg.Models) == 0 {
			continue
		}
		sort.SliceStable(pg.Models, func(i, j int) bool { return pg.Models[i].Name < pg.Models[j].Name })
		for j := range pg.Models {
			pg.Models[j].OfficialPricing = s.lookupOfficialPricing(pg.Models[j].Name, officialMemo)
		}
		out = append(out, *pg)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RateMultiplier != out[j].RateMultiplier {
			return out[i].RateMultiplier < out[j].RateMultiplier
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// lookupOfficialPricing 查询模型的 LiteLLM 官方参考价，带 memo 避免同名模型重复转换。
// pricingService 为 nil（测试场景）或查不到时返回 nil。
func (s *ChannelService) lookupOfficialPricing(modelName string, memo map[string]*PlazaOfficialPricing) *PlazaOfficialPricing {
	if s.pricingService == nil {
		return nil
	}
	if cached, ok := memo[modelName]; ok {
		return cached
	}
	var result *PlazaOfficialPricing
	if lp := s.pricingService.GetModelPricing(modelName); lp != nil && !lp.TokenPricingAbsent {
		result = &PlazaOfficialPricing{
			InputPrice:        nonZeroPtr(lp.InputCostPerToken),
			OutputPrice:       nonZeroPtr(lp.OutputCostPerToken),
			CacheWritePrice:   nonZeroPtr(lp.CacheCreationInputTokenCost),
			CacheWrite1hPrice: nonZeroPtr(lp.CacheCreationInputTokenCostAbove1hr),
			CacheReadPrice:    nonZeroPtr(lp.CacheReadInputTokenCost),
		}
		if result.InputPrice == nil && result.OutputPrice == nil &&
			result.CacheWritePrice == nil && result.CacheWrite1hPrice == nil && result.CacheReadPrice == nil {
			result = nil
		}
	}
	memo[modelName] = result
	return result
}
