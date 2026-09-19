package service

import (
	"mime"
	"strings"
	"testing"
)

// 邮件头必须是 ASCII（RFC 5322）。Content-Type 的 charset=UTF-8 只管正文。
// 2026-09-19 线上实测：裸 UTF-8 的中文标题在阿里云 DirectMail 的发送详情里
// 显示成「棕庝恬鎯鸿浣」。
func TestEncodeEmailHeaderWordKeepsASCIIReadable(t *testing.T) {
	for _, s := range []string{"Test Email", "[WindHub] Balance Low Alert", ""} {
		if got := encodeEmailHeaderWord(s); got != s {
			t.Fatalf("纯 ASCII 不该被编码: %q -> %q", s, got)
		}
	}
}

func TestEncodeEmailHeaderWordEncodesNonASCII(t *testing.T) {
	const raw = "[微末智能平台] 余额不足提醒 / Balance Low Alert"
	got := encodeEmailHeaderWord(raw)
	if !strings.HasPrefix(got, "=?UTF-8?b?") && !strings.HasPrefix(got, "=?utf-8?b?") {
		t.Fatalf("非 ASCII 应该编成 RFC 2047 encoded-word，实际: %q", got)
	}
	for _, r := range got {
		if r > 127 {
			t.Fatalf("编码结果里仍有非 ASCII 字节: %q", got)
		}
	}
	// 收件端解回来必须和原文一致
	back, err := new(mime.WordDecoder).DecodeHeader(got)
	if err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if back != raw {
		t.Fatalf("往返不一致:\n  原文 %q\n  解回 %q", raw, back)
	}
}

// 头注入防护不能因为加了编码而丢掉。
func TestEncodeEmailHeaderWordStripsCRLF(t *testing.T) {
	got := encodeEmailHeaderWord("subject\r\nBcc: attacker@example.com")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("CR/LF 没被去掉: %q", got)
	}
}
