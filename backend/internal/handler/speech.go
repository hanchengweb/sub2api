package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// SpeechHandler uses the existing account identity; no upstream identifiers or
// credentials are accepted from clients. Audio and input text are not persisted.
type SpeechHandler struct {
	endpoint string
	client   *http.Client
	redis    redis.UniversalClient
}

func NewSpeechHandler(rdb *redis.Client) *SpeechHandler {
	endpoint := strings.TrimRight(os.Getenv("WXM_SPEECH_URL"), "/")
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		endpoint = ""
	}
	return &SpeechHandler{endpoint: endpoint, redis: rdb, client: &http.Client{
		Timeout:       50 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func speechError(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	if status == 429 {
		c.Header("Retry-After", "3")
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message, "type": "speech_error"}})
}

func speechOwner(c *gin.Context) (*service.APIKey, bool) {
	c.Header("Cache-Control", "no-store")
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.UserID <= 0 {
		speechError(c, 401, "speech_auth_required", "请连接账号后使用语音")
		return nil, false
	}
	// Free speech has independent limits, but never extends expired credentials.
	if key.IsExpired() || key.Status == service.StatusAPIKeyExpired {
		speechError(c, 403, "speech_connection_expired", "当前连接已过期，请重新连接")
		return nil, false
	}
	return key, true
}

func (h *SpeechHandler) Voices(c *gin.Context) {
	if _, ok := speechOwner(c); !ok {
		return
	}
	if h.endpoint == "" {
		speechError(c, 503, "speech_unavailable", "自然语音暂未开通")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.endpoint+"/healthz", nil)
	res, err := h.client.Do(req)
	if err != nil {
		speechError(c, 503, "speech_unavailable", "自然语音暂时不可用，请稍后重试")
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		speechError(c, 503, "speech_unavailable", "自然语音正在准备，请稍后重试")
		return
	}
	c.JSON(200, gin.H{"object": "list", "default": "warm", "max_input_characters": 200, "data": []gin.H{
		{"id": "warm", "name": "温柔女声", "description": "自然清晰的女声"},
		{"id": "bright", "name": "清朗男声", "description": "清朗自然的男声"},
	}})
}

// Reservations are per account, not per key, so issuing another key does not
// bypass the free daily budget. Redis failure closes the path rather than
// allowing unbounded work on the shared inference host.
var speechBudget = redis.NewScript(`
local used=tonumber(redis.call('GET',KEYS[1]) or '0')
local rate=tonumber(redis.call('GET',KEYS[2]) or '0')
if used+tonumber(ARGV[1])>10000 or rate>=20 then return 0 end
redis.call('INCRBY',KEYS[1],ARGV[1]); redis.call('EXPIRE',KEYS[1],ARGV[2])
redis.call('INCR',KEYS[2]); redis.call('EXPIRE',KEYS[2],60)
return 1`)

func (h *SpeechHandler) Synthesize(c *gin.Context) {
	key, ok := speechOwner(c)
	if !ok {
		return
	}
	if h.endpoint == "" {
		speechError(c, 503, "speech_unavailable", "自然语音暂未开通")
		return
	}
	var data struct {
		Input          string `json:"input"`
		Voice          string `json:"voice"`
		Model          string `json:"model,omitempty"`
		ResponseFormat string `json:"response_format"`
	}
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 8193))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		speechError(c, 400, "speech_request_invalid", "语音参数无效")
		return
	}
	data.Input = strings.TrimSpace(data.Input)
	n := utf8.RuneCountInString(data.Input)
	if data.Voice == "" {
		data.Voice = "warm"
	}
	if data.ResponseFormat == "" {
		data.ResponseFormat = "mp3"
	}
	if n < 1 || n > 200 || !utf8.ValidString(data.Input) || (data.Voice != "warm" && data.Voice != "bright") || data.ResponseFormat != "mp3" || (data.Model != "" && data.Model != "speech-v1") {
		speechError(c, 400, "speech_request_invalid", "请输入 1 至 200 个字符，并选择可用音色")
		return
	}
	now := time.Now().UTC()
	day := now.Format("20060102")
	ttl := int64(time.Until(now.Truncate(24*time.Hour).Add(24*time.Hour)).Seconds()) + 60
	allowed, err := speechBudget.Run(c.Request.Context(), h.redis, []string{
		fmt.Sprintf("wxm:speech:chars:%d:%s", key.UserID, day),
		fmt.Sprintf("wxm:speech:rate:%d", key.UserID),
	}, n, ttl).Int()
	if err != nil {
		speechError(c, 503, "speech_unavailable", "自然语音暂时不可用，请稍后重试")
		return
	}
	if allowed != 1 {
		speechError(c, 429, "speech_limit", "语音使用较频繁，请稍后重试")
		return
	}
	body, _ := json.Marshal(gin.H{"input": data.Input, "voice": data.Voice, "response_format": "mp3"})
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, h.endpoint+"/synthesize", bytes.NewReader(body))
	if err != nil {
		speechError(c, 503, "speech_unavailable", "自然语音暂时不可用")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := h.client.Do(req)
	if err != nil {
		speechError(c, 503, "speech_unavailable", "语音生成未完成，请稍后重试")
		return
	}
	defer res.Body.Close()
	if res.StatusCode == 429 {
		speechError(c, 429, "speech_busy", "语音服务繁忙，请稍后重试")
		return
	}
	if res.StatusCode != 200 {
		speechError(c, 503, "speech_unavailable", "语音生成未完成，请稍后重试")
		return
	}
	wave, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024+1))
	if err != nil || len(wave) < 4 || len(wave) > 4*1024*1024 || !(string(wave[:3]) == "ID3" || wave[0] == 255 && wave[1]&224 == 224) {
		speechError(c, 503, "speech_unavailable", "语音生成未完成，请稍后重试")
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(200, "audio/mpeg", wave)
}
