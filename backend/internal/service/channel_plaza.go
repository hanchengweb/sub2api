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
	// VideoPricing 仅视频模型有值：视频按秒计价，价格存在分组的
	// video_price_480p/720p/1080p 三列上，不在渠道定价表里，所以走单独字段。
	VideoPricing *PlazaVideoPricing
}

// PlazaVideoPricing 视频模型的每秒单价（按清晰度分档）。
type PlazaVideoPricing struct {
	PricePer480P  *float64
	PricePer720P  *float64
	PricePer1080P *float64
}

// plazaPricingEquivalent 判断两份定价对用户是否等价。
//
// 任一方为 nil 视为等价：nil 表示该渠道没配定价，应当并入同名的已定价
// 条目，而不是在广场上单开一行「无价格」。
func plazaPricingEquivalent(a, b *ChannelModelPricing) bool {
	if a == nil || b == nil {
		return true
	}
	return a.userFacingPricingKey() == b.userFacingPricingKey()
}

// userFacingPricingKey 生成「用户实际付什么价」的指纹。
//
// 只取价格字段，刻意排除 ID / ChannelID / Platform / 时间戳——同一个模型配在
// 两个协议（如 deepseek-v4-flash 同时挂 openai 与 anthropic）时这些元数据必定不同，
// 带上它们就永远合并不了。
func (p *ChannelModelPricing) userFacingPricingKey() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	num := func(f *float64) {
		if f == nil {
			b.WriteString("-|")
			return
		}
		fmt.Fprintf(&b, "%.12g|", *f)
	}
	b.WriteString(string(p.BillingMode))
	b.WriteByte('|')
	for _, f := range []*float64{
		p.InputPrice, p.OutputPrice, p.CacheWritePrice, p.CacheReadPrice,
		p.ImageInputPrice, p.ImageOutputPrice, p.PerRequestPrice,
	} {
		num(f)
	}

	// 区间顺序不影响实际报价，排序后再序列化，避免两边区间同集合但存储
	// 顺序不同时被判为不等价。
	iv := make([]PricingInterval, len(p.Intervals))
	copy(iv, p.Intervals)
	sort.SliceStable(iv, func(i, j int) bool {
		if iv[i].TierLabel != iv[j].TierLabel {
			return iv[i].TierLabel < iv[j].TierLabel
		}
		return iv[i].MinTokens < iv[j].MinTokens
	})
	for k := range iv {
		in := &iv[k]
		fmt.Fprintf(&b, "#%s:%d:", in.TierLabel, in.MinTokens)
		if in.MaxTokens == nil {
			b.WriteString("-:")
		} else {
			fmt.Fprintf(&b, "%d:", *in.MaxTokens)
		}
		for _, f := range []*float64{
			in.InputPrice, in.OutputPrice, in.CacheWritePrice, in.CacheReadPrice, in.PerRequestPrice,
		} {
			num(f)
		}
	}
	return b.String()
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
//   - 分组启用自定义模型列表（ModelsListConfig）时只展示白名单内的模型，与
//     /v1/models 共用同一份配置；旧别名仍可调用与计费，只是不对外宣传；
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
	// allowByGroup[groupID] 是该分组自定义模型列表的白名单集合；未启用自定义列表的
	// 分组不入表，表示不过滤。广场与 /v1/models 共用同一份配置：运营者配一次，
	// 对外宣传面与客户端可见的模型列表保持一致，不会出现「列表里没有、定价页却在展示」。
	allowByGroup := make(map[int64]map[string]struct{})
	for i := range groups {
		g := groups[i]
		if g.CustomModelsListEnabled() {
			allow := make(map[string]struct{}, len(g.ModelsListConfig.Models))
			for _, m := range g.ModelsListConfig.Models {
				if m = strings.TrimSpace(m); m != "" {
					allow[m] = struct{}{}
				}
			}
			allowByGroup[g.ID] = allow
		}
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

	// modelIdx[groupID][modelName] = 该名字在 byGroup[groupID].Models 里占的行下标。
	//
	// 一个名字可能占多行：去重按「用户实际付什么价」而不是按平台。
	//   同名同价（如 deepseek-v4-flash 同时挂 openai 与 anthropic，其实是同一个上游
	//   的两种调用协议）→ 合并成一行，不在广场上出重复条目。
	//   同名不同价（不同上游共用一个裸名）→ 保留多行，否则会互相吞掉，
	//   用户看到的价格取决于渠道遍历顺序。
	modelIdx := make(map[int64]map[string][]int, len(groups))
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
				idx = make(map[string][]int, len(supported))
				modelIdx[gid] = idx
			}
			allow := allowByGroup[gid]
			for j := range supported {
				m := supported[j]
				// 分组启用自定义模型列表时，广场只展示白名单内的模型。旧别名仍可调用
				// 与计费（渠道定价的 models 数组不受影响），只是不再对外宣传。
				if allow != nil {
					if _, ok := allow[m.Name]; !ok {
						continue
					}
				}
				// Composite 分组本身没有具体平台，按「渠道已配置的具体平台」展开；
				// 普通分组仍按分组平台隔离。与「可用渠道」页口径一致。
				if pg.Platform == PlatformComposite {
					if !isConcreteRequestPlatform(m.Platform) {
						continue
					}
				} else if m.Platform != pg.Platform {
					continue
				}
				rows := idx[m.Name]
				merged := false
				for _, at := range rows {
					if !plazaPricingEquivalent(pg.Models[at].Pricing, m.Pricing) {
						continue
					}
					// 先见者胜；仅当已存条目无定价而新条目有定价时升级。
					if pg.Models[at].Pricing == nil && m.Pricing != nil {
						pg.Models[at].Pricing = m.Pricing
					}
					merged = true
					break
				}
				if merged {
					continue
				}
				idx[m.Name] = append(rows, len(pg.Models))
				pg.Models = append(pg.Models, PlazaModel{
					Name:     m.Name,
					Platform: m.Platform,
					Pricing:  m.Pricing,
				})
			}
		}
	}

	// 补齐「白名单里有、渠道集合里没有」的模型。
	//
	// 广场的模型集合是从渠道 SupportedModels 拼的，而 /v1/models 直接读白名单，
	// 两者本该一致（见上文注释），实际会漏：视频模型没有渠道定价行，就进不了
	// 渠道集合，于是「客户端列得出来、定价页查不到价」。视频价本来就在分组上
	// （video_price_* 三列），这里按白名单补行并挂上它。
	for i := range groups {
		g := groups[i]
		pg := byGroup[g.ID]
		if pg == nil || !g.CustomModelsListEnabled() {
			continue
		}
		present := make(map[string]struct{}, len(pg.Models))
		for j := range pg.Models {
			present[pg.Models[j].Name] = struct{}{}
		}
		for _, name := range g.ModelsListConfig.Models {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := present[name]; ok {
				continue
			}
			// 只补视频模型：其余缺失项没有可展示的价格，补进来只会多一行「-」。
			if !isPlazaVideoModelName(name) {
				continue
			}
			vp := &PlazaVideoPricing{
				PricePer480P:  g.VideoPrice480P,
				PricePer720P:  g.VideoPrice720P,
				PricePer1080P: g.VideoPrice1080P,
			}
			if vp.PricePer480P == nil && vp.PricePer720P == nil && vp.PricePer1080P == nil {
				continue
			}
			pg.Models = append(pg.Models, PlazaModel{
				Name:         name,
				Platform:     PlatformGrok,
				VideoPricing: vp,
			})
			present[name] = struct{}{}
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

// isPlazaVideoModelName 判断模型名是否为视频模型。
//
// 按名字判而不是按 billing_mode：视频模型的 billing_mode 配的是 image
// （它走图片接口那条闸门），计费模式区分不出视频。
func isPlazaVideoModelName(name string) bool {
	return strings.Contains(strings.ToLower(name), "video")
}
