package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 1.5K 是给 B 端固定输出尺寸单开的档位。只认显式标签和确切像素——
// 通用像素分档一个字都不能动，否则存量 API 调用方会从 2K 被静默挪走。
func TestImageTier1_5KRecognized(t *testing.T) {
	for _, in := range []string{"1.5k", "1.5K", " 1536x1536 ", "1536x864", "864x1536"} {
		tier, ok := ClassifyImageBillingTier(in)
		require.True(t, ok, in)
		require.Equal(t, ImageBillingSize1_5K, tier, in)
	}
}

// 这是本次改动最关键的保证：1025~2048 之间的其它尺寸仍然归 2K，
// 不能因为新开了 1.5K 档就改掉它们的账。
func TestGeneralPixelBandingUnchanged(t *testing.T) {
	for _, in := range []string{"1200x1200", "1280x720", "1500x1500", "2048x2048", "1920x1080"} {
		tier, ok := ClassifyImageBillingTier(in)
		require.True(t, ok, in)
		require.Equal(t, ImageBillingSize2K, tier, in+" 必须仍然是 2K")
	}
	for _, in := range []string{"1024x1024", "512x512"} {
		tier, _ := ClassifyImageBillingTier(in)
		require.Equal(t, ImageBillingSize1K, tier, in)
	}
	for _, in := range []string{"3840x2160", "4096x4096"} {
		tier, _ := ClassifyImageBillingTier(in)
		require.Equal(t, ImageBillingSize4K, tier, in)
	}
}

// 档位排序要把 1.5K 夹在 1K 和 2K 之间，否则用量统计里的档位顺序会错乱。
func TestImageTierRankOrder(t *testing.T) {
	require.Less(t, imageTierRank(ImageBillingSize1K), imageTierRank(ImageBillingSize1_5K))
	require.Less(t, imageTierRank(ImageBillingSize1_5K), imageTierRank(ImageBillingSize2K))
	require.Less(t, imageTierRank(ImageBillingSize2K), imageTierRank(ImageBillingSize4K))
}

// 归一后的标签再进一次归一必须原样返回（计费链路上会调两次）。
func TestNormalizeIsIdempotentFor1_5K(t *testing.T) {
	require.Equal(t, ImageBillingSize1_5K, NormalizeImageBillingTierOrDefault("1.5k"))
	require.Equal(t, ImageBillingSize1_5K, NormalizeImageBillingTierOrDefault(ImageBillingSize1_5K))
}

// 统计口径要带上新档位，否则 1.5K 的张数会在明细里凭空消失。
func TestBreakdownKeeps1_5K(t *testing.T) {
	out := normalizeImageSizeBreakdown(map[string]int{ImageBillingSize1_5K: 3, ImageBillingSize2K: 1})
	require.Equal(t, 3, out[ImageBillingSize1_5K])
	require.Equal(t, 1, out[ImageBillingSize2K])
}
