package service

import "strings"

const (
	VideoBillingResolution480P  = "480p"
	VideoBillingResolution720P  = "720p"
	VideoBillingResolution1080P = "1080p"
	VideoBillingResolution4K    = "4k"
)

// 视频按秒计费。计费时长必须与上游实际消耗对齐：
//
//	比上游窄 → 用户拉长 duration 套利（上游生成 30 秒，我们只收 15 秒）
//	比上游宽 → 短视频多收钱
//
// 区间必须按模型分，不能用一组全局常量——不同上游限制不同：
//
//	grok-imagine-video（直连 xAI）      1-15 秒
//	grok-video-1.5（toAPI）            6-30 秒
//	  —— 错误原文 "seconds must be between 6 and 30"（2026-09-08 实测）
const (
	VideoBillingDefaultDurationSeconds = 8

	// 直连 xAI 的 grok-imagine-video。
	xaiVideoMinDurationSeconds = 1
	xaiVideoMaxDurationSeconds = 15

	// toAPI 的 grok-video-1.5。
	toapiVideoMinDurationSeconds = 6
	toapiVideoMaxDurationSeconds = 30
)

// VideoBillingMinDurationSeconds / VideoBillingMaxDurationSeconds 是未知模型的兵底区间，
// 取已知上游的并集。对未知模型宁可放宽：超出上游真实限制的请求会被上游拒掉，
// 任务失败后走退款；而区间卡得太窄会静默少收钱，没有任何信号。
const (
	VideoBillingMinDurationSeconds = xaiVideoMinDurationSeconds
	VideoBillingMaxDurationSeconds = toapiVideoMaxDurationSeconds
)

// videoUpstreamLimit 一个上游视频模型的时长区间，以及请求没带参数时上游的默认值。
//
// DefaultSeconds / DefaultResolution 为零值表示不知道上游默认值，走全局兜底
// （8 秒 / 最贵档）。AutoIsMax 表示上游支持 duration=-1（自动时长）：上游自己定时长，
// 最长可能出到上限，所以按上限计费。
type videoUpstreamLimit struct {
	Prefix            string
	MinSeconds        int
	MaxSeconds        int
	DefaultSeconds    int
	DefaultResolution string
	AutoIsMax         bool
}

// videoUpstreamLimits 按模型前缀匹配，顺序敏感：grok-imagine-video 要排在 grok-video 前面，
// 否则会被误匹配。toAPI 的默认值来自 toapis.com/model-guide/<模型> 的参数表（2026-09-27 核对）。
var videoUpstreamLimits = []videoUpstreamLimit{
	// 直连 xAI。
	{Prefix: "grok-imagine-video", MinSeconds: xaiVideoMinDurationSeconds, MaxSeconds: xaiVideoMaxDurationSeconds},
	// toAPI grok-video-1.5，区间来自 2026-09-08 实测报错。
	{Prefix: "grok-video", MinSeconds: toapiVideoMinDurationSeconds, MaxSeconds: toapiVideoMaxDurationSeconds},
	// kling-v3 / kling-v3-omni：3-15 秒，默认 5 秒、720P。
	{Prefix: "kling-v3", MinSeconds: 3, MaxSeconds: 15, DefaultSeconds: 5, DefaultResolution: VideoBillingResolution720P},
	// seedance-2-fast：4-15 秒，默认 5 秒、720p，-1 为自动。
	{Prefix: "seedance-2-fast", MinSeconds: 4, MaxSeconds: 15, DefaultSeconds: 5, DefaultResolution: VideoBillingResolution720P, AutoIsMax: true},
	// seedance-2-5：4-30 秒，默认 **30 秒**、720p，-1 为自动。没带时长按 8 秒收会每条少收 22 秒。
	{Prefix: "seedance-2-5", MinSeconds: 4, MaxSeconds: 30, DefaultSeconds: 30, DefaultResolution: VideoBillingResolution720P, AutoIsMax: true},
}

