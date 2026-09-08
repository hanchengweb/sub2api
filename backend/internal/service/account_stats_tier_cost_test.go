//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func f64(v float64) *float64 { return &v }

// 按次成本要能按档位取价：图片 1K/2K/4K 的上游成本本就不同
// （toAPI gpt-image-2：¥0.1050 / 0.1400 / 0.1750），只配一个扁平值必然算错。
func TestPerRequestStatsCost_PicksTierByLabel(t *testing.T) {
	pricing := &ChannelModelPricing{
		BillingMode:     BillingModeImage,
		PerRequestPrice: f64(10.5), // 兜底＝1K 价
		Intervals: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: f64(10.5)},
			{TierLabel: "2K", PerRequestPrice: f64(14)},
			{TierLabel: "4K", PerRequestPrice: f64(17.5)},
		},
	}
	for tier, want := range map[string]float64{"1K": 10.5, "2K": 14, "4K": 17.5} {
		got := calculateStatsCost(pricing, UsageTokens{}, 1, mediaStatsContext{SizeTier: tier})
		require.NotNil(t, got, "档位 %s", tier)
		require.InDelta(t, want, *got, 1e-9, "档位 %s 应取该档价", tier)
	}
}

// 档位标签大小写不敏感，且未知档位回落到扁平价而不是算不出来。
func TestPerRequestStatsCost_TierFallback(t *testing.T) {
	pricing := &ChannelModelPricing{
		BillingMode:     BillingModeImage,
		PerRequestPrice: f64(10.5),
		Intervals:       []PricingInterval{{TierLabel: "2K", PerRequestPrice: f64(14)}},
	}
	lower := calculateStatsCost(pricing, UsageTokens{}, 1, mediaStatsContext{SizeTier: "2k"})
	require.NotNil(t, lower)
	require.InDelta(t, 14, *lower, 1e-9, "档位匹配应忽略大小写")

	unknown := calculateStatsCost(pricing, UsageTokens{}, 1, mediaStatsContext{SizeTier: "8K"})
	require.NotNil(t, unknown)
	require.InDelta(t, 10.5, *unknown, 1e-9, "未知档位回落扁平价")

	none := calculateStatsCost(pricing, UsageTokens{}, 1, mediaStatsContext{})
	require.NotNil(t, none)
	require.InDelta(t, 10.5, *none, 1e-9, "没传档位回落扁平价")
}

// 视频按秒计费：单价 × 时长，与客户侧 CalculateVideoCost 口径一致。
// grok-video-1.5 成本 ¥0.08/秒 = 8 积分/秒；只按次算的话 5 秒和 30 秒成本相同，显然错。
func TestPerRequestStatsCost_VideoMultipliesByDuration(t *testing.T) {
	pricing := &ChannelModelPricing{
		BillingMode:     BillingModeImage,
		PerRequestPrice: f64(8), // 每秒成本
	}
	for _, tc := range []struct {
		seconds int
		want    float64
	}{{6, 48}, {8, 64}, {30, 240}} {
		got := calculateStatsCost(pricing, UsageTokens{}, 1, mediaStatsContext{DurationSeconds: tc.seconds})
		require.NotNil(t, got)
		require.InDelta(t, tc.want, *got, 1e-9, "%d 秒", tc.seconds)
	}
}

// 多张图片按张数乘。
func TestPerRequestStatsCost_MultipliesByRequestCount(t *testing.T) {
	pricing := &ChannelModelPricing{BillingMode: BillingModeImage, PerRequestPrice: f64(10.5)}
	got := calculateStatsCost(pricing, UsageTokens{}, 3, mediaStatsContext{})
	require.NotNil(t, got)
	require.InDelta(t, 31.5, *got, 1e-9)
}

// token 模式不受媒体上下文影响：传了档位/时长也不能改变 token 计费结果。
func TestTokenStatsCost_IgnoresMediaContext(t *testing.T) {
	pricing := &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  f64(0.0002),
		OutputPrice: f64(0.0006),
	}
	tokens := UsageTokens{InputTokens: 1000, OutputTokens: 500}
	plain := calculateStatsCost(pricing, tokens, 1, mediaStatsContext{})
	withMedia := calculateStatsCost(pricing, tokens, 1, mediaStatsContext{SizeTier: "4K", DurationSeconds: 30})
	require.NotNil(t, plain)
	require.NotNil(t, withMedia)
	require.InDelta(t, *plain, *withMedia, 1e-12, "token 计费不该被媒体上下文影响")
}
