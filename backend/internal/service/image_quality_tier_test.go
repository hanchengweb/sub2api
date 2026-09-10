//go:build unit

package service

import "testing"

func TestNormalizeImageQuality(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"":         {ImageQualityLow, true}, // 空值走上游默认档，实测成本等于低质量价
		"low":      {ImageQualityLow, true},
		"LOW":      {ImageQualityLow, true},
		"standard": {ImageQualityLow, true},
		"medium":   {ImageQualityMedium, true},
		"high":     {ImageQualityHigh, true},
		"HD":       {ImageQualityHigh, true},
		// auto 让上游自选档位，我们事先不知道会被收哪一档，按任何一档计费都可能错
		"auto":  {"", false},
		"ultra": {"", false},
	}
	for in, want := range cases {
		got, ok := NormalizeImageQuality(in)
		if got != want.want || ok != want.ok {
			t.Fatalf("NormalizeImageQuality(%q) = (%q,%v), want (%q,%v)", in, got, ok, want.want, want.ok)
		}
	}
}

func TestImageBillingTierWithQuality(t *testing.T) {
	if got := ImageBillingTierWithQuality("2K", "high"); got != "2K·高" {
		t.Fatalf("got %q, want 2K·高", got)
	}
	if got := ImageBillingTierWithQuality("1K", ""); got != "1K·低" {
		t.Fatalf("空 quality 应按低质量档，got %q", got)
	}
	// 归一不了时不把脏值拼进标签
	if got := ImageBillingTierWithQuality("4K", "auto"); got != "4K" {
		t.Fatalf("got %q, want 4K", got)
	}
	if got := ImageBillingTierWithQuality("", "high"); got != "" {
		t.Fatalf("空档位应原样返回空，got %q", got)
	}
}

func TestBaseImageBillingTier(t *testing.T) {
	if got := BaseImageBillingTier("2K·高"); got != "2K" {
		t.Fatalf("got %q, want 2K", got)
	}
	if got := BaseImageBillingTier("2K"); got != "2K" {
		t.Fatalf("无后缀应原样返回，got %q", got)
	}
}

// 分质量档的模型按完整标签命中；不分质量的模型靠回退命中纯清晰度档，
// 不必为每个质量各配一条。
func TestGetTierByLabelFallsBackToBaseTier(t *testing.T) {
	mk := func(v float64) *float64 { return &v }

	withQuality := &ChannelModelPricing{Intervals: []PricingInterval{
		{TierLabel: "2K·低", PerRequestPrice: mk(31.98)},
		{TierLabel: "2K·高", PerRequestPrice: mk(133.0)},
	}}
	if iv := withQuality.GetTierByLabel("2K·高"); iv == nil || *iv.PerRequestPrice != 133.0 {
		t.Fatal("应精确命中 2K·高")
	}

	plain := &ChannelModelPricing{Intervals: []PricingInterval{
		{TierLabel: "2K", PerRequestPrice: mk(43.0)},
	}}
	if iv := plain.GetTierByLabel("2K·高"); iv == nil || *iv.PerRequestPrice != 43.0 {
		t.Fatal("不分质量的模型应回退到纯清晰度档 2K")
	}
	if iv := plain.GetTierByLabel("4K·高"); iv != nil {
		t.Fatal("回退也应只在同一清晰度内，4K 不该命中 2K")
	}
}

// 售价侧与成本侧必须同口径，否则会「按质量档收钱、按清晰度档记成本」。
func TestGetRequestTierPriceFallsBackToBaseTier(t *testing.T) {
	mk := func(v float64) *float64 { return &v }
	r := &ModelPricingResolver{}

	withQuality := &ResolvedPricing{RequestTiers: []PricingInterval{
		{TierLabel: "2K·低", PerRequestPrice: mk(31.98)},
		{TierLabel: "2K·高", PerRequestPrice: mk(133.0)},
	}}
	if got := r.GetRequestTierPrice(withQuality, "2K·高"); got != 133.0 {
		t.Fatalf("got %v, want 133", got)
	}

	plain := &ResolvedPricing{RequestTiers: []PricingInterval{
		{TierLabel: "2K", PerRequestPrice: mk(43.0)},
	}}
	if got := r.GetRequestTierPrice(plain, "2K·高"); got != 43.0 {
		t.Fatalf("应回退到 2K，got %v", got)
	}
	if got := r.GetRequestTierPrice(plain, "4K·高"); got != 0 {
		t.Fatalf("4K 不该命中 2K，got %v", got)
	}
}

// 端到端：请求里的 quality 要进到计费档位标签。
func TestParseGrokMediaRequestCarriesQualityIntoTier(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2-vip","prompt":"cat","size":"1:1","resolution":"2k","quality":"high"}`)
	info := ParseGrokMediaRequest("application/json", body)
	if info.Quality != "high" {
		t.Fatalf("Quality = %q, want high", info.Quality)
	}
	if info.SizeTier != "2K·高" {
		t.Fatalf("SizeTier = %q, want 2K·高", info.SizeTier)
	}

	// 不带 quality 时按低质量档，与上游默认一致
	plain := ParseGrokMediaRequest("application/json",
		[]byte(`{"model":"gpt-image-2","prompt":"cat","size":"1:1","resolution":"1k"}`))
	if plain.SizeTier != "1K·低" {
		t.Fatalf("SizeTier = %q, want 1K·低", plain.SizeTier)
	}
}

// 回归：openai 图片链路原先只看 size，而 toAPI 的 size 是宽高比（"1:1"），
// 解析不出像素就一律落到默认 2K —— 1K 请求按 2K 收费、4K 也按 2K 收费。
// 两条链路（openai_images / grok_media）必须算出同一个档位。
func TestOpenAIImagesRequestUsesResolutionAndQuality(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2-vip","prompt":"cat","size":"1:1","resolution":"1k","quality":"high"}`)
	svc := &OpenAIGatewayService{}
	parsed, err := svc.ParseOpenAIImagesRequest(imagesRequestContext(t, body), body)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if parsed.Resolution != "1k" {
		t.Fatalf("Resolution = %q, want 1k", parsed.Resolution)
	}
	if parsed.SizeTier != "1K·高" {
		t.Fatalf("SizeTier = %q, want 1K·高（原先会算成 2K）", parsed.SizeTier)
	}

	// 与 grok_media 那条链路必须一致
	grok := ParseGrokMediaRequest("application/json", body)
	if grok.SizeTier != parsed.SizeTier {
		t.Fatalf("两条链路档位不一致：openai=%q grok=%q", parsed.SizeTier, grok.SizeTier)
	}
}
