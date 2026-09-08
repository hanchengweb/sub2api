//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 记录 SetSessionAccountID 收到的 TTL，用于断言绑定寿命。
type bindingTTLCacheStub struct {
	GatewayCache

	lastKey string
	lastTTL time.Duration
	calls   int
}

func (s *bindingTTLCacheStub) SetSessionAccountID(_ context.Context, _ int64, sessionHash string, _ int64, ttl time.Duration) error {
	s.calls++
	s.lastKey = sessionHash
	s.lastTTL = ttl
	return nil
}

// 视频任务的归属绑定必须与扣费记录同寿（24h），不能用粘性会话的 1 小时。
//
// 这不是调度粘性，而是「这个异步任务属于谁」：状态查询靠它找回原账号，失败退款也靠
// 它才能走到。2026-09-08 线上实例：13:08 提交的视频上游失败（toAPI 收费 0），我们扣了
// 185 积分；15:23 去查状态时绑定已于 14:08 过期，拿到 redis: nil，退款无从触发，而扣费
// 记录还端端正正躺在库里。
func TestBindGrokMediaVideoRequestAccount_UsesTaskBindingTTL(t *testing.T) {
	cache := &bindingTTLCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	gid := int64(4)

	require.NoError(t, svc.BindGrokMediaVideoRequestAccount(
		context.Background(), &gid, "tsk_vid_abc", 6, 9, 42,
	))

	require.Equal(t, 1, cache.calls)
	require.Equal(t, openAIImageTaskBindingTTL, cache.lastTTL,
		"归属绑定要和扣费记录同寿；用 1 小时的粘性会话 TTL 会让超过 1 小时才轮询的失败任务永远退不了款")
	require.Equal(t, 24*time.Hour, cache.lastTTL, "该常量当前应为 24 小时")
}

// 图片与视频两条异步路径的绑定寿命必须一致，否则同样的失败在两边表现不同。
func TestMediaTaskBindings_ShareSameTTL(t *testing.T) {
	gid := int64(4)

	videoCache := &bindingTTLCacheStub{}
	videoSvc := &OpenAIGatewayService{cache: videoCache}
	require.NoError(t, videoSvc.BindGrokMediaVideoRequestAccount(
		context.Background(), &gid, "tsk_vid_abc", 6, 9, 42))

	imageCache := &bindingTTLCacheStub{}
	imageSvc := &OpenAIGatewayService{cache: imageCache}
	require.NoError(t, imageSvc.BindOpenAIImageTaskAccount(
		context.Background(), &gid, "tsk_img_abc", 6, 9, 42))

	require.Equal(t, imageCache.lastTTL, videoCache.lastTTL,
		"视频与图片的任务归属绑定寿命必须一致")
}

// 参数不合法要报错而不是静默写入，否则绑定缺失会无声发生。
func TestBindGrokMediaVideoRequestAccount_RejectsInvalidInput(t *testing.T) {
	cache := &bindingTTLCacheStub{}
	svc := &OpenAIGatewayService{cache: cache}
	gid := int64(4)

	require.Error(t, svc.BindGrokMediaVideoRequestAccount(
		context.Background(), &gid, "tsk_vid_abc", 6, 9, 0), "accountID 为 0 应报错")
	require.Zero(t, cache.calls)
}
