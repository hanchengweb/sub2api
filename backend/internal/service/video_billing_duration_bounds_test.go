//go:build unit

package service

import (
	"strings"
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
		{"kling-v3", 3, 15},               // toAPI，模型说明页参数表
		{"kling-v3-omni", 3, 15},          // 同上
		{"seedance-2-fast", 4, 15},        // toAPI，模型说明页参数表
		{"seedance-2-5", 4, 30},           // 同上
		{"seedance-2", 4, 15},             // 同上
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

// 表里任何一条都不能被排在它前面的短前缀截胡——那条就永远匹配不到，
// 该模型会静默按别的模型的区间和默认值计费。以后加模型排错了位置，这里直接报出来。
func TestVideoUpstreamLimits_NoEntryShadowed(t *testing.T) {
	for j, later := range videoUpstreamLimits {
		for _, earlier := range videoUpstreamLimits[:j] {
			require.Falsef(t, strings.HasPrefix(later.Prefix, earlier.Prefix),
				"%q 排在 %q 后面，永远匹配不到：长前缀要排在短前缀前面", later.Prefix, earlier.Prefix)
		}
		got, ok := videoUpstreamLimitForModel(later.Prefix)
		require.True(t, ok)
		require.Equal(t, later.Prefix, got.Prefix)
	}
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

// 请求没带参数时按上游默认值计费：kling 上游默认 5 秒、720P（toAPI 模型说明页）。
// 按 8 秒、4K 收就是对没带参数的请求多收（4K 档每秒成本 210，720P 只有 42）。
func TestVideoBillingDefaultsFollowUpstream(t *testing.T) {
	require.Equal(t, 5, NormalizeVideoBillingDurationForModel("kling-v3-omni", 0))
	require.Equal(t, 15, NormalizeVideoBillingDurationForModel("kling-v3-omni", 20))
	require.Equal(t, 3, NormalizeVideoBillingDurationForModel("kling-v3-omni", 2))
	require.Equal(t, 8, NormalizeVideoBillingDurationForModel("grok-video-1.5", 0), "其它模型不受影响")

	require.Equal(t, VideoBillingResolution720P, NormalizeVideoBillingResolutionForModel("kling-v3-omni", ""))
	require.Equal(t, VideoBillingResolution720P, NormalizeVideoBillingResolutionForModel("kling-v3", "  "))
	require.Equal(t, VideoBillingResolution1080P, NormalizeVideoBillingResolutionForModel("kling-v3-omni", "1080P"))
	// 带了但认不出来：仍按最贵档兜底，不因为上游有默认值就放宽。
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionForModel("kling-v3-omni", "2k"))
	// 不知道上游默认值的模型照旧按最贵档。
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionForModel("grok-video-1.5", ""))
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionForModel("", ""))
}

// 端到端：客户端没带分辨率和时长的 kling 请求，解析出来就是上游默认值。
func TestParseGrokMediaRequestKlingDefaults(t *testing.T) {
	info := ParseGrokMediaRequest("application/json", []byte(`{"model":"kling-v3-omni","prompt":"x"}`))
	require.Equal(t, VideoBillingResolution720P, info.Resolution)
	require.Equal(t, 5, info.DurationSeconds)

	info = ParseGrokMediaRequest("application/json", []byte(`{"model":"kling-v3-omni","prompt":"x","resolution":"4K","duration":10}`))
	require.Equal(t, VideoBillingResolution4K, info.Resolution, "显式 4K 照收 4K")
	require.Equal(t, 10, info.DurationSeconds)
}

// seedance 的上游默认值：两个方向都会错。
//   - seedance-2-fast 默认 5 秒：B 端租户不带时长，按 8 秒收是每条多收 3 秒
//   - seedance-2-5 默认 30 秒：按 8 秒收是每条少收 22 秒
func TestVideoBillingSeedanceDefaultsFollowUpstream(t *testing.T) {
	require.Equal(t, 5, NormalizeVideoBillingDurationForModel("seedance-2-fast", 0))
	require.Equal(t, 30, NormalizeVideoBillingDurationForModel("seedance-2-5", 0))
	require.Equal(t, VideoBillingResolution720P, NormalizeVideoBillingResolutionForModel("seedance-2-fast", ""))
	require.Equal(t, VideoBillingResolution720P, NormalizeVideoBillingResolutionForModel("seedance-2-5", ""))
	require.Equal(t, VideoBillingResolution1080P, NormalizeVideoBillingResolutionForModel("seedance-2-5", "1080p"))

	// -1 是上游的「自动时长」：上游自己定，最长可能出到上限，按上限收。
	require.Equal(t, 15, NormalizeVideoBillingDurationForModel("seedance-2-fast", -1))
	require.Equal(t, 30, NormalizeVideoBillingDurationForModel("seedance-2-5", -1))
	// 上游没声明支持自动的模型，-1 照旧按默认值。
	require.Equal(t, 5, NormalizeVideoBillingDurationForModel("kling-v3-omni", -1))
	require.Equal(t, 8, NormalizeVideoBillingDurationForModel("grok-video-1.5", -1))

	// seedance-2 基础款：默认 5 秒、720p，-1 按 15 秒；显式 4k 照收 4k。
	require.Equal(t, 5, NormalizeVideoBillingDurationForModel("seedance-2", 0))
	require.Equal(t, 15, NormalizeVideoBillingDurationForModel("seedance-2", -1))
	require.Equal(t, 4, NormalizeVideoBillingDurationForModel("seedance-2", 2))
	require.Equal(t, VideoBillingResolution720P, NormalizeVideoBillingResolutionForModel("seedance-2", ""))
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionForModel("seedance-2", "4k"))
}

// 端到端：B 端租户发的 seedance-2-fast 请求不带分辨率和时长，解析出来是 720p、5 秒。
func TestParseGrokMediaRequestSeedanceDefaults(t *testing.T) {
	info := ParseGrokMediaRequest("application/json", []byte(`{"model":"seedance-2-fast","prompt":"x"}`))
	require.Equal(t, VideoBillingResolution720P, info.Resolution)
	require.Equal(t, 5, info.DurationSeconds)

	info = ParseGrokMediaRequest("application/json", []byte(`{"model":"seedance-2-5","prompt":"x","duration":-1}`))
	require.Equal(t, 30, info.DurationSeconds)

	info = ParseGrokMediaRequest("application/json", []byte(`{"model":"seedance-2","prompt":"x"}`))
	require.Equal(t, VideoBillingResolution720P, info.Resolution)
	require.Equal(t, 5, info.DurationSeconds)
}
