package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const actualSunburstUsageFixture = `{"input_tokens":19,"output_tokens":824,"total_tokens":843,"input_tokens_details":{"text_tokens":19,"image_tokens":0,"cached_tokens":0,"cached_tokens_details":{"text_tokens":0,"image_tokens":0}},"output_tokens_details":{"text_tokens":0,"image_tokens":824}}`

func TestImageCacheActualSupplierUsage(t *testing.T) {
	u, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":` + actualSunburstUsageFixture + `}`))
	require.True(t, ok)
	require.NoError(t, validateGPTImage26TokenUsage(u, ok))
	require.True(t, u.CacheReadImageInputTokensKnown)
	require.Zero(t, u.CacheReadImageInputTokens)
	cachePrice := 0.0016
	pricing := &ModelPricing{InputPricePerToken: .004, OutputPricePerToken: .024, ImageInputPricePerToken: .0064, ImageOutputPricePerToken: .024, CacheReadPricePerToken: .001, ImageCacheReadPricePerToken: &cachePrice}
	cost := (&BillingService{}).computeTokenBreakdown(pricing, openAIUsageBillingTokens(u), 1, "", false)
	// 19 text * .004 + 824 image output * .024 = 19.852 credits.
	require.InDelta(t, 19.852, cost.TotalCost, 1e-10)
	inputCost, outputCost, imageInputCost, cacheTextCost, cacheImageCost := .0028, .0168, .00448, .0007, .00112
	accountCost := calculateTokenStatsCost(&ChannelModelPricing{InputPrice: &inputCost, OutputPrice: &outputCost, ImageInputPrice: &imageInputCost, ImageOutputPrice: &outputCost, CacheReadPrice: &cacheTextCost, ImageCacheReadPrice: &cacheImageCost}, openAIUsageBillingTokens(u))
	require.NotNil(t, accountCost)
	require.InDelta(t, 13.8964, *accountCost, 1e-10)
	require.InDelta(t, .3, (cost.TotalCost-*accountCost)/cost.TotalCost, 1e-10)
}

