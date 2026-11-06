//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageCacheRecordUsageBillsAndPersistsExclusiveBuckets(t *testing.T) {
	const groupID int64 = 814
	const model = "gpt-image-2.6-sunburst-official"
	input, imageInput, cacheText, imageCache, output := 40.0, 64.0, 10.0, 16.0, 240.0
	price := ChannelModelPricing{Models: []string{model}, BillingMode: BillingModeToken, InputPrice: &input, OutputPrice: &output, ImageInputPrice: &imageInput, CacheReadPrice: &cacheText, ImageCacheReadPrice: &imageCache, ImageOutputPrice: &output}
	costPrice := cloneChannelModelPricing(&price)
	for _, field := range agentPricedFields(&costPrice, AgentCategoryText) {
		if field.value != nil {
			*field.value *= .7
		}
	}
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformOpenAI, model: model}] = &price
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{GroupIDs: []int64{groupID}, Pricing: []ChannelModelPricing{costPrice}}}}
	cache.groupPlatform[groupID] = PlatformOpenAI
	cache.loadedAt = time.Now()
	channels := &ChannelService{}
	channels.cache.Store(cache)
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	ledger := &openAIRecordUsageBillingRepoStub{}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, ledger, userRepo, &openAIRecordUsageSubRepoStub{}, nil)
	svc.channelService = channels
	svc.resolver = NewModelPricingResolver(channels, svc.billingService)
	u, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"text_tokens":40,"image_tokens":60,"cached_tokens":50,"cached_tokens_details":{"text_tokens":20,"image_tokens":30}},"output_tokens_details":{"image_tokens":10}}}`))
	require.NoError(t, validateGPTImage26TokenUsage(u, ok))
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &OpenAIForwardResult{RequestID: "image-cache-record", Model: model, UpstreamModel: model, Usage: u, ImageCount: 1, Duration: time.Second},
		APIKey: &APIKey{ID: 100, GroupID: i64p(groupID), Group: &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1}},
		User:   &User{ID: 200}, Account: &Account{ID: 300},
	})
	require.NoError(t, err)
	require.Equal(t, 1, ledger.calls)
	require.Equal(t, 0, userRepo.deductCalls, "the atomic ledger owns the single deduction")
	require.InDelta(t, 5800.0, ledger.lastCmd.BalanceCost, 1e-9)
	require.Equal(t, 1, logs.calls)
	log := logs.lastLog
	require.Equal(t, 50, log.InputTokens)
	require.Equal(t, 30, log.ImageInputTokens)
	require.Equal(t, 50, log.CacheReadTokens)
	require.Equal(t, 30, log.CacheReadImageTokens)
	require.True(t, log.CacheReadImageTokensKnown)
	require.InDelta(t, 480.0, log.CacheReadImageCost, 1e-9)
	require.InDelta(t, 680.0, log.CacheReadCost, 1e-9)
	require.InDelta(t, 5800.0, log.TotalCost, 1e-9)
	require.NotNil(t, log.AccountStatsCost)
	require.InDelta(t, 4060.0, *log.AccountStatsCost, 1e-9)
}
