package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// ToAPIs 任务 Webhook 的请求头与事件类型。
const (
	ToAPIsWebhookHeaderID        = "X-ToAPIs-Webhook-Id"
	ToAPIsWebhookHeaderTimestamp = "X-ToAPIs-Webhook-Timestamp"
	ToAPIsWebhookHeaderSignature = "X-ToAPIs-Webhook-Signature"

	ToAPIsWebhookEventCompleted = "generation.completed"
	ToAPIsWebhookEventFailed    = "generation.failed"
	ToAPIsWebhookEventTest      = "endpoint.test"

	// 首次收到事件时，与本地时间相差超过该阈值即拒绝（防重放）。
	toAPIsWebhookMaxSkew = 5 * time.Minute
)

// ToAPIsWebhookEvent 是 Webhook 事件体。字段取自官方文档的 payload 定义，
// 只解析我们会用到的部分。
type ToAPIsWebhookEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		TaskID   string `json:"task_id"`
		TaskType string `json:"task_type"`
		Model    string `json:"model"`
		Status   string `json:"status"`
	} `json:"data"`
}

// IsTerminalFailure 判断该事件是否为「最终失败」。
//
// 只有内部重试结束、上游计费/退款已完成时才会发 generation.failed，
// 暂时性失败不会触发——所以收到即可安全退款，不必再查状态。
func (e *ToAPIsWebhookEvent) IsTerminalFailure() bool {
	if e == nil {
		return false
	}
	return e.Type == ToAPIsWebhookEventFailed
}

// VerifyToAPIsWebhookSignature 校验 ToAPIs Webhook 签名。
//
// 签名原文必须是「未经解析、未经重新序列化」的原始请求体：
//
//	HMAC-SHA256(secret, event_id + "." + timestamp + "." + raw_body)  → 小写十六进制
//
// signature 头形如 `v1=<当前签名>[,v1=<上一签名>]`：密钥轮换后 24 小时内会同时带两个，
// 任一匹配即通过，所以 secrets 允许传多个。
//
// 注意不要照抄官方文档的 Go 示例：那段里 hex.DecodeString(hex.EncodeToString(mac.Sum(nil)))
// 是一次空转，且时间戳的双向比较写得容易看错。这里直接比十六进制串。
func VerifyToAPIsWebhookSignature(eventID, timestamp, signatureHeader string, rawBody []byte, secrets []string, now time.Time) bool {
	if eventID == "" || timestamp == "" || signatureHeader == "" {
		return false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return false
	}
	skew := now.Unix() - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(toAPIsWebhookMaxSkew.Seconds()) {
		return false
	}

	received := parseToAPIsSignatureHeader(signatureHeader)
	if len(received) == 0 {
		return false
	}

	signed := make([]byte, 0, len(eventID)+len(timestamp)+2+len(rawBody))
	signed = append(signed, eventID...)
	signed = append(signed, '.')
	signed = append(signed, timestamp...)
	signed = append(signed, '.')
	signed = append(signed, rawBody...)

	for _, secret := range secrets {
		if strings.TrimSpace(secret) == "" {
			continue
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(signed)
		expected := hex.EncodeToString(mac.Sum(nil))
		for _, candidate := range received {
			// 定长比较，避免按字节短路泄漏信息。
			if hmac.Equal([]byte(expected), []byte(candidate)) {
				return true
			}
		}
	}
	return false
}

// parseToAPIsSignatureHeader 拆出所有 v1= 签名值（小写十六进制）。
func parseToAPIsSignatureHeader(header string) []string {
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "v1=") {
			continue
		}
		if value := strings.TrimPrefix(part, "v1="); value != "" {
			out = append(out, strings.ToLower(value))
		}
	}
	return out
}

// ToAPIs Webhook 的配置键。密钥在 ToAPIs 控制台生成，仅显示一次。
const (
	SettingToAPIsWebhookEnabled  = "toapis_webhook_enabled"
	SettingToAPIsWebhookSecret   = "toapis_webhook_secret"
	SettingToAPIsWebhookSecretV0 = "toapis_webhook_secret_previous"
)

// ToAPIsWebhookConfig 返回 Webhook 是否启用与可用于验签的密钥集合。
//
// 轮换期（24 小时内）上游会同时带当前和上一密钥的两个签名，所以这里把两把都返回，
// 任一匹配即通过；上一密钥留空则只用当前密钥。
func (s *SettingService) ToAPIsWebhookConfig(ctx context.Context) (enabled bool, secrets []string) {
	if s == nil || s.settingRepo == nil {
		return false, nil
	}
	vals, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingToAPIsWebhookEnabled, SettingToAPIsWebhookSecret, SettingToAPIsWebhookSecretV0,
	})
	if err != nil {
		return false, nil
	}
	if strings.TrimSpace(vals[SettingToAPIsWebhookEnabled]) != "true" {
		return false, nil
	}
	for _, key := range []string{SettingToAPIsWebhookSecret, SettingToAPIsWebhookSecretV0} {
		if v := strings.TrimSpace(vals[key]); v != "" {
			secrets = append(secrets, v)
		}
	}
	// 开关开着但没配密钥时视为未启用：绝不能因为「没配密钥」就放行未验签的请求。
	if len(secrets) == 0 {
		return false, nil
	}
	return true, secrets
}
