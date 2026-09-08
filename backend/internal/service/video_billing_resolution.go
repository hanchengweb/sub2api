package service

import "strings"

const (
	VideoBillingResolution480P  = "480p"
	VideoBillingResolution720P  = "720p"
	VideoBillingResolution1080P = "1080p"
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

// videoDurationBoundsForModel 返回该模型在上游的合法时长区间。
//
// 注意前缀顺序：grok-imagine-video 必须先判，否则会被 grok-video 误匹配。
func videoDurationBoundsForModel(model string) (int, int) {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "grok-imagine-video"):
		return xaiVideoMinDurationSeconds, xaiVideoMaxDurationSeconds
	case strings.HasPrefix(m, "grok-video"):
		return toapiVideoMinDurationSeconds, toapiVideoMaxDurationSeconds
	default:
		return VideoBillingMinDurationSeconds, VideoBillingMaxDurationSeconds
	}
}

// NormalizeVideoBillingDurationSecondsOrDefault 归一化计费用视频时长（不知模型时用兵底区间）。
func NormalizeVideoBillingDurationSecondsOrDefault(durationSeconds int) int {
	return NormalizeVideoBillingDurationForModel("", durationSeconds)
}

// NormalizeVideoBillingDurationForModel 按模型的上游区间归一化计费时长：
// 未指定（<=0）按上游默认 8 秒计，超出区间按边界收敛。
func NormalizeVideoBillingDurationForModel(model string, durationSeconds int) int {
	if durationSeconds <= 0 {
		return VideoBillingDefaultDurationSeconds
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

func NormalizeVideoBillingResolutionOrDefault(resolution string) string {
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480", "480p", "sd":
		return VideoBillingResolution480P
	case "720", "720p", "hd":
		return VideoBillingResolution720P
	case "1080", "1080p", "full_hd", "full-hd", "fhd":
		return VideoBillingResolution1080P
	default:
		return VideoBillingResolution480P
	}
}
