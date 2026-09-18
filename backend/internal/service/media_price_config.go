package service

func imagePriceConfigFromAPIKey(apiKey *APIKey) *ImagePriceConfig {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return &ImagePriceConfig{
		Price1K: apiKey.Group.ImagePrice1K,
		Price2K: apiKey.Group.ImagePrice2K,
		Price4K: apiKey.Group.ImagePrice4K,
	}
}

func apiKeyHasConfiguredImagePrice(apiKey *APIKey, imageSize string) bool {
	return apiKey != nil && apiKey.Group != nil && apiKey.Group.GetImagePrice(imageSize) != nil
}

// videoPriceConfigFromAPIKey 取该模型的每秒单价配置。
//
// 按模型的价优先于分组三列：三列是全分组一个价，各上游每秒成本差到 60 倍
// （grok 8 / seedance-2 4K 500），共用一个单价等于除最便宜那个以外全部倒贴。
// 按模型配了就四档全用它的，不跟分组三列混着取——混取会配出
// 「720p 用模型价、1080p 用分组价」这种谁也说不清的组合。
func videoPriceConfigFromAPIKey(apiKey *APIKey, model string) *VideoPriceConfig {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	g := apiKey.Group
	if byModel := g.VideoModelPricesFor(model); byModel != nil {
		return byModel
	}
	return &VideoPriceConfig{
		Price480P:  g.VideoPrice480P,
		Price720P:  g.VideoPrice720P,
		Price1080P: g.VideoPrice1080P,
		// 4K 没有独立的分组列，回落到 1080p 而不是 nil：nil 会掉到代码默认价，
		// 那张表是美元口径的历史值，当积分用等于白送。
		Price4K: g.VideoPrice1080P,
	}
}

func apiKeyHasConfiguredVideoPrice(apiKey *APIKey, model, resolution string) bool {
	if apiKey == nil || apiKey.Group == nil {
		return false
	}
	if apiKey.Group.GetVideoModelPrice(model, resolution) != nil {
		return true
	}
	return apiKey.Group.GetVideoPrice(resolution) != nil
}

func webSearchPricePerCallFromAPIKey(apiKey *APIKey) *float64 {
	if apiKey == nil || apiKey.Group == nil {
		return nil
	}
	return apiKey.Group.WebSearchPricePerCall
}
