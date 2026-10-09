package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const mediaQuotaClaimKey = "organization_media_quota_claim"
const mediaQuotaDispatchKey = "organization_media_quota_dispatched"

func mediaQuotaError(c *gin.Context, err error) {
	status, code, message := http.StatusServiceUnavailable, "media_quota_unavailable", "媒体额度暂时无法核对，请稍后重试。"
	switch {
	case errors.Is(err, service.ErrMediaQuotaExceeded):
		status, code, message = 429, "media_quota_exceeded", "图片张数或视频秒数额度不足或已到期，请联系管理员追加额度。"
	case errors.Is(err, service.ErrMediaQuotaConflict):
		status, code, message = 409, "media_quota_conflict", "请求编号已使用或参数已变化，请查询原任务，勿重复生成。"
	case errors.Is(err, service.ErrMediaQuotaReview):
		status, code, message = 409, "media_quota_review_required", "媒体用量需要核对，已暂停新生成，请联系管理员。"
	}
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"type": code, "code": code, "message": message}})
}

// This guard applies after API-key authentication and composite model resolution,
// including direct aliases. Personal accounts retain their current billing.
func (h *OpenAIGatewayHandler) OrganizationMediaQuotaMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key.User == nil || key.User.AccountType != "organization_service" || h.mediaQuota == nil {
			c.Next()
			return
		}
		path := strings.TrimPrefix(c.Request.URL.Path, "/v1")
		if path == "/organization/media-quota" {
			c.Next()
			return
		}
		enabled, err := h.mediaQuota.Enabled(c.Request.Context(), key.UserID)
		if err != nil {
			mediaQuotaError(c, err)
			return
		}
		if !enabled {
			c.Next()
			return
		}
		if strings.EqualFold(c.GetHeader("Upgrade"), "websocket") {
			// Frames can enable image tools after the HTTP handshake. This account
			// uses the bounded HTTP generation contract until frame accounting exists.
			c.AbortWithStatusJSON(422, gin.H{"error": gin.H{"code": "media_quota_requires_http", "message": "当前合同额度请使用 HTTP 聊天或标准媒体生成接口。"}})
			return
		}
		kind := ""
		lookup := false
		if c.Request.Method == http.MethodGet {
			if strings.HasPrefix(path, "/images/generations/") || strings.HasPrefix(path, "/images/tasks/") {
				kind = "image"
				lookup = true
			}
			if strings.HasPrefix(path, "/videos/") && !strings.HasSuffix(path, "/content") {
				kind = "video"
				lookup = true
			}
			if !lookup {
				c.Next()
				return
			}
		} else if c.Request.Method == http.MethodPost {
			switch path {
			case "/images/generations", "/images/edits", "/images/generations/async", "/images/edits/async":
				kind = "image"
			case "/videos/generations":
				kind = "video"
			case "/videos/edits", "/videos/extensions", "/images/batches":
				c.AbortWithStatusJSON(422, gin.H{"error": gin.H{"code": "media_quota_requires_bounded_output", "message": "当前合同额度支持单次图片和指定时长的视频生成，请使用对应生成入口。"}})
				return
			}
		} else {
			c.Next()
			return
		}
		requestID := ""
		var reservation *service.MediaQuotaReservation
		if !lookup {
			if kind == "" && !strings.Contains(path, "responses") && !strings.Contains(path, "chat/completions") && !strings.Contains(path, "messages") && !strings.Contains(path, "models/") {
				c.Next()
				return
			}
			body, e := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
			if e != nil {
				mediaQuotaError(c, e)
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			if kind == "" {
				// Native image tools or media models on chat endpoints have no bounded
				// output contract. Prevent these from bypassing the quantity ledger.
				model := strings.ToLower(gjson.GetBytes(body, "model").String())
				if model == "" {
					model = strings.ToLower(c.Param("modelAction"))
				}
				hasMedia := strings.Contains(model, "image") || strings.Contains(model, "seedream") || strings.Contains(model, "seedance") || strings.Contains(model, "video")
				for _, v := range gjson.GetBytes(body, "tools").Array() {
					hasMedia = hasMedia || v.Get("type").String() == "image_generation"
				}
				for _, v := range gjson.GetBytes(body, "generationConfig.responseModalities").Array() {
					hasMedia = hasMedia || strings.EqualFold(v.String(), "IMAGE")
				}
				if hasMedia {
					c.AbortWithStatusJSON(422, gin.H{"error": gin.H{"code": "media_quota_requires_media_endpoint", "message": "请通过图片或视频生成入口使用媒体额度。"}})
					return
				}
				c.Next()
				return
			}
			if !gjson.ValidBytes(body) || gjson.GetBytes(body, "stream").Bool() {
				c.AbortWithStatusJSON(422, gin.H{"error": gin.H{"code": "media_quota_requires_json", "message": "媒体额度调用须使用非流式 JSON 生成请求。"}})
				return
			}
			model := gjson.GetBytes(body, "model").String()
			// Count/duration aliases and automatic multi-output modes must not exceed
			// the explicit reservation. Only the bounded generation contract is accepted.
			allowed := map[string]bool{"model": true, "prompt": true, "n": true, "response_format": true, "size": true, "resolution": true, "quality": true, "output_format": true, "background": true, "moderation": true, "stream": true, "watermark": true, "metadata": true, "image": true, "image_urls": true, "reference_images": true, "mask": true, "duration": true, "aspect_ratio": true, "video_generation_mode": true, "seed": true}
			validFields := true
			gjson.ParseBytes(body).ForEach(func(k, v gjson.Result) bool {
				if !allowed[k.String()] {
					validFields = false
				}
				return true
			})
			gjson.GetBytes(body, "metadata").ForEach(func(k, v gjson.Result) bool {
				if k.String() != "resolution" {
					validFields = false
				}
				return true
			})
			if !validFields {
				c.AbortWithStatusJSON(422, gin.H{"error": gin.H{"code": "media_quota_unsupported_parameters", "message": "当前额度模式不支持自动多图或未限定产出的参数，请使用标准图片或视频生成设置。"}})
				return
			}
			units := int64(1)
			if kind == "image" {
				n := gjson.GetBytes(body, "n")
				if n.Exists() {
					if n.Type != gjson.Number || n.Float() != float64(n.Int()) || n.Int() < 1 || n.Int() > 10 {
						mediaQuotaError(c, service.ErrMediaQuotaConflict)
						return
					}
					units = n.Int()
				}
			} else {
				if n := gjson.GetBytes(body, "n"); n.Exists() && (n.Type != gjson.Number || n.Float() != 1) {
					mediaQuotaError(c, service.ErrMediaQuotaConflict)
					return
				}
				d := gjson.GetBytes(body, "duration")
				if d.Exists() && (d.Type != gjson.Number || d.Float() != float64(d.Int())) {
					mediaQuotaError(c, service.ErrMediaQuotaConflict)
					return
				}
				units, e = service.MediaQuotaVideoUnits(model, int(d.Int()))
				if e != nil {
					c.AbortWithStatusJSON(422, gin.H{"error": gin.H{"code": "media_quota_fixed_duration_required", "message": "请选择此模型支持的固定视频秒数后生成。"}})
					return
				}
				// Materialize the bounded duration; do not rely on a changing vendor default.
				body, e = sjson.SetBytes(body, "duration", units)
				if e != nil {
					mediaQuotaError(c, e)
					return
				}
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
				c.Request.ContentLength = int64(len(body))
			}
			requestID = strings.TrimSpace(c.GetHeader("X-Client-Request-ID"))
			if len(requestID) < 8 || len(requestID) > 180 || len(model) > 160 {
				mediaQuotaError(c, service.ErrMediaQuotaConflict)
				return
			}
			var payload any
			if e = json.Unmarshal(body, &payload); e != nil {
				mediaQuotaError(c, e)
				return
			}
			canonical, _ := json.Marshal(payload)
			hash := sha256.Sum256(append([]byte(path+":"), canonical...))
			claim := service.MediaQuotaClaim{UserID: key.UserID, APIKeyID: key.ID, RequestID: requestID, Digest: hex.EncodeToString(hash[:]), Kind: kind, Model: model, Units: units}
			reservation, e = h.mediaQuota.Reserve(c.Request.Context(), claim)
			if e != nil {
				mediaQuotaError(c, e)
				return
			}
			if reservation != nil && reservation.Replay {
				if reservation.TaskID != "" {
					c.AbortWithStatusJSON(202, gin.H{"id": reservation.TaskID, "object": "generation.task", "status": reservation.Status, "quota_replay": true})
					return
				}
				mediaQuotaError(c, service.ErrMediaQuotaConflict)
				return
			}
			c.Set(mediaQuotaClaimKey, claim)
		}
		capture := &mediaQuotaWriter{ResponseWriter: c.Writer}
		c.Writer = capture
		c.Next()
		taskID := ""
		if lookup {
			taskID = c.Param("task_id")
			if taskID == "" {
				taskID = c.Param("request_id")
			}
		}
		if reservation != nil || lookup {
			h.recordMediaQuotaOutcome(c, key.UserID, requestID, taskID, kind, capture.Status(), capture.body.Bytes(), capture.truncated)
		}
	}
}