func videoUpstreamLimitForModel(model string) (videoUpstreamLimit, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, limit := range videoUpstreamLimits {
		if strings.HasPrefix(m, limit.Prefix) {
			return limit, true
		}
	}
	return videoUpstreamLimit{}, false
}

// videoDurationBoundsForModel 返回该模型在上游的合法时长区间；不认识的模型用兜底区间。
func videoDurationBoundsForModel(model string) (int, int) {
	if limit, ok := videoUpstreamLimitForModel(model); ok {
		return limit.MinSeconds, limit.MaxSeconds
	}
	return VideoBillingMinDurationSeconds, VideoBillingMaxDurationSeconds
}

// NormalizeVideoBillingDurationSecondsOrDefault 归一化计费用视频时长（不知模型时用兵底区间）。
func NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds int) int {
	return NormalizeVideoBillingDurationForModel("", durationSeconds)
}

// NormalizeVideoBillingDurationForModel 按模型的上游区间归一化计费时长：
//   - 自动（-1，仅上游支持时）按区间上限计
//   - 未指定（<=0）按该模型上游的默认时长计，不知道就按 8 秒
//   - 超出区间按边界收敛
func NormalizeVideoBillingDurationForModel(model string, durationSeconds int) int {
	limit, known := videoUpstreamLimitForModel(model)
	if durationSeconds < 0 && known && limit.AutoIsMax {
		return limit.MaxSeconds
	}
	if durationSeconds <= 0 {
		return videoDefaultDurationForModel(model)
	}
	minSeconds, maxSeconds := videoDurationBoundsForModel(model)
	if durationSeconds < minSeconds {
		return minSeconds
	}
	if durationSeconds > maxSeconds {
		return maxSeconds
	}
	return durationSeconds
}

// videoDefaultDurationForModel 请求没带时长时，上游按多少秒出片。
//
// 必须和上游一致，两个方向都会错：kling 上游默认 5 秒、按 8 秒收是多收；
// seedance-2-5 上游默认 30 秒、按 8 秒收是少收。
func videoDefaultDurationForModel(model string) int {
	if limit, ok := videoUpstreamLimitForModel(model); ok && limit.DefaultSeconds > 0 {
		return limit.DefaultSeconds
	}
	return VideoBillingDefaultDurationSeconds
}

// videoDefaultResolutionForModel 请求没带分辨率时上游的默认分辨率；不知道就返回空。
func videoDefaultResolutionForModel(model string) string {
	if limit, ok := videoUpstreamLimitForModel(model); ok {
		return limit.DefaultResolution
	}
	return ""
}

// NormalizeVideoBillingResolutionForModel 按模型归一化计费分辨率。
//
// 请求**没带**分辨率时，上游会按它自己的默认分辨率出片，按最贵档收就是多收——
// kling-v3-omni 上游默认 720P（成本每秒 42），4K 档成本是 210。所以只要知道该模型的
// 上游默认值，没带就按它计。带了但认不出来的值仍按最贵档兜底，不放宽。
// 不认识的模型也照旧按最贵档。
func NormalizeVideoBillingResolutionForModel(model, resolution string) string {
	if strings.TrimSpace(resolution) == "" {
		if def := videoDefaultResolutionForModel(model); def != "" {
			return def
		}
	}
	return NormalizeVideoBillingResolutionOrDefault(resolution)
}

func NormalizeVideoBillingResolutionOrDefault(resolution string) string {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480", "480p", "sd":
		return VideoBillingResolution480P
	case "720", "720p", "hd":
		return VideoBillingResolution720P
	case "1080", "1080p", "full_hd", "full-hd", "fhd":
		return VideoBillingResolution1080P
	case "4k", "2160", "2160p", "uhd", "ultra_hd", "ultra-hd":
		return VideoBillingResolution4K
	default:
		// 认不出来的分辨率归到最贵档，不是最便宜档。
		// 上游按实际分辨率收我们钱，我们按 480p 收用户钱 = 静默亏，没有任何信号；
		// 反过来多收会被用户立刻投诉，至少是可发现的。与本文件时长那段同一条原则。
		return VideoBillingResolution4K
	}
}
