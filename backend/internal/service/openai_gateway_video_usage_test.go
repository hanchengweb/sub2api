//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 视频用量判定必须只看端点信号（VideoCount），不能看模型名。
//
// 2026-09-08 之前这里额外要求模型名前缀是 "grok-imagine-video"，而线上实际跑的是
// grok-video-1.5，于是 19 次真实视频调用全部被当成图片计费：分组 video_price_*
// 从未生效，usage_logs 的视频列全空。这组用例防止该判定重新变回按名字匹配。
func TestIsGrokVideoUsageResult_NameIndependent(t *testing.T) {
	for _, model := range []string{
		"grok-video-1.5",         // 线上实际模型：老实现在这里判 false
		"t-grok-video-1.5",       // 带上游前缀的展示名
		"grok-imagine-video-1.5", // 老实现唯一认得的名字，不能回归
		"sora-2",                 // 将来换上游也不该再改这个判定
		"",
	} {
		result := &OpenAIForwardResult{VideoCount: 1, Model: model, BillingModel: model}
		require.True(t, isGrokVideoUsageResult(result, []string{model}),
			"VideoCount>0 就应判为视频用量，与模型名无关: %q", model)
	}
}

// 没有视频计数就不是视频用量：图片请求不能被误判成按秒计费。
func TestIsGrokVideoUsageResult_RequiresVideoCount(t *testing.T) {
	require.False(t, isGrokVideoUsageResult(nil, nil))
	require.False(t, isGrokVideoUsageResult(&OpenAIForwardResult{}, nil))
	require.False(t, isGrokVideoUsageResult(
		&OpenAIForwardResult{ImageCount: 1, Model: "grok-imagine-video-1.5"}, nil),
		"只有 ImageCount 的结果不是视频用量")
}

// 视频生成会顺带置 ImageCount=1（遗留计数器）。这条用例锁住「视频优先于图片」：
// 两个计数同时为 1 时必须判为视频，否则又会掉回按图计费。
func TestIsGrokVideoUsageResult_VideoWinsOverLegacyImageCounter(t *testing.T) {
	result := &OpenAIForwardResult{VideoCount: 1, ImageCount: 1, Model: "grok-video-1.5"}
	require.True(t, isGrokVideoUsageResult(result, nil))
}
