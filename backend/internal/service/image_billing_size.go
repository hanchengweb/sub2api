package service

import (
	"sort"
	"strconv"
	"strings"
)

const (
	ImageBillingSize1K = "1K"
	ImageBillingSize2K = "2K"
	ImageBillingSize4K = "4K"

	ImageSizeSourceOutput  = "output"
	ImageSizeSourceInput   = "input"
	ImageSizeSourceDefault = "default"
	ImageSizeSourceLegacy  = "legacy"
)

type ImageBillingSizeResolution struct {
	BillingSize string
	InputSize   string
	OutputSize  string
	Source      string
	Breakdown   map[string]int
}

func ClassifyImageBillingTier(size string) (string, bool) {
	trimmed := strings.TrimSpace(size)
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case "", "auto":
		return "", false
	case "1k":
		return ImageBillingSize1K, true
	case "2k":
		return ImageBillingSize2K, true
	case "4k":
		return ImageBillingSize4K, true
	case "2048x2048", "2048x1152":
		return ImageBillingSize2K, true
	case "3840x2160", "2160x3840":
		return ImageBillingSize4K, true
	}

	width, height, ok := parseImageBillingDimensions(trimmed)
	if !ok {
		return "", false
	}
	maxEdge := width
	if height > maxEdge {
		maxEdge = height
	}
	switch {
	case maxEdge <= 1024:
		return ImageBillingSize1K, true
	case maxEdge <= 2048:
		return ImageBillingSize2K, true
	default:
		return ImageBillingSize4K, true
	}
}

func NormalizeImageBillingTierOrDefault(size string) string {
	// 已经是「清晰度·质量」的完整档位标签就原样返回。
	//
	// 计费链路上这个函数会被调用两次：一次把请求参数归一成档位，另一次在结算前
	// 拿 result.ImageSize 再归一一遍。第二次拿到的已经是算好的标签，而带质量后缀的
	// "1K·高" 走 ClassifyImageBillingTier 解析不出来，会被打回默认 2K——
	// 于是不管请求什么清晰度什么质量，最终都按 2K 收费。线上实测就是这么发现的。
	if base := BaseImageBillingTier(size); base != size {
		if _, ok := ClassifyImageBillingTier(base); ok {
			return size
		}
	}
	if tier, ok := ClassifyImageBillingTier(size); ok {
		return tier
	}
	return ImageBillingSize2K
}

// ResolveImageBillingTier 按「上游实际按哪个字段计费」判定图片档位。
//
// 两套口径共存：
//
//	OpenAI：size 就是像素尺寸（"1024x1024"），没有 resolution。
//	toAPI：size 是宽高比（"1:1"），档位在 resolution（"1k"）。
//
// resolution 能解析时优先，因为那正是上游拿来计费的字段；两者矛盾时跟着上游走才不会错账。
// 都解析不出则回落 2K 默认。
//
// 不加这个回落时，客户端传 resolution="1k" 会因为 size="1:1" 解析不出像素而
// 默认成 2K：上游按 1K 收我们的钱，我们却按 2K 收用户的钱。
func ResolveImageBillingTier(size, resolution string) string {
	if tier, ok := ClassifyImageBillingTier(resolution); ok {
		return tier
	}
	return NormalizeImageBillingTierOrDefault(size)
}

func ResolveImageBillingSize(inputSize string, outputSizes []string) ImageBillingSizeResolution {
	inputSize = strings.TrimSpace(inputSize)
	outputSizes = compactTrimmedStrings(outputSizes)

	breakdown := map[string]int{}
	outputSize := firstDisplayImageOutputSize(outputSizes)
	outputTier := ""
	for _, output := range outputSizes {
		tier, ok := ClassifyImageBillingTier(output)
		if !ok {
			continue
		}
		breakdown[tier]++
		if imageTierRank(tier) > imageTierRank(outputTier) {
			outputTier = tier
		}
	}
	if outputTier != "" {
		return ImageBillingSizeResolution{
			BillingSize: outputTier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceOutput,
			Breakdown:   normalizeImageSizeBreakdown(breakdown),
		}
	}

	if tier, ok := ClassifyImageBillingTier(inputSize); ok {
		return ImageBillingSizeResolution{
			BillingSize: tier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceInput,
		}
	}

	return ImageBillingSizeResolution{
		BillingSize: ImageBillingSize2K,
		InputSize:   inputSize,
		OutputSize:  outputSize,
		Source:      ImageSizeSourceDefault,
	}
}

// hasClassifiableImageSize 判断这批尺寸里有没有能解析成档位的。
// 有就说明上游真回了图片尺寸，应当按实际产出计费，而不是按请求档位。
func hasClassifiableImageSize(sizes []string) bool {
	for _, sz := range sizes {
		if _, ok := ClassifyImageBillingTier(strings.TrimSpace(sz)); ok {
			return true
		}
	}
	return false
}

// IsExplicitImageBillingTier 判断标签是否来自「请求显式指定的清晰度 + 质量」。
//
// ImageBillingTierWithQuality 只要质量能归一（含空值→低）就一定会加质量后缀，
// 所以带后缀 = 这个档位是按请求参数算出来的，而不是按返回图片像素反推的。
func IsExplicitImageBillingTier(label string) bool {
	base := BaseImageBillingTier(label)
	if base == label {
		return false
	}
	_, ok := ClassifyImageBillingTier(base)
	return ok
}

