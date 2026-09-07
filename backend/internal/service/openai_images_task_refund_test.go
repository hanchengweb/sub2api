//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 只认明确的失败终态：pending/running/completed 误判成失败会把没失败的任务也退掉。
func TestOpenAIImageTaskFailed_OnlyTerminalFailures(t *testing.T) {
	failed := []string{
		`{"status":"failed"}`,
		`{"status":"FAILED"}`,
		`{"status":"error"}`,
		`{"status":"cancelled"}`,
		`{"status":"canceled"}`,
	}
	for _, body := range failed {
		require.True(t, OpenAIImageTaskFailed([]byte(body)), "应判为失败: %s", body)
	}

	notFailed := []string{
		`{"status":"pending"}`,
		`{"status":"running"}`,
		`{"status":"completed"}`,
		`{"status":""}`,
		`{"data":[{"url":"https://x"}]}`, // 无 status 字段
		`not json`,
		``,
	}
	for _, body := range notFailed {
		require.False(t, OpenAIImageTaskFailed([]byte(body)), "不应判为失败: %s", body)
	}
}

// 扣费记录的键必须区分用户与密钥：同一个上游 task_id 落到别人头上会退错账。
func TestOpenAIImageTaskChargeHash_ScopedAndDistinct(t *testing.T) {
	base := OpenAIImageTaskChargeHash("tsk_1", 6, 9)
	require.NotEmpty(t, base)

	require.NotEqual(t, base, OpenAIImageTaskChargeHash("tsk_2", 6, 9), "不同任务应不同键")
	require.NotEqual(t, base, OpenAIImageTaskChargeHash("tsk_1", 7, 9), "不同用户应不同键")
	require.NotEqual(t, base, OpenAIImageTaskChargeHash("tsk_1", 6, 10), "不同密钥应不同键")

	// 与「任务→账号」绑定分属不同键空间，避免互相覆盖。
	require.NotEqual(t, OpenAIImageTaskSessionHash("tsk_1", 6, 9), base)

	// 参数不合法时返回空串，调用方据此跳过。
	require.Empty(t, OpenAIImageTaskChargeHash("", 6, 9))
	require.Empty(t, OpenAIImageTaskChargeHash("tsk_1", 0, 9))
	require.Empty(t, OpenAIImageTaskChargeHash("tsk_1", 6, 0))
}

// 金额经 int64 缓存往返后必须还原到积分的可用精度。
func TestOpenAIImageChargeScale_RoundTrip(t *testing.T) {
	for _, credits := range []float64{39.5, 135.71, 59, 0.05785695, 29.33} {
		scaled := int64(credits*openAIImageChargeScale + 0.5)
		require.InDelta(t, credits, float64(scaled)/openAIImageChargeScale, 1e-6)
	}
}
