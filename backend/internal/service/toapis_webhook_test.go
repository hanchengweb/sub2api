//go:build unit

package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 按官方文档的定义独立算一遍签名，作为对照——不拿被测实现的输出当期望值。
func toapisSign(secret, eventID, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(eventID + "." + timestamp + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyToAPIsWebhookSignature_AcceptsValid(t *testing.T) {
	now := time.Now()
	ts := fmt.Sprintf("%d", now.Unix())
	body := []byte(`{"id":"evt_1","type":"generation.failed"}`)
	sig := toapisSign("s3cret", "evt_1", ts, body)

	require.True(t, VerifyToAPIsWebhookSignature(
		"evt_1", ts, "v1="+sig, body, []string{"s3cret"}, now))
}

// 签名原文必须是原始字节：body 被重新序列化（哪怕语义等价）就应该验不过。
// 这条是为了防止将来有人图省事改成「先解析成结构体再重新 marshal 后验签」。
func TestVerifyToAPIsWebhookSignature_RawBodySensitive(t *testing.T) {
	now := time.Now()
	ts := fmt.Sprintf("%d", now.Unix())
	original := []byte(`{"id":"evt_1", "type":"generation.failed"}`)
	reserialized := []byte(`{"id":"evt_1","type":"generation.failed"}`)
	sig := toapisSign("s3cret", "evt_1", ts, original)

	require.True(t, VerifyToAPIsWebhookSignature("evt_1", ts, "v1="+sig, original, []string{"s3cret"}, now))
	require.False(t, VerifyToAPIsWebhookSignature("evt_1", ts, "v1="+sig, reserialized, []string{"s3cret"}, now),
		"重新序列化后的 body 不该通过——签名必须对原始字节做")
}

// 密钥轮换后 24 小时内会同时发两个 v1 签名，任一有效即通过。
func TestVerifyToAPIsWebhookSignature_AcceptsEitherDuringRotation(t *testing.T) {
	now := time.Now()
	ts := fmt.Sprintf("%d", now.Unix())
	body := []byte(`{"id":"evt_2"}`)
	current := toapisSign("new-secret", "evt_2", ts, body)
	previous := toapisSign("old-secret", "evt_2", ts, body)
	header := "v1=" + current + ", v1=" + previous

	// 只持有旧密钥时，靠头里的第二个签名通过
	require.True(t, VerifyToAPIsWebhookSignature("evt_2", ts, header, body, []string{"old-secret"}, now))
	// 只持有新密钥时，靠第一个通过
	require.True(t, VerifyToAPIsWebhookSignature("evt_2", ts, header, body, []string{"new-secret"}, now))
	// 两个都不持有则拒绝
	require.False(t, VerifyToAPIsWebhookSignature("evt_2", ts, header, body, []string{"unrelated"}, now))
}

// 超出 ±5 分钟的时间戳一律拒绝（防重放），未来时间同样拒绝。
func TestVerifyToAPIsWebhookSignature_RejectsClockSkew(t *testing.T) {
	now := time.Now()
	body := []byte(`{"id":"evt_3"}`)
	for _, offset := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		ts := fmt.Sprintf("%d", now.Add(offset).Unix())
		sig := toapisSign("s3cret", "evt_3", ts, body)
		require.False(t, VerifyToAPIsWebhookSignature("evt_3", ts, "v1="+sig, body, []string{"s3cret"}, now),
			"偏移 %v 应被拒绝", offset)
	}
	// 边界内（4 分钟）仍应通过
	ts := fmt.Sprintf("%d", now.Add(-4*time.Minute).Unix())
	sig := toapisSign("s3cret", "evt_3", ts, body)
	require.True(t, VerifyToAPIsWebhookSignature("evt_3", ts, "v1="+sig, body, []string{"s3cret"}, now))
}

// 篡改任一签名要素都必须失败。
func TestVerifyToAPIsWebhookSignature_RejectsTampering(t *testing.T) {
	now := time.Now()
	ts := fmt.Sprintf("%d", now.Unix())
	body := []byte(`{"id":"evt_4","amount":1}`)
	sig := toapisSign("s3cret", "evt_4", ts, body)

	require.False(t, VerifyToAPIsWebhookSignature("evt_OTHER", ts, "v1="+sig, body, []string{"s3cret"}, now), "换 event id")
	require.False(t, VerifyToAPIsWebhookSignature("evt_4", ts, "v1="+sig, []byte(`{"id":"evt_4","amount":999}`), []string{"s3cret"}, now), "改 body")
	require.False(t, VerifyToAPIsWebhookSignature("evt_4", ts, "v1=deadbeef", body, []string{"s3cret"}, now), "假签名")
	require.False(t, VerifyToAPIsWebhookSignature("evt_4", ts, sig, body, []string{"s3cret"}, now), "缺 v1= 前缀")
}

// 缺失要素或没有配置密钥时一律拒绝，不能因为「没配密钥」就放行。
func TestVerifyToAPIsWebhookSignature_RejectsMissingInputs(t *testing.T) {
	now := time.Now()
	ts := fmt.Sprintf("%d", now.Unix())
	body := []byte(`{}`)
	sig := toapisSign("s3cret", "evt_5", ts, body)

	require.False(t, VerifyToAPIsWebhookSignature("", ts, "v1="+sig, body, []string{"s3cret"}, now))
	require.False(t, VerifyToAPIsWebhookSignature("evt_5", "", "v1="+sig, body, []string{"s3cret"}, now))
	require.False(t, VerifyToAPIsWebhookSignature("evt_5", ts, "", body, []string{"s3cret"}, now))
	require.False(t, VerifyToAPIsWebhookSignature("evt_5", ts, "v1="+sig, body, nil, now), "未配置密钥必须拒绝")
	require.False(t, VerifyToAPIsWebhookSignature("evt_5", ts, "v1="+sig, body, []string{"  "}, now), "空白密钥不算配置")
	require.False(t, VerifyToAPIsWebhookSignature("evt_5", "not-a-number", "v1="+sig, body, []string{"s3cret"}, now))
}

// 只有 generation.failed 算最终失败：completed 与 test 事件都不能触发退款。
func TestToAPIsWebhookEvent_IsTerminalFailure(t *testing.T) {
	require.True(t, (&ToAPIsWebhookEvent{Type: ToAPIsWebhookEventFailed}).IsTerminalFailure())
	require.False(t, (&ToAPIsWebhookEvent{Type: ToAPIsWebhookEventCompleted}).IsTerminalFailure())
	require.False(t, (&ToAPIsWebhookEvent{Type: ToAPIsWebhookEventTest}).IsTerminalFailure())
	require.False(t, (*ToAPIsWebhookEvent)(nil).IsTerminalFailure())
}
