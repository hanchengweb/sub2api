//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 视频状态查询要能识别终态失败，否则提交阶段扣的积分退不回来。
func TestGrokMediaUsageFromResponse_VideoStatusDetectsFailure(t *testing.T) {
	info := GrokMediaRequestInfo{}

	failed := grokMediaUsageFromResponse(GrokMediaEndpointVideoStatus, info, []byte(`{"status":"failed"}`))
	require.True(t, failed.TaskFailed)

	for _, body := range []string{
		`{"status":"pending"}`,
		`{"status":"processing"}`,
		`{"status":"completed"}`,
		`{"url":"https://x/video.mp4"}`,
	} {
		got := grokMediaUsageFromResponse(GrokMediaEndpointVideoStatus, info, []byte(body))
		require.False(t, got.TaskFailed, "不应判为失败: %s", body)
	}
}

// 状态查询不计费：不能因为解析出 TaskFailed 就顺带产生用量。
func TestGrokMediaUsageFromResponse_VideoStatusHasNoUsage(t *testing.T) {
	got := grokMediaUsageFromResponse(GrokMediaEndpointVideoStatus, GrokMediaRequestInfo{}, []byte(`{"status":"failed"}`))
	require.Zero(t, got.VideoCount)
	require.Zero(t, got.ImageCount)
	require.Empty(t, got.ResponseID)
}

// 视频生成沿用 ImageCount 这个遗留计数器，退款绑定正是靠它触发——这条断言防止
// 它被"清理"掉后，视频的失败退款静默失效。
func TestGrokMediaUsageFromResponse_VideoGenerationKeepsLegacyCounter(t *testing.T) {
	got := grokMediaUsageFromResponse(
		GrokMediaEndpointVideosGenerations,
		GrokMediaRequestInfo{Resolution: "720p", DurationSeconds: 5},
		[]byte(`{"request_id":"req_abc"}`),
	)
	require.Equal(t, 1, got.VideoCount)
	require.Equal(t, 1, got.ImageCount, "视频生成必须保留 ImageCount=1，退款绑定依赖它")
	require.Equal(t, 5, got.VideoDurationSeconds)
	require.False(t, got.TaskFailed)
}
