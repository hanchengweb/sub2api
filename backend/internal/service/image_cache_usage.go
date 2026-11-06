package service

import (
	"fmt"
	"math"
	"strings"

	"github.com/tidwall/gjson"
)

// cacheReadImageInputTokens is a subset of both image input and total cache
// input. Presence is retained because omitted cache detail means unknown.
// Image generation usage documents cached_tokens_details.cached_image_tokens;
// input_tokens_details.cached_tokens_details.image_tokens is the breakdown
// confirmed by the ToAPI 2.6 generation task response.
func applyOpenAIImageCacheUsage(value gjson.Result, usage *OpenAIUsage) {
	if usage == nil {
		return
	}
	read := func(path string) (int, bool) {
		v := value.Get(path)
		if !v.Exists() {
			return 0, false
		}
		n := v.Float()
		if v.Type != gjson.Number || n < 0 || n > math.MaxInt32 || n != math.Trunc(n) {
			usage.TokenDetailsInvalid = true
			return 0, false
		}
		return int(n), true
	}
	first := func(paths ...string) (int, bool) {
		for _, path := range paths {
			if value.Get(path).Exists() {
				return read(path)
			}
		}
		return 0, false
	}
	// Validate totals without changing the legacy parser's alias precedence.
	first("input_tokens", "prompt_tokens")
	first("output_tokens", "completion_tokens")
	if total, known := first("total_tokens"); known && total != usage.InputTokens+usage.OutputTokens {
		usage.TokenDetailsInvalid = true
	}
	if imageOutput, known := first("output_tokens_details.image_tokens", "completion_tokens_details.image_tokens"); known {
		if imageOutput > usage.OutputTokens {
			usage.TokenDetailsInvalid = true
		}
		if textOutput, textKnown := first("output_tokens_details.text_tokens", "completion_tokens_details.text_tokens"); textKnown && imageOutput+textOutput != usage.OutputTokens {
			usage.TokenDetailsInvalid = true
		}
	}
	_, usage.CacheReadInputTokensKnown = first("input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens", "cache_read_input_tokens", "cache_read_tokens", "cached_tokens")
	first("input_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_write_tokens", "input_tokens_details.cache_creation_tokens", "prompt_tokens_details.cache_creation_tokens", "cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens")
	imageInput, imageKnown := first("input_tokens_details.image_tokens", "prompt_tokens_details.image_tokens")
	usage.ImageInputTokensKnown = imageKnown
	if imageKnown {
		usage.ImageInputTokens = imageInput
	}
	if textInput, textKnown := first("input_tokens_details.text_tokens", "prompt_tokens_details.text_tokens"); imageKnown && textKnown && imageInput+textInput != usage.InputTokens {
		usage.TokenDetailsInvalid = true
	}
	imageCache, cacheKnown := first("cached_tokens_details.cached_image_tokens", "input_tokens_details.cached_tokens_details.image_tokens", "prompt_tokens_details.cached_tokens_details.image_tokens")
	textCache, textCacheKnown := first("cached_tokens_details.cached_text_tokens", "input_tokens_details.cached_tokens_details.text_tokens", "prompt_tokens_details.cached_tokens_details.text_tokens")
	if !cacheKnown && usage.CacheReadInputTokensKnown && usage.CacheReadInputTokens == 0 {
		imageCache, cacheKnown = 0, true
	}
	if !cacheKnown && usage.CacheReadInputTokensKnown && imageKnown && imageInput == 0 {
		imageCache, cacheKnown = 0, true
	}
	usage.CacheReadImageInputTokens, usage.CacheReadImageInputTokensKnown = imageCache, cacheKnown
	if cacheKnown && (imageCache > usage.CacheReadInputTokens || (imageKnown && imageCache > imageInput)) {
		usage.TokenDetailsInvalid = true
	}
	if cacheKnown && textCacheKnown && imageCache+textCache != usage.CacheReadInputTokens {
		usage.TokenDetailsInvalid = true
	}
	if usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.CacheReadInputTokens < 0 || usage.CacheCreationInputTokens < 0 || imageInput > usage.InputTokens || usage.CacheReadInputTokens+usage.CacheCreationInputTokens > usage.InputTokens {
		usage.TokenDetailsInvalid = true
	}
	if cacheKnown && imageKnown {
		uncachedTotal := usage.InputTokens - usage.CacheReadInputTokens - usage.CacheCreationInputTokens
		if imageInput-imageCache > uncachedTotal {
			usage.TokenDetailsInvalid = true
		}
	}
}

func requiresGPTImage26TokenUsage(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-image-2.6-flare-official", "gpt-image-2.6-sunburst-official":
		return true
	default:
		return false
	}
}

func validateGPTImage26TokenUsage(usage OpenAIUsage, found bool) error {
	if !found || usage.TokenDetailsInvalid || usage.InputTokens <= 0 || usage.OutputTokens <= 0 || usage.CacheCreationInputTokens != 0 ||
		!usage.ImageInputTokensKnown || !usage.CacheReadInputTokensKnown || !usage.CacheReadImageInputTokensKnown {
		return fmt.Errorf("upstream image usage is incomplete or inconsistent; do not retry generation automatically")
	}
	return nil
}

func openAIUsageBillingTokens(usage OpenAIUsage) UsageTokens {
	imageInput := usage.ImageInputTokens
	imageCache := 0
	if usage.CacheReadImageInputTokensKnown {
		imageInput = max(imageInput-usage.CacheReadImageInputTokens, 0)
		imageCache = usage.CacheReadImageInputTokens
	}
	return UsageTokens{
		InputTokens:          max(usage.InputTokens-usage.CacheReadInputTokens-usage.CacheCreationInputTokens, 0),
		ImageInputTokens:     imageInput,
		OutputTokens:         usage.OutputTokens,
		CacheCreationTokens:  usage.CacheCreationInputTokens,
		CacheReadTokens:      usage.CacheReadInputTokens,
		CacheReadImageTokens: imageCache,
		ImageOutputTokens:    usage.ImageOutputTokens,
	}
}