func TestImageCacheMutuallyExclusiveBuckets(t *testing.T) {
	u, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"text_tokens":40,"image_tokens":60,"cached_tokens":50,"cached_tokens_details":{"text_tokens":20,"image_tokens":30}},"output_tokens_details":{"image_tokens":10}}}`))
	require.NoError(t, validateGPTImage26TokenUsage(u, ok))
	tokens := openAIUsageBillingTokens(u)
	require.Equal(t, 50, tokens.InputTokens)
	require.Equal(t, 30, tokens.ImageInputTokens)
	require.Equal(t, 30, tokens.CacheReadImageTokens)
	cachePrice := 16.0
	p := &ModelPricing{InputPricePerToken: 40, ImageInputPricePerToken: 64, OutputPricePerToken: 240, ImageOutputPricePerToken: 240, CacheReadPricePerToken: 10, ImageCacheReadPricePerToken: &cachePrice}
	bd := (&BillingService{}).computeTokenBreakdown(p, tokens, 1, "", false)
	require.Equal(t, 800.0, bd.InputCost)
	require.Equal(t, 1920.0, bd.ImageInputCost)
	require.Equal(t, 480.0, bd.CacheReadImageCost)
	require.Equal(t, 680.0, bd.CacheReadCost)
	require.Equal(t, 5800.0, bd.TotalCost) // image cache is already in CacheReadCost.
	p.ImageCacheReadPricePerToken = nil
	legacy := (&BillingService{}).computeTokenBreakdown(p, tokens, 1, "", false)
	require.Equal(t, 500.0, legacy.CacheReadCost)
	zero := 0.0
	p.ImageCacheReadPricePerToken = &zero
	require.Equal(t, 200.0, (&BillingService{}).computeTokenBreakdown(p, tokens, 1, "", false).CacheReadCost)
}

func TestImageCacheUnknownAndInvalidUsage(t *testing.T) {
	for _, body := range []string{
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":60,"cached_tokens":50}}`,
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":60,"cached_tokens":50,"cached_tokens_details":{"image_tokens":51}}}`,
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":60,"cached_tokens":50,"cached_tokens_details":{"image_tokens":-1}}}`,
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":60,"cached_tokens":50,"cached_tokens_details":{"image_tokens":1.5}}}`,
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":60,"cached_tokens":50,"cached_tokens_details":{"image_tokens":30,"text_tokens":10}}}`,
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":60}}`,
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":99,"cached_tokens":50,"cached_tokens_details":{"image_tokens":0}}}`,
		`{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":0,"cached_tokens":0,"cache_write_tokens":-1}}`,
	} {
		u, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":` + body + `}`))
		require.Error(t, validateGPTImage26TokenUsage(u, ok), body)
	}
	old, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"image_tokens":60,"cached_tokens":50}}}`))
	require.True(t, ok)
	require.False(t, old.CacheReadImageInputTokensKnown)
	require.Equal(t, 60, openAIUsageBillingTokens(old).ImageInputTokens) // legacy unsplit behavior retained.
}

func TestImageCacheMergeExplicitZero(t *testing.T) {
	u := OpenAIUsage{InputTokens: 99, ImageInputTokens: 60, CacheReadInputTokens: 50, CacheReadImageInputTokens: 30, CacheReadImageInputTokensKnown: true}
	mergeOpenAIUsage(&u, []byte(`{"usage":`+actualSunburstUsageFixture+`}`))
	require.True(t, u.CacheReadImageInputTokensKnown)
	require.Zero(t, u.CacheReadImageInputTokens)
	require.Zero(t, u.CacheReadInputTokens)
	require.Zero(t, u.ImageInputTokens)
}

func TestImageCacheAggregateDoesNotInventKnownDetails(t *testing.T) {
	known, ok := extractOpenAIUsageFromJSONBytes([]byte(`{"usage":{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"text_tokens":40,"image_tokens":60,"cached_tokens":50,"cached_tokens_details":{"text_tokens":20,"image_tokens":30}}}}`))
	require.True(t, ok)
	var aggregate OpenAIUsage
	addOpenAIUsage(&aggregate, known)
	addOpenAIUsage(&aggregate, known)
	require.True(t, aggregate.CacheReadImageInputTokensKnown)
	require.Equal(t, 60, aggregate.CacheReadImageInputTokens)
	require.Equal(t, 60, openAIUsageBillingTokens(aggregate).ImageInputTokens)
	addOpenAIUsage(&aggregate, OpenAIUsage{InputTokens: 10, CacheReadInputTokens: 2})
	require.False(t, aggregate.CacheReadImageInputTokensKnown)
	require.Zero(t, aggregate.CacheReadImageInputTokens)
	require.Zero(t, openAIUsageBillingTokens(aggregate).CacheReadImageTokens)
	// Unknown values are never used as a partial exact cache bucket.
	partial := known
	partial.CacheReadImageInputTokensKnown = false
	require.Zero(t, openAIUsageBillingTokens(partial).CacheReadImageTokens)
}

func TestImageCachePriceResolverAndTimeScaling(t *testing.T) {
	price := .0016
	channel := ChannelModelPricing{ImageCacheReadPrice: &price}
	resolved := &ResolvedPricing{}
	(&ModelPricingResolver{}).applyTokenOverrides(&channel, resolved)
	require.Equal(t, price, *resolved.BasePricing.ImageCacheReadPricePerToken)
	applyTimePricing(&channel, &TimePricing{OffPeakMultiplier: .5, Timezone: "UTC", PeakWindows: []PeakWindow{{Start: "09:00", End: "10:00"}}}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	require.Equal(t, .0008, *channel.ImageCacheReadPrice)
	require.Equal(t, .0016, price) // never mutate the cached pointer.
	negative := -1.0
	channel.ImageCacheReadPrice = &negative
	require.Error(t, checkPricesNotNegative(channel))
}

func TestImageCacheSharedPricingConsumers(t *testing.T) {
	input, imageInput, cacheText, imageCache, output := 40.0, 64.0, 10.0, 16.0, 240.0
	p := ChannelModelPricing{InputPrice: &input, OutputPrice: &output, ImageInputPrice: &imageInput, CacheReadPrice: &cacheText, ImageCacheReadPrice: &imageCache, ImageOutputPrice: &output}
	tokens := UsageTokens{InputTokens: 50, ImageInputTokens: 30, CacheReadTokens: 50, CacheReadImageTokens: 30, OutputTokens: 10, ImageOutputTokens: 10}
	require.Equal(t, 5800.0, *calculateTokenStatsCost(&p, tokens))
	p.Intervals = []PricingInterval{{InputPrice: &input, OutputPrice: &output, CacheReadPrice: &cacheText}}
	require.Equal(t, 5800.0, *calculateTokenStatsCost(&p, tokens))
	clone := cloneChannelModelPricing(&p)
	require.NotSame(t, p.ImageCacheReadPrice, clone.ImageCacheReadPrice)
	*clone.ImageCacheReadPrice = 8
	require.Equal(t, 16.0, *p.ImageCacheReadPrice)
	require.False(t, plazaPricingEquivalent(&p, &clone))
	require.Equal(t, p.ImageCacheReadPrice, agentCostFieldValue(&p, "image_cache_read"))
	var found bool
	for _, field := range agentPricedFields(&clone, AgentCategoryText) {
		if field.name == "image_cache_read" {
			found = true
			require.Equal(t, clone.ImageCacheReadPrice, field.value)
		}
	}
	require.True(t, found)
	require.False(t, pricingNeedsFallback(&ChannelModelPricing{ImageCacheReadPrice: &imageCache}))
}

func TestImageCacheStrictResponseDoesNotDeliverUnpricedImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"private-image"}]}`))}
	_, _, _, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesNonStreamingResponse(resp, c, true)
	require.Error(t, err)
	require.Equal(t, 502, r.Code)
	require.NotContains(t, r.Body.String(), "private-image")
	require.Contains(t, r.Body.String(), "do_not_retry")
}

func TestImageCacheLegacyImageResponseStillPasses(t *testing.T) {
	r := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(r)
	resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"old"}]}`))}
	_, count, _, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesNonStreamingResponse(resp, c)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, 200, r.Code)
}
