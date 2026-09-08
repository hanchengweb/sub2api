//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// toAPI 的 size 是宽高比、档位在 resolution；OpenAI 的 size 才是像素尺寸。
// 两套口径必须都能判对，否则会按错误档位收费。
//
// 实测（2026-09-08）：客户端传 size="1:1" + resolution="1k"，上游按 1K 收我们的钱，
// 而我们只看 size、解析不出像素就默认 2K，于是按 2K 收用户的钱——两边错位。
func TestResolveImageBillingTier_ToapiStyle(t *testing.T) {
	require.Equal(t, ImageBillingSize1K, ResolveImageBillingTier("1:1", "1k"))
	require.Equal(t, ImageBillingSize2K, ResolveImageBillingTier("16:9", "2k"))
	require.Equal(t, ImageBillingSize4K, ResolveImageBillingTier("1:1", "4K"), "档位大小写不敏感")
}

// OpenAI 风格（只有像素 size、无 resolution）行为不变。
func TestResolveImageBillingTier_OpenAIStyle(t *testing.T) {
	require.Equal(t, ImageBillingSize1K, ResolveImageBillingTier("1024x1024", ""))
	require.Equal(t, ImageBillingSize2K, ResolveImageBillingTier("2048x2048", ""))
	require.Equal(t, ImageBillingSize4K, ResolveImageBillingTier("3840x2160", ""))
}

// 两者都给且矛盾时跟上游走：上游按 resolution 计费，我们也必须按它，否则又会错账。
func TestResolveImageBillingTier_ResolutionWinsOverSize(t *testing.T) {
	require.Equal(t, ImageBillingSize4K, ResolveImageBillingTier("1024x1024", "4k"),
		"resolution 是上游的计费字段，矛盾时它说了算")
}

// 都解析不出时回落 2K 默认，不能返回空档位（空档位会让分档定价查不到、回落扁平价）。
func TestResolveImageBillingTier_FallsBackTo2K(t *testing.T) {
	require.Equal(t, ImageBillingSize2K, ResolveImageBillingTier("1:1", ""))
	require.Equal(t, ImageBillingSize2K, ResolveImageBillingTier("", ""))
	require.Equal(t, ImageBillingSize2K, ResolveImageBillingTier("auto", "auto"))
}

// 视频的 resolution（720p 等）不能被误判成图片档位。
func TestResolveImageBillingTier_VideoResolutionNotMistaken(t *testing.T) {
	for _, r := range []string{"480p", "720p", "1080p"} {
		require.Equal(t, ImageBillingSize2K, ResolveImageBillingTier("", r),
			"视频分辨率 %s 不是图片档位，应回落默认", r)
	}
}
