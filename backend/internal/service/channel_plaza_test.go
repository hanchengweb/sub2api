//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// newPlazaChannelService 构造 ListPlazaGroups 测试用的 ChannelService。
func newPlazaChannelService(channels []Channel, groups []Group, pricing *PricingService) *ChannelService {
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return channels, nil },
	}
	svc := NewChannelService(repo, &stubGroupRepoForAvailable{activeGroups: groups}, nil, nil)
	svc.pricingService = pricing
	return svc
}

func plazaPricedChannel(id int64, name string, groupIDs []int64, platform string, models ...string) Channel {
	return Channel{
		ID:       id,
		Name:     name,
		Status:   StatusActive,
		GroupIDs: groupIDs,
		ModelPricing: []ChannelModelPricing{{
			Platform:    platform,
			Models:      models,
			BillingMode: BillingModeToken,
			InputPrice:  testPtrFloat64(3e-6),
			OutputPrice: testPtrFloat64(1.5e-5),
		}},
	}
}

func TestListPlazaGroups_GroupCentricAggregation(t *testing.T) {
	// 两个渠道挂同一分组:模型并入同一 PlazaGroup;无模型的分组不返回。
	channels := []Channel{
		plazaPricedChannel(1, "chA", []int64{10}, "anthropic", "claude-sonnet"),
		plazaPricedChannel(2, "chB", []int64{10}, "anthropic", "claude-opus"),
	}
	groups := []Group{
		{ID: 10, Name: "g-main", Description: "desc", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-empty", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaChannelService(channels, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1, "无模型的分组不应返回")
	require.Equal(t, int64(10), out[0].ID)
	require.Equal(t, "desc", out[0].Description)
	require.Len(t, out[0].Models, 2)
	// 组内模型按名称排序
	require.Equal(t, "claude-opus", out[0].Models[0].Name)
	require.Equal(t, "claude-sonnet", out[0].Models[1].Name)
}

func TestListPlazaGroups_DedupFirstWinsWithPricingUpgrade(t *testing.T) {
	// 同名模型:先见者胜;仅当已存条目无定价而新条目有定价时升级替换。
	unpriced := Channel{
		ID: 1, Name: "alpha", Status: StatusActive, GroupIDs: []int64{10},
		// mapping-only → SupportedModels 产出无定价条目
		ModelMapping: map[string]map[string]string{
			"anthropic": {"claude-sonnet": "claude-sonnet"},
		},
	}
	priced := plazaPricedChannel(2, "beta", []int64{10}, "anthropic", "claude-sonnet")
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}

	// alpha(无价)按名称序先于 beta(有价):先见者无价,应被有价条目升级。
	svc := newPlazaChannelService([]Channel{priced, unpriced}, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1)
	require.NotNil(t, out[0].Models[0].Pricing, "无价条目应被有价条目升级")
	require.NotNil(t, out[0].Models[0].Pricing.InputPrice)
}

func TestListPlazaGroups_PlatformIsolation(t *testing.T) {
	// 渠道同时有 anthropic/openai 定价,anthropic 分组只应看到 anthropic 模型。
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{10, 20},
		ModelPricing: []ChannelModelPricing{
			{Platform: "anthropic", Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: "openai", Models: []string{"gpt-5"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	groups := []Group{
		{ID: 10, Name: "g-claude", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "g-gpt", Platform: "openai", RateMultiplier: 1},
	}
	svc := newPlazaChannelService([]Channel{ch}, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 2)
	byName := map[string][]PlazaModel{}
	for _, g := range out {
		byName[g.Name] = g.Models
	}
	require.Len(t, byName["g-claude"], 1)
	require.Equal(t, "claude-sonnet", byName["g-claude"][0].Name)
	require.Len(t, byName["g-gpt"], 1)
	require.Equal(t, "gpt-5", byName["g-gpt"][0].Name)
}

func TestListPlazaGroups_InactiveChannelSkipped(t *testing.T) {
	inactive := plazaPricedChannel(1, "off", []int64{10}, "anthropic", "claude-sonnet")
	inactive.Status = "inactive"
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaChannelService([]Channel{inactive}, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Empty(t, out)
}

func TestListPlazaGroups_SortedByRateMultiplierAsc(t *testing.T) {
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10, 20, 30}, "anthropic", "claude-sonnet"),
	}
	groups := []Group{
		{ID: 10, Name: "b-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 20, Name: "a-standard", Platform: "anthropic", RateMultiplier: 1},
		{ID: 30, Name: "cheap", Platform: "anthropic", RateMultiplier: 0.5},
	}
	svc := newPlazaChannelService(channels, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.Equal(t, "cheap", out[0].Name, "倍率低者在前")
	require.Equal(t, "a-standard", out[1].Name, "同倍率按名称")
	require.Equal(t, "b-standard", out[2].Name)
}

func TestListPlazaGroups_OfficialPricingFill(t *testing.T) {
	pricingSvc := newStubPricingServiceFromMap(map[string]*LiteLLMModelPricing{
		"claude-sonnet": {
			Mode:                                "chat",
			InputCostPerToken:                   3e-6,
			OutputCostPerToken:                  1.5e-5,
			CacheCreationInputTokenCost:         3.75e-6,
			CacheCreationInputTokenCostAbove1hr: 6e-6,
			CacheReadInputTokenCost:             3e-7,
		},
		"token-absent": {Mode: "image_generation", TokenPricingAbsent: true, OutputCostPerImage: 0.04},
	})
	channels := []Channel{
		plazaPricedChannel(1, "ch", []int64{10}, "anthropic", "claude-sonnet", "unknown-model", "token-absent"),
	}
	groups := []Group{{ID: 10, Name: "g", Platform: "anthropic", RateMultiplier: 1}}
	svc := newPlazaChannelService(channels, groups, pricingSvc)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 3)

	byName := map[string]PlazaModel{}
	for _, m := range out[0].Models {
		byName[m.Name] = m
	}
	// 命中:填充完整官方价(含 1h 缓存写入)
	official := byName["claude-sonnet"].OfficialPricing
	require.NotNil(t, official)
	require.InDelta(t, 3e-6, *official.InputPrice, 1e-12)
	require.InDelta(t, 6e-6, *official.CacheWrite1hPrice, 1e-12)
	require.InDelta(t, 3e-7, *official.CacheReadPrice, 1e-12)
	// 未命中:nil(GetModelPricing 的 claude 系列模糊匹配对非 claude 名不生效)
	require.Nil(t, byName["unknown-model"].OfficialPricing)
	// TokenPricingAbsent 条目不作为官方 token 价展示
	require.Nil(t, byName["token-absent"].OfficialPricing)
}

func TestListPlazaGroups_RepoErrorsPropagate(t *testing.T) {
	sentinel := errors.New("boom")
	repo := &mockChannelRepository{
		listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, sentinel },
	}
	svc := NewChannelService(repo, &stubGroupRepoForAvailable{}, nil, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.Nil(t, out)
	require.ErrorIs(t, err, sentinel)

	svc2 := NewChannelService(
		&mockChannelRepository{listAllFn: func(ctx context.Context) ([]Channel, error) { return nil, nil }},
		&stubGroupRepoForAvailable{listActiveErr: sentinel},
		nil, nil,
	)
	out2, err2 := svc2.ListPlazaGroups(context.Background())
	require.Nil(t, out2)
	require.ErrorIs(t, err2, sentinel)
}

func TestListPlazaGroups_CompositeExpandsConcretePlatforms(t *testing.T) {
	// Composite 分组本身没有具体平台,应展开渠道已配置的所有具体平台;
	// 非具体平台(如 composite 自身)的定价条目不进广场。
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{30},
		ModelPricing: []ChannelModelPricing{
			{Platform: "openai", Models: []string{"deepseek-v4-flash"}, InputPrice: testPtrFloat64(3e-4)},
			{Platform: "anthropic", Models: []string{"claude-sonnet"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: "grok", Models: []string{"grok-video-1.5"}, InputPrice: testPtrFloat64(1e-6)},
			{Platform: PlatformComposite, Models: []string{"should-not-appear"}, InputPrice: testPtrFloat64(1e-6)},
		},
	}
	groups := []Group{{ID: 30, Name: "g-composite", Platform: PlatformComposite, RateMultiplier: 1}}
	svc := newPlazaChannelService([]Channel{ch}, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1, "composite 分组不应因平台不等被整组丢弃")

	names := make([]string, 0, len(out[0].Models))
	for _, m := range out[0].Models {
		names = append(names, m.Name)
	}
	require.ElementsMatch(t, []string{"claude-sonnet", "deepseek-v4-flash", "grok-video-1.5"}, names)
	require.NotContains(t, names, "should-not-appear", "非具体平台的条目不应展开")
}

func TestListPlazaGroups_CompositeMergesSameNameSamePrice(t *testing.T) {
	// 同一个模型挂在两个协议上（线上 deepseek-v4-flash 就同时挂 openai 与 anthropic），
	// 实质是同一个上游的两种调用协议，价格一样，广场不应该出两行。
	ch := Channel{
		ID: 1, Name: "dual", Status: StatusActive, GroupIDs: []int64{30},
		ModelPricing: []ChannelModelPricing{
			{Platform: "openai", Models: []string{"deepseek-v4-flash"}, InputPrice: testPtrFloat64(3e-4)},
			{Platform: "anthropic", Models: []string{"deepseek-v4-flash"}, InputPrice: testPtrFloat64(3e-4)},
		},
	}
	groups := []Group{{ID: 30, Name: "g-composite", Platform: PlatformComposite, RateMultiplier: 1}}
	svc := newPlazaChannelService([]Channel{ch}, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1, "同名同价的两个协议应合并成一行")
	require.Equal(t, "deepseek-v4-flash", out[0].Models[0].Name)
}

func TestListPlazaGroups_CompositeKeepsSameNameDifferentPrice(t *testing.T) {
	// 同名不同价必须各留一行：不同上游共用一个裸名时，合并会让用户看到的
	// 价格取决于渠道遍历顺序——这正是当初把平台加进去重键要防的事。
	ch := Channel{
		ID: 1, Name: "dual", Status: StatusActive, GroupIDs: []int64{30},
		ModelPricing: []ChannelModelPricing{
			{Platform: "openai", Models: []string{"shared-name"}, InputPrice: testPtrFloat64(3e-4)},
			{Platform: "anthropic", Models: []string{"shared-name"}, InputPrice: testPtrFloat64(9e-3)},
		},
	}
	groups := []Group{{ID: 30, Name: "g-composite", Platform: PlatformComposite, RateMultiplier: 1}}
	svc := newPlazaChannelService([]Channel{ch}, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 2, "同名不同价应各保留一条")

	prices := make([]float64, 0, 2)
	for _, m := range out[0].Models {
		require.Equal(t, "shared-name", m.Name)
		require.NotNil(t, m.Pricing)
		require.NotNil(t, m.Pricing.InputPrice)
		prices = append(prices, *m.Pricing.InputPrice)
	}
	require.ElementsMatch(t, []float64{3e-4, 9e-3}, prices)
}

// 区间定价（图片分辨率分层）也必须进入等价判定，否则两个只在 tier 价上
// 不同的条目会被当成同一行合并掉，用户看到的图片价就错了。
func TestListPlazaGroups_IntervalPricingParticipatesInDedup(t *testing.T) {
	mk := func(platform string, per float64) ChannelModelPricing {
		return ChannelModelPricing{
			Platform: platform, Models: []string{"img"}, BillingMode: BillingModePerRequest,
			Intervals: []PricingInterval{{TierLabel: "1K", PerRequestPrice: testPtrFloat64(per)}},
		}
	}
	groups := []Group{{ID: 30, Name: "g-composite", Platform: PlatformComposite, RateMultiplier: 1}}

	same := Channel{ID: 1, Name: "c", Status: StatusActive, GroupIDs: []int64{30},
		ModelPricing: []ChannelModelPricing{mk("openai", 0.395), mk("anthropic", 0.395)}}
	out, err := newPlazaChannelService([]Channel{same}, groups, nil).ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out[0].Models, 1, "区间价相同应合并")

	diff := Channel{ID: 1, Name: "c", Status: StatusActive, GroupIDs: []int64{30},
		ModelPricing: []ChannelModelPricing{mk("openai", 0.395), mk("anthropic", 2.933)}}
	out, err = newPlazaChannelService([]Channel{diff}, groups, nil).ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out[0].Models, 2, "区间价不同不得合并")
}

func TestListPlazaGroups_CustomModelsListFiltersDisplay(t *testing.T) {
	// 分组启用自定义模型列表时,广场只展示白名单内的模型;旧别名仍在渠道定价里
	// (照常可调用与计费),只是不对外宣传。
	ch := Channel{
		ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{30},
		ModelPricing: []ChannelModelPricing{
			{Platform: "openai", Models: []string{"t-gpt-image-2", "gpt-image-2"}, InputPrice: testPtrFloat64(3e-6)},
			{Platform: "openai", Models: []string{"gpt-image-1"}, InputPrice: testPtrFloat64(3e-6)},
		},
	}
	groups := []Group{{
		ID: 30, Name: "g-composite", Platform: PlatformComposite, RateMultiplier: 1,
		ModelsListConfig: GroupModelsListConfig{
			Enabled: true,
			Models:  []string{"t-gpt-image-2"},
		},
	}}
	svc := newPlazaChannelService([]Channel{ch}, groups, nil)
	out, err := svc.ListPlazaGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Len(t, out[0].Models, 1, "白名单外的模型不应出现在广场")
	require.Equal(t, "t-gpt-image-2", out[0].Models[0].Name)
}

func TestListPlazaGroups_CustomModelsListDisabledShowsAll(t *testing.T) {
	// 未启用(或白名单为空)时不过滤,保持既有行为。
	mk := func(cfg GroupModelsListConfig) []PlazaGroup {
		ch := Channel{
			ID: 1, Name: "multi", Status: StatusActive, GroupIDs: []int64{30},
			ModelPricing: []ChannelModelPricing{
				{Platform: "openai", Models: []string{"t-gpt-image-2", "gpt-image-2"}, InputPrice: testPtrFloat64(3e-6)},
			},
		}
		groups := []Group{{ID: 30, Name: "g", Platform: PlatformComposite, RateMultiplier: 1, ModelsListConfig: cfg}}
		out, err := newPlazaChannelService([]Channel{ch}, groups, nil).ListPlazaGroups(context.Background())
		require.NoError(t, err)
		return out
	}

	// 开关关闭:即使配了名单也不过滤
	off := mk(GroupModelsListConfig{Enabled: false, Models: []string{"t-gpt-image-2"}})
	require.Len(t, off[0].Models, 2)

	// 开关打开但名单为空:同样不过滤(避免误配导致整组为空而消失)
	empty := mk(GroupModelsListConfig{Enabled: true})
	require.Len(t, empty[0].Models, 2)
}