type mediaQuotaWriter struct {
	gin.ResponseWriter
	body      bytes.Buffer
	truncated bool
}

func (w *mediaQuotaWriter) Write(b []byte) (int, error) {
	if !w.truncated {
		if w.body.Len()+len(b) <= 2<<20 {
			w.body.Write(b)
		} else {
			w.truncated = true
			w.body.Reset()
		}
	}
	return w.ResponseWriter.Write(b)
}
func (w *mediaQuotaWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (h *OpenAIGatewayHandler) OrganizationMediaQuota(c *gin.Context) {
	key, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || key.User == nil || key.User.AccountType != "organization_service" {
		c.AbortWithStatus(403)
		return
	}
	if h.mediaQuota == nil {
		mediaQuotaError(c, errors.New("unavailable"))
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	result, err := h.mediaQuota.Summary(c.Request.Context(), key.UserID, page)
	if err != nil {
		mediaQuotaError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, result)
}

func (h *OpenAIGatewayHandler) beginMediaQuotaDispatch(c *gin.Context) bool {
	raw, ok := c.Get(mediaQuotaClaimKey)
	if !ok || h.mediaQuota == nil {
		return true
	}
	claim := raw.(service.MediaQuotaClaim)
	if dispatched, _ := c.Get(mediaQuotaDispatchKey); dispatched == true {
		// A transport error is not evidence that the supplier rejected the work.
		// Do not turn one reservation into multiple paid generation attempts.
		mediaQuotaError(c, service.ErrMediaQuotaConflict)
		return false
	}
	if err := h.mediaQuota.ValidateDispatch(c.Request.Context(), claim.UserID, claim.RequestID); err != nil {
		mediaQuotaError(c, err)
		return false
	}
	c.Set(mediaQuotaDispatchKey, true)
	return true
}

func (h *OpenAIGatewayHandler) recordMediaQuotaOutcome(c *gin.Context, userID int64, requestID, taskID, kind string, status int, body []byte, truncated bool) {
	if h.mediaQuota == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 10*time.Second)
	defer cancel()
	fail := func(err error) {
		if err != nil {
			logger.L().Error("organization_media_quota.receipt_pending", zap.Error(err))
		}
	}
	if status >= 400 {
		dispatched, _ := c.Get(mediaQuotaDispatchKey)
		if requestID != "" && (dispatched != true || status == 400 || status == 401 || status == 403 || status == 404 || status == 422 || status == 429) {
			fail(h.mediaQuota.Settle(ctx, userID, requestID, taskID, 0, "submission_rejected"))
		}
		return
	}
	if truncated {
		if count, ok := c.Get("organization_media_image_count"); ok && kind == "image" && count.(int) > 0 {
			fail(h.mediaQuota.Settle(ctx, userID, requestID, taskID, int64(count.(int)), "provider_output"))
		}
		return
	}
	data := gjson.ParseBytes(bytes.TrimSpace(body))
	if !gjson.ValidBytes(body) {
		return
	}
	state := strings.ToLower(data.Get("status").String())
	if state == "" {
		state = strings.ToLower(data.Get("data.status").String())
	}
	// Gateway async tasks wrap the provider result in result.
	result := data
	if data.Get("result").IsObject() {
		result = data.Get("result")
	}
	if taskID == "" {
		for _, p := range []string{"id", "request_id", "task_id", "data.id", "data.request_id"} {
			if v := data.Get(p).String(); v != "" {
				taskID = v
				break
			}
		}
		if taskID != "" {
			if err := h.mediaQuota.Bind(ctx, userID, requestID, taskID); err != nil {
				fail(err)
				return
			}
		}
	}
	if kind == "image" {
		count := service.MediaQuotaImageUnits([]byte(result.Raw))
		if count > 0 {
			// Partial output still consumed capacity even if another output failed.
			fail(h.mediaQuota.Settle(ctx, userID, requestID, taskID, count, "provider_output"))
			return
		}
	}
	switch state {
	case "failed", "error", "cancelled", "canceled", "expired":
		if strings.Contains(c.Request.URL.Path, "/images/tasks/") {
			return
		}
		// Gateway timeout/local execution errors do not prove provider rejection.
		if data.Get("error.code").String() == "timeout_error" || data.Get("error.type").String() == "timeout_error" {
			return
		}
		fail(h.mediaQuota.Settle(ctx, userID, requestID, taskID, 0, "provider_failure"))
		return
	}
	if kind == "video" {
		completed := state == "completed" || state == "succeeded" || state == "success" || state == "done" || state == "ready"
		if !completed {
			return
		}
		seconds := float64(0)
		for _, p := range []string{"video.duration", "duration", "duration_seconds", "usage.video_duration_seconds", "data.duration"} {
			if d := result.Get(p).Float(); d > 0 {
				seconds = d
				break
			}
		}
		if seconds > 0 && seconds <= 86400 {
			fail(h.mediaQuota.Settle(ctx, userID, requestID, taskID, int64(math.Ceil(seconds)), "provider_output"))
		} else {
			// Accepted fixed-duration requests have a contractual output length. Keep
			// this billing basis distinct from a measured duration in the receipt.
			fail(h.mediaQuota.Settle(ctx, userID, requestID, taskID, -1, "fixed_duration"))
		}
	}
}
