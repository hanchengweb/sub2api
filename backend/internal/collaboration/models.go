package collaboration

import "strings"

// /v1/models does not expose a universal endpoint-capability field. Until it
// does, offer known chat families conservatively instead of treating every
// image, video, audio or embedding catalog entry as a chat-completions model.
// This is a feature compatibility guard, never an authorization substitute.
func textModel(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	for _, excluded := range []string{"image", "video", "audio", "speech", "tts", "whisper", "transcribe", "transcription", "realtime", "embedding", "rerank", "moderation", "sora", "veo", "flux", "dall-e", "imagine"} {
		if strings.Contains(name, excluded) {
			return false
		}
	}
	for _, family := range []string{"gpt-4", "gpt-5", "gpt-6", "o1", "o3", "o4", "claude-", "deepseek-", "grok-3", "grok-4", "gemini-2", "gemini-3", "qwen3", "qwen2.5", "glm-4", "glm-5", "kimi-"} {
		if strings.HasPrefix(name, family) {
			return true
		}
	}
	return false
}
