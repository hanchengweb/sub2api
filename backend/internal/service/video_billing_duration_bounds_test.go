//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 各上游的真实时长区间。计费区间必须与之逐模型对齐——比上游窄会被套利（少收），
// 比上游宽会多收。
//
// grok-video-1.5 的 6-30 来自 2026-09-08 实测，上游错误原文
// "seconds must be between 6 and 30"。
func TestVideoDurationBounds_MatchUpstreamPerModel(t *testing.T) {
	cases := []struct {
		model            string
		wantMin, wantMax int
	}{
		{"grok-imagine-video", 1, 15},     // 直连 xAI
		{"grok-imagine-video-1.5", 1, 15}, // 同上，带版本号
		{"grok-video-1.5", 6, 30},         // toAPI
	}
	for _, tc := range cases {
		gotMin, gotMax := videoDurationBoundsForModel(tc.model)
		require.Equal(t, tc.wantMin, gotMin, "%s 下限", tc.model)
		require.Equal(t, tc.wantMax, gotMax, "%s 上限", tc.model)
	}
}

// grok-imagine-video 必须先于 grok-video 判定，否则前缀会被误匹配成 toAPI 的区间，
// 直连 xAI 的 1 秒请求会被当成 6 秒计费（多收）。
func TestVideoDurationBounds_PrefixOrderNotConfused(t *testing.T) {
	xaiMin, xaiMax := videoDurationBoundsForModel("grok-imagine-video-1.5")
	toapiMin, toapiMax := videoDurationBoundsForModel("grok-video-1.5")
	require.NotEqual(t, [2]int{xaiMin, xaiMax}, [2]int{toapiMin, toapiMax},
		"两个上游区间不同，前缀匹配不能把它们混为一谈")
	require.Equal(t, 1, xaiMin, "grok-imagine-video 被 grok-video 前缀吃掉了")
}

// toAPI 上游允许 30 秒，就必须按 30 秒收钱。
// 曾经上限卡在 15：用户请求 30 秒、上游生成 30 秒，我们只收一半，且超限是静默收敛。
func TestVideoBillingDuration_ToapiUpperBoundNotTruncated(t *testing.T) {
	require.Equal(t, 30, NormalizeVideoBillingDurationForModel("grok-video-1.5", 30))
	require.Equal(t, 30, NormalizeVideoBillingDurationForModel("grok-video-1.5", 999),
		"超过上游上限收敛到上游上限")
}

// toAPI 拒绝 <6 秒，任何成功生成的视频都 ≥6 秒，故下限收敛到 6 而不是原值。
func TestVideoBillingDuration_ToapiLowerBound(t *testing.T) {
	for _, tooShort := range []int{1, 3, 5} {
		require.Equal(t, 6, NormalizeVideoBillingDurationForModel("grok-video-1.5", tooShort))
	}
	require.Equal(t, 6, NormalizeVideoBillingDurationForModel("grok-video-1.5", 6))
}

// 直连 xAI 的区间不能被 toAPI 的改动带偏：1 秒仍按 1 秒收。
func TestVideoBillingDuration_XaiRangeUnchanged(t *testing.T) {
	require.Equal(t, 1, NormalizeVideoBillingDurationForModel("grok-imagine-video", 1))
	require.Equal(t, 15, NormalizeVideoBillingDurationForModel("grok-imagine-video", 15))
	require.Equal(t, 15, NormalizeVideoBillingDurationForModel("grok-imagine-video", 999))
}

// 未知模型走兵底区间（已知上游的并集）：宁可放宽也不要静默少收——
// 超出上游真实限制的请求会被上游拒掉并走退款，而区间过窄没有任何信号。
func TestVideoBillingDuration_UnknownModelUsesWidestRange(t *testing.T) {
	require.Equal(t, 1, NormalizeVideoBillingDurationForModel("some-future-model", 1))
	require.Equal(t, 30, NormalizeVideoBillingDurationForModel("some-future-model", 999))
	require.Equal(t, 1, NormalizeVideoBillingDurationSecondsOrDefault(1), "不带模型的旧入口等价于兵底区间")
}

// 未指定时长一律按上游默认 8 秒，且 8 秒落在所有已知上游的合法区间内。
func TestVideoBillingDuration_DefaultValidEverywhere(t *testing.T) {
	for _, m := range []string{"grok-imagine-video", "grok-video-1.5", "unknown"} {
		require.Equal(t, 8, NormalizeVideoBillingDurationForModel(m, 0), "%s 未指定走默认", m)
		lo, hi := videoDurationBoundsForModel(m)
		require.GreaterOrEqual(t, VideoBillingDefaultDurationSeconds, lo, "%s 默认值低于下限会被上游拒", m)
		require.LessOrEqual(t, VideoBillingDefaultDurationSeconds, hi, "%s 默认值高于上限", m)
	}
}
