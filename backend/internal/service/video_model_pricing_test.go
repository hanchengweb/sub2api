package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func fl(v float64) *float64 { return &v }

// 各上游每秒成本差到 60 倍（grok 8 / seedance-2 4K 500）。分组三列是全分组一个价，
// 配了它就把所有视频模型按同一个单价收——除最便宜那个以外全部倒贴。
func TestVideoModelPriceBeatsGroupFlatPrice(t *testing.T) {
	g := &Group{
		VideoPrice480P:  fl(37),
		VideoPrice720P:  fl(37),
		VideoPrice1080P: fl(37),
		VideoModelPrices: map[string]map[string]float64{
			"seedance-2": {"480p": 74, "720p": 119, "1080p": 254, "4k": 529},
		},
	}
	key := &APIKey{Group: g}

	cfg := videoPriceConfigFromAPIKey(key, "seedance-2")
	require.NotNil(t, cfg)
	require.Equal(t, 74.0, *cfg.Price480P)
	require.Equal(t, 254.0, *cfg.Price1080P)
	require.Equal(t, 529.0, *cfg.Price4K)

	// 没单独配的模型仍然走分组三列
	fallback := videoPriceConfigFromAPIKey(key, "grok-video-1.5")
	require.NotNil(t, fallback)
	require.Equal(t, 37.0, *fallback.Price480P)
}

// 按模型配了就四档全用它的，不跟分组三列混着取——混取会配出
// 「720p 用模型价、1080p 用分组价」这种谁也说不清的组合。
func TestVideoModelPriceDoesNotMixWithGroupColumns(t *testing.T) {
	g := &Group{
		VideoPrice1080P:  fl(37),
		VideoModelPrices: map[string]map[string]float64{"kling-v3": {"480p": 71}},
	}
	cfg := videoPriceConfigFromAPIKey(&APIKey{Group: g}, "kling-v3")
	require.NotNil(t, cfg)
	require.Equal(t, 71.0, *cfg.Price480P)
	// 不借用分组的 37，而是补成该模型自己已配档位里最贵的 71。
	// 留 nil 会掉到美元口径的代码默认价（0.25/秒），当积分用等于白送。
	require.Equal(t, 71.0, *cfg.Price1080P)
}

// 上游价目表经常只给两档（seedance-2-5 只有 480P/720P）。没配到的档位
// 必须补成已配档位里最贵的，否则一旦有人点到 1080p/4K 就是全额倒贴。
func TestPartialModelPriceFillsMissingTiersWithTopPrice(t *testing.T) {
	g := &Group{VideoModelPrices: map[string]map[string]float64{
		"seedance-2-5": {"480p": 89.27, "720p": 165},
	}}
	cfg := videoPriceConfigFromAPIKey(&APIKey{Group: g}, "seedance-2-5")
	require.NotNil(t, cfg)
	require.Equal(t, 89.27, *cfg.Price480P)
	require.Equal(t, 165.0, *cfg.Price720P)
	require.Equal(t, 165.0, *cfg.Price1080P, "没配的档位补最贵的，不能掉到默认价")
	require.Equal(t, 165.0, *cfg.Price4K)
}

// 认不出来的分辨率归最贵档。上游按真实分辨率收我们钱、我们按 480p 收用户钱
// 是静默亏损，没有任何信号；多收会被投诉，至少可发现。
func TestUnknownResolutionBillsAtTopTier(t *testing.T) {
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionOrDefault(""))
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionOrDefault("2k"))
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionOrDefault("4K"))
	require.Equal(t, VideoBillingResolution4K, NormalizeVideoBillingResolutionOrDefault("2160p"))
	// 认得出的照旧
	require.Equal(t, VideoBillingResolution480P, NormalizeVideoBillingResolutionOrDefault("480p"))
	require.Equal(t, VideoBillingResolution720P, NormalizeVideoBillingResolutionOrDefault("hd"))
	require.Equal(t, VideoBillingResolution1080P, NormalizeVideoBillingResolutionOrDefault("fhd"))
}

// 4K 没有独立的分组列，回落到 1080p 而不是 nil：nil 会掉到代码里那张
// 美元口径的默认价表，当积分用等于白送。
func TestGroupFlatPriceCoversFourKViaTopColumn(t *testing.T) {
	g := &Group{VideoPrice480P: fl(37), VideoPrice720P: fl(37), VideoPrice1080P: fl(90)}
	cfg := videoPriceConfigFromAPIKey(&APIKey{Group: g}, "some-video-model")
	require.NotNil(t, cfg)
	require.Equal(t, 90.0, *cfg.Price4K)
	require.Equal(t, 90.0, *g.GetVideoPrice("4k"))
	require.Equal(t, 90.0, *g.GetVideoPrice("完全不认识的值"))
}

// 计费总价 = 每秒价 × 时长 × 条数
func TestCalculateVideoCostUsesPerModelPrice(t *testing.T) {
	svc := &BillingService{}
	g := &Group{VideoModelPrices: map[string]map[string]float64{
		"seedance-2": {"480p": 74, "4k": 529},
	}}
	cfg := videoPriceConfigFromAPIKey(&APIKey{Group: g}, "seedance-2")

	cost := svc.CalculateVideoCost("seedance-2", "4k", 1, 8, cfg, 1)
	require.NotNil(t, cost)
	require.InDelta(t, 529*8, cost.TotalCost, 0.001)

	cheap := svc.CalculateVideoCost("seedance-2", "480p", 1, 8, cfg, 1)
	require.InDelta(t, 74*8, cheap.TotalCost, 0.001)
}

// Exercise the real auth snapshot, Redis JSON round-trip and gateway billing path.
func TestVideoModelPricingSurvivesAuthCache(t *testing.T) {
	svc := &APIKeyService{}
	gid := int64(4)
	key := &APIKey{ID: 1, UserID: 1, GroupID: &gid, User: &User{ID: 1}, Group: &Group{
		ID: gid, VideoPrice480P: fl(37), VideoPrice720P: fl(37), VideoPrice1080P: fl(37),
		VideoModelPrices: map[string]map[string]float64{
			"kling-v3-omni": {"720p": 71}, "grok-video-1.5": {"480p": 37},
			"seedance-2": {"480p": 74, "720p": 119, "1080p": 254, "4k": 529},
		},
	}}
	raw, err := json.Marshal(svc.snapshotFromAPIKey(context.Background(), key))
	require.NoError(t, err)
	var snapshot APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(raw, &snapshot))
	cached := svc.snapshotToAPIKey("test-key", &snapshot)
	require.Equal(t, key.Group.VideoModelPrices, cached.Group.VideoModelPrices)
	gateway := &OpenAIGatewayService{billingService: &BillingService{}}
	for _, tc := range []struct {
		model, resolution string
		want              float64
	}{
		{"kling-v3-omni", "720p", 568}, {"kling-v3-omni", "480p", 568},
		{"grok-video-1.5", "480p", 296}, {"seedance-2", "4k", 4232},
	} {
		t.Run(tc.model+tc.resolution, func(t *testing.T) {
			result := &OpenAIForwardResult{UpstreamModel: tc.model, VideoCount: 1, VideoResolution: tc.resolution, VideoDurationSeconds: 8}
			cost := gateway.calculateOpenAIVideoCost(context.Background(), tc.model, cached, result, 1)
			require.InDelta(t, tc.want, cost.ActualCost, 0.001)
		})
	}
	_, ok, err := svc.applyAuthCacheEntry("test-key", &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: 17}})
	require.NoError(t, err)
	require.False(t, ok, "old Redis snapshots must be reloaded from the database")
}
