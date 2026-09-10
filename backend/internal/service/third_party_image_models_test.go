//go:build unit

package service

import "testing"

// 闸门原本只认 gpt-image-* 与 grok-imagine*，新供应商一律进不来——
// 线上豆包就是「借名 gpt-image-1」才过的闸。
func TestThirdPartyImageGenerationModelGate(t *testing.T) {
	allowed := []string{
		"flux-2-pro", "flux-kontext-max",
		"nano_banana", "nano_banana_2", "nano_banana_pro",
		"doubao-seedream-5-0-pro", "doubao-seedream-4-0",
		"vidu-image", "viduq2-fast", "viduq3-fast",
		"gemini-3-pro-image-official", "gemini-2.5-flash-image-preview",
		"qwen-image-3.0", "qwen-image-3.0-pro",
	}
	for _, m := range allowed {
		if !isOpenAIImageGenerationModel(m) {
			t.Fatalf("%q 应被允许调用生图接口", m)
		}
	}

	// 同名家族里的对话模型不能被误放行，否则对话请求会走到生图链路上
	blocked := []string{
		"gemini-2.5-pro", "gemini-3-pro", "qwen-max", "qwen3-coder",
		"deepseek-v4-flash", "claude-sonnet", "gpt-4o", "",
	}
	for _, m := range blocked {
		if isOpenAIImageGenerationModel(m) {
			t.Fatalf("%q 不是生图模型，不该放行", m)
		}
	}

	// 原有两类仍然放行
	for _, m := range []string{"gpt-image-2", "gpt-image-2-vip", "grok-imagine", "grok-imagine-image"} {
		if !isOpenAIImageGenerationModel(m) {
			t.Fatalf("%q 是既有生图模型，必须继续放行", m)
		}
	}
}