func ApplyOpenAIImageBillingResolution(result *OpenAIForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	// 优先级：实际输出像素 > 显式 resolution 档位 > 输入像素 > 默认。
	//
	// 上游真回了图片尺寸时按它算最准（请求 2K 却返回 4K 就该按 4K 收），所以
	// 有可用输出尺寸时不拦。拿不到输出尺寸时，才保留请求里显式算出的档位——
	// toAPI 的 size 是宽高比（"1:1"）永远解析不出像素，此时按像素反推只会落到
	// 默认 2K，把算好的档位覆盖掉。线上实测正是如此：请求 resolution=1k
	// quality=high，最终记成 2K、按 2K 收费（image_size_source=default）。
	if IsExplicitImageBillingTier(strings.TrimSpace(result.ImageSize)) &&
		!hasClassifiableImageSize(result.ImageOutputSizes) &&
		!hasClassifiableImageSize([]string{result.ImageOutputSize}) {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	resolved := ResolveImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func ApplyForwardImageBillingResolution(result *ForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	// 优先级：实际输出像素 > 显式 resolution 档位 > 输入像素 > 默认。
	//
	// 上游真回了图片尺寸时按它算最准（请求 2K 却返回 4K 就该按 4K 收），所以
	// 有可用输出尺寸时不拦。拿不到输出尺寸时，才保留请求里显式算出的档位——
	// toAPI 的 size 是宽高比（"1:1"）永远解析不出像素，此时按像素反推只会落到
	// 默认 2K，把算好的档位覆盖掉。线上实测正是如此：请求 resolution=1k
	// quality=high，最终记成 2K、按 2K 收费（image_size_source=default）。
	if IsExplicitImageBillingTier(strings.TrimSpace(result.ImageSize)) &&
		!hasClassifiableImageSize(result.ImageOutputSizes) &&
		!hasClassifiableImageSize([]string{result.ImageOutputSize}) {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	resolved := ResolveImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func applyImageBillingResolution(
	billingSize *string,
	inputSize *string,
	outputSize *string,
	source *string,
	breakdown *map[string]int,
	resolved ImageBillingSizeResolution,
) {
	*billingSize = resolved.BillingSize
	*inputSize = resolved.InputSize
	*outputSize = resolved.OutputSize
	*source = resolved.Source
	*breakdown = resolved.Breakdown
}

func parseImageBillingDimensions(size string) (int, int, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func compactTrimmedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstDisplayImageOutputSize(outputSizes []string) string {
	for _, output := range outputSizes {
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func imageTierRank(tier string) int {
	switch strings.ToUpper(strings.TrimSpace(tier)) {
	case ImageBillingSize1K:
		return 1
	case ImageBillingSize2K:
		return 2
	case ImageBillingSize4K:
		return 3
	default:
		return 0
	}
}

func normalizeImageSizeBreakdown(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for _, tier := range []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K} {
		if count := in[tier]; count > 0 {
			out[tier] = count
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func SortedImageBillingBreakdownKeys(breakdown map[string]int) []string {
	keys := make([]string, 0, len(breakdown))
	for key := range breakdown {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := imageTierRank(keys[i]), imageTierRank(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	return keys
}

// --- 质量维度 ---

const (
	ImageQualityLow    = "低"
	ImageQualityMedium = "中"
	ImageQualityHigh   = "高"

	// imageTierQualitySep 连接清晰度与质量的分隔符。
	// 用「·」而不是「-」：模型名里本来就有大量连字符，混用会让标签难读也难切。
	imageTierQualitySep = "·"
)

// NormalizeImageQuality 把客户端传的 quality 归一到计费用的质量档。
//
// 第二个返回值为 false 表示这个取值我们无法定价：
//   - "auto" 让上游自行决定档位，我们事先不知道会被收哪一档的钱，
//     按任何一档计费都可能错（低估就亏，高估就多收用户的钱）；
//   - 其它未知取值同理。
//
// 空值按低质量处理：线上历史记录的成本都等于低质量价（推断，未向上游求证）。
func NormalizeImageQuality(quality string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "", "low", "standard":
		return ImageQualityLow, true
	case "medium", "mid":
		return ImageQualityMedium, true
	case "high", "hd":
		return ImageQualityHigh, true
	default:
		return "", false
	}
}

// ImageBillingTierWithQuality 拼出「清晰度·质量」的计费档位标签，如 "2K·高"。
//
// 质量归一不了时退回纯清晰度档——调用方应当已经用 NormalizeImageQuality 拦过，
// 这里只是不让脏值拼进标签。
func ImageBillingTierWithQuality(tier, quality string) string {
	tier = strings.TrimSpace(tier)
	if tier == "" {
		return ""
	}
	q, ok := NormalizeImageQuality(quality)
	if !ok {
		return tier
	}
	return tier + imageTierQualitySep + q
}

// BaseImageBillingTier 去掉质量后缀，返回纯清晰度档。
//
// 查价时先按「清晰度·质量」精确匹配，未命中再退到纯清晰度档：
// 只有 gpt-image-2-official / -vip 这类模型才分质量档，其余模型的档位仍是
// "1K"/"2K"/"4K"。有了这层回退，不分质量的模型不需要为每个质量各配一条。
func BaseImageBillingTier(label string) string {
	if idx := strings.Index(label, imageTierQualitySep); idx > 0 {
		return label[:idx]
	}
	return label
}
