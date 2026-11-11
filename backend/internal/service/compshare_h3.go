package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const CompShareH3Model = "MiniMax-H3"
const compShareH3BaseURL = "https://cp.compshare.cn"
const CompShareH3ContextKey = "compshare_h3_snapshot"

func IsCompShareH3Model(model string) bool { return strings.TrimSpace(model) == CompShareH3Model }

// The endpoint and API-key account jointly select this protocol; model labels alone do not.
func (a *Account) IsCompShareH3Account() bool {
	if a == nil || a.Platform != PlatformGrok || a.Type != AccountTypeAPIKey {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(a.GetCredential("base_url")))
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "cp.compshare.cn") &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && strings.Trim(u.Path, "/") == "" && u.RawQuery == "" && u.Fragment == ""
}

func buildCompShareH3URL(a *Account, cfg *config.Config, endpoint GrokMediaEndpoint, id string) (string, error) {
	if !a.IsCompShareH3Account() {
		return "", errors.New("CompShare H3 account required")
	}
	if _, err := grokOperatorPolicyValidator(cfg)(compShareH3BaseURL); err != nil {
		return "", err
	}
	switch endpoint {
	case GrokMediaEndpointVideosGenerations:
		return compShareH3BaseURL + "/minimax/v2/video_generation", nil
	case GrokMediaEndpointVideoStatus:
		if !arkVideoTaskID.MatchString(id) {
			return "", errors.New("invalid CompShare task ID")
		}
		return compShareH3BaseURL + "/minimax/v2/query/video_generation/" + id, nil
	default:
		return "", errors.New("CompShare H3 supports generation and task lookup only")
	}
}

// NormalizeCompShareH3Request accepts the playground contract and the native content contract.
// All paid optional features stay closed until their prices are configured.
func NormalizeCompShareH3Request(body []byte) ([]byte, error) {
	var in map[string]json.RawMessage
	if json.Unmarshal(body, &in) != nil || in == nil {
		return nil, errors.New("H3 requires a JSON object")
	}
	allowed := map[string]bool{"model": true, "prompt": true, "content": true, "resolution": true, "duration": true, "ratio": true, "aspect_ratio": true, "image": true, "reference_images": true, "n": true, "video_generation_mode": true, "use_context_ir": true, "skill_id": true, "mute_audio": true, "aigc_watermark": true, "watermark": true, "callback_url": false, "callback_token": false}
	for k := range in {
		if !allowed[k] {
			return nil, fmt.Errorf("unsupported H3 parameter: %s", k)
		}
	}
	if gjson.GetBytes(body, "model").String() != CompShareH3Model {
		return nil, errors.New("unsupported CompShare video model")
	}
	if raw, ok := in["use_context_ir"]; ok && string(raw) != "false" {
		return nil, errors.New("H3 prompt optimization is not priced")
	}
	if _, ok := in["skill_id"]; ok {
		return nil, errors.New("H3 Skills are not priced")
	}
	if raw, ok := in["n"]; ok && string(raw) != "1" {
		return nil, errors.New("H3 supports one video per request")
	}
	duration := 5
	if raw, ok := in["duration"]; ok {
		if string(raw) == "null" || json.Unmarshal(raw, &duration) != nil || duration < 4 || duration > 30 {
			return nil, errors.New("H3 duration must be an integer from 4 to 30")
		}
	}
	res := "768P"
	if raw, ok := in["resolution"]; ok {
		if json.Unmarshal(raw, &res) != nil {
			return nil, errors.New("invalid H3 resolution")
		}
		res = strings.ToUpper(strings.TrimSpace(res))
	}
	switch res {
	case "480P", "768P", "1080P", "2K", "4K":
	default:
		return nil, errors.New("H3 resolution must be 480P, 768P, 1080P, 2K or 4K")
	}
	ratio := "16:9"
	if raw, ok := in["aspect_ratio"]; ok {
		if json.Unmarshal(raw, &ratio) != nil {
			return nil, errors.New("invalid H3 aspect ratio")
		}
	}
	if raw, ok := in["ratio"]; ok {
		if json.Unmarshal(raw, &ratio) != nil {
			return nil, errors.New("invalid H3 ratio")
		}
		if a, ok := in["aspect_ratio"]; ok && string(a) != string(raw) {
			return nil, errors.New("conflicting H3 ratios")
		}
	}
	switch ratio {
	case "adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16":
	default:
		return nil, errors.New("unsupported H3 ratio")
	}
	var content []map[string]any
	if raw, ok := in["content"]; ok {
		for _, key := range []string{"prompt", "image", "reference_images"} {
			if _, exists := in[key]; exists {
				return nil, errors.New("use native H3 content or prompt/image fields, not both")
			}
		}
		if json.Unmarshal(raw, &content) != nil || len(content) == 0 {
			return nil, errors.New("invalid H3 content")
		}
	} else {
		var prompt string
		if json.Unmarshal(in["prompt"], &prompt) != nil || strings.TrimSpace(prompt) == "" {
			return nil, errors.New("H3 prompt is required")
		}
		content = append(content, map[string]any{"type": "text", "text": prompt})
		if raw, ok := in["image"]; ok {
			image := grokMediaJSONImageURL(gjson.ParseBytes(raw))
			if image == "" {
				_ = json.Unmarshal(raw, &image)
			}
			if image == "" {
				return nil, errors.New("invalid H3 image")
			}
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image}, "role": "first_frame"})
		}
		for _, image := range gjson.GetBytes(body, "reference_images").Array() {
			imageURL := grokMediaJSONImageURL(image)
			if imageURL == "" && image.Type == gjson.String {
				imageURL = image.String()
			}
			if imageURL == "" {
				return nil, errors.New("invalid H3 reference image")
			}
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}, "role": "reference_image"})
		}
	}
	hasText := false
	for _, item := range content {
		typ, _ := item["type"].(string)
		for key := range item {
			if key != "type" && key != "text" && key != "image_url" && key != "audio_url" && key != "role" {
				return nil, errors.New("unsupported H3 content field")
			}
		}
		switch typ {
		case "text":
			txt, _ := item["text"].(string)
			hasText = hasText || strings.TrimSpace(txt) != ""
		case "image_url", "audio_url":
			asset, ok := item[typ].(map[string]any)
			if !ok {
				return nil, errors.New("invalid H3 media URL")
			}
			raw, _ := asset["url"].(string)
			u, err := url.Parse(raw)
			if len(asset) != 1 || err != nil || (u.Scheme != "https" && !(typ == "image_url" && strings.HasPrefix(raw, "data:image/"))) || u.User != nil || (u.Scheme == "https" && (u.Hostname() == "" || (u.Port() != "" && u.Port() != "443"))) {
				return nil, errors.New("invalid H3 media URL")
			}
			role, _ := item["role"].(string)
			if typ == "audio_url" && role != "reference_audio" {
				return nil, errors.New("invalid H3 audio role")
			}
			if typ == "image_url" && role != "" && role != "first_frame" && role != "last_frame" && role != "reference_image" {
				return nil, errors.New("invalid H3 image role")
			}
		default:
			return nil, errors.New("H3 video references are not priced or content type is unsupported")
		}
	}
	if !hasText {
		return nil, errors.New("H3 text prompt is required")
	}
	out := map[string]any{"model": CompShareH3Model, "content": content, "resolution": res, "duration": duration, "ratio": ratio, "use_context_ir": false}
	for _, key := range []string{"mute_audio", "aigc_watermark"} {
		if raw, ok := in[key]; ok {
			if string(raw) != "true" && string(raw) != "false" {
				return nil, fmt.Errorf("invalid H3 %s", key)
			}
			out[key] = json.RawMessage(raw)
		}
	}
	if raw, ok := in["watermark"]; ok {
		if string(raw) != "true" && string(raw) != "false" {
			return nil, errors.New("invalid H3 watermark")
		}
		out["aigc_watermark"] = json.RawMessage(raw)
	}
	return json.Marshal(out)
}

type CompShareH3Snapshot struct {
	RequestID           string               `json:"request_id"`
	MediaQuotaRequestID string               `json:"media_quota_request_id,omitempty"`
	TaskID              string               `json:"task_id"`
	UserID              int64                `json:"user_id"`
	APIKeyID            int64                `json:"api_key_id"`
	GroupID             int64                `json:"group_id"`
	AccountID           int64                `json:"account_id"`
	PayloadHash         string               `json:"payload_hash"`
	Request             json.RawMessage      `json:"request"`
	Resolution          string               `json:"resolution"`
	Duration            int                  `json:"duration"`
	Prices              map[string]float64   `json:"prices"`
	Multiplier          float64              `json:"multiplier"`
	BaseCost            float64              `json:"base_cost"`
	Credits             float64              `json:"credits"`
	Billing             *UsageBillingCommand `json:"billing,omitempty"`
	Dispatched          bool                 `json:"-"`
	Settled             bool                 `json:"-"`
	TerminalStatus      string               `json:"-"`
}

// The receipt freezes whether this task uses organization seconds. Old receipts
// have no marker and keep their original balance-only accounting on every replay.
func (s *CompShareH3Snapshot) BillingMode() string {
	if s != nil && s.MediaQuotaRequestID != "" {
		return "organization_quota"
	}
	return "balance"
}

type CompShareH3Repository interface {
	GetCompShareH3(context.Context, string, int64, int64) (*CompShareH3Snapshot, error)
	ClaimCompShareH3(context.Context, string) (bool, error)
	BindCompShareH3(context.Context, string, string) error
	SettleCompShareH3(context.Context, string, int64, int64, string, string, float64) (float64, error)
}

func (s *OpenAIGatewayService) PrepareCompShareH3(ctx context.Context, key *APIKey, a *Account, body []byte, id string) (*CompShareH3Snapshot, error) {
	repo, ok := s.usageBillingRepo.(CompShareH3Repository)
	if !ok {
		return nil, errors.New("H3 durable billing unavailable")
	}
	if key == nil || key.User == nil || key.Group == nil || key.GroupID == nil || a == nil || !a.IsCompShareH3Account() || a.GetMappedModel(CompShareH3Model) != CompShareH3Model || strings.TrimSpace(a.GetCredential("api_key")) == "" {
		return nil, errors.New("H3 requires a configured CompShare API-key route")
	}
	if _, err := buildCompShareH3URL(a, s.cfg, GrokMediaEndpointVideosGenerations, ""); err != nil {
		return nil, err
	}
	canonical, err := NormalizeCompShareH3Request(body)
	if err != nil {
		return nil, err
	}
	if len(id) > 128 {
		return nil, errors.New("H3 idempotency key exceeds 128 characters")
	}
	if id == "" {
		id = generateRequestID()
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", key.UserID, key.ID, id)))
	requestID := "csh3-" + hex.EncodeToString(digest[:])[:56]
	snap, err := repo.GetCompShareH3(ctx, requestID, key.UserID, key.ID)
	if err != nil {
		return nil, err
	}
	hash := HashUsageRequestPayload(canonical)
	if snap != nil {
		if snap.PayloadHash != hash {
			return nil, ErrUsageBillingRequestConflict
		}
		return snap, nil
	}
	prices := map[string]float64{}
	for _, res := range []string{"480p", "768p", "1080p", "2k", "4k"} {
		p := key.Group.GetVideoModelPrice(CompShareH3Model, res)
		if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) || *p <= 0 {
			return nil, errors.New("H3 requires configured model prices for all five resolutions")
		}
		prices[res] = *p
	}
	base := s.ResolveUserGroupRateMultiplier(ctx, key.UserID, *key.GroupID, key.Group.RateMultiplier)
	mult := resolveVideoRateMultiplier(key, base)
	if math.IsNaN(mult) || math.IsInf(mult, 0) || mult <= 0 {
		return nil, errors.New("H3 requires a positive video rate multiplier")
	}
	res := strings.ToLower(gjson.GetBytes(canonical, "resolution").String())
	duration := int(gjson.GetBytes(canonical, "duration").Int())
	cost := prices[res] * float64(duration)
	return &CompShareH3Snapshot{RequestID: requestID, UserID: key.UserID, APIKeyID: key.ID, GroupID: *key.GroupID, AccountID: a.ID, PayloadHash: hash, Request: canonical, Resolution: res, Duration: duration, Prices: prices, Multiplier: mult, BaseCost: cost, Credits: cost * mult}, nil
}

func (s *OpenAIGatewayService) ClaimCompShareH3(ctx context.Context, snap *CompShareH3Snapshot) (bool, error) {
	repo, ok := s.usageBillingRepo.(CompShareH3Repository)
	if !ok {
		return false, errors.New("H3 billing unavailable")
	}
	claimed, err := repo.ClaimCompShareH3(ctx, snap.RequestID)
	if errors.Is(err, ErrMediaQuotaExceeded) {
		// Expiry was verified before the dispatch claim. No supplier request was
		// sent, so release both reservations instead of leaving an unknown task.
		if releaseErr := s.settleCompShareH3(ctx, snap, "submission_rejected", "", 0); releaseErr != nil {
			return false, fmt.Errorf("H3 undispatched reservation release failed: %w", releaseErr)
		}
	}
	return claimed, err
}

func (s *OpenAIGatewayService) GetCompShareH3(ctx context.Context, id string, userID, keyID int64) (*CompShareH3Snapshot, error) {
	if s == nil {
		return nil, nil
	}
	repo, ok := s.usageBillingRepo.(CompShareH3Repository)
	if !ok {
		return nil, nil
	}
	return repo.GetCompShareH3(ctx, id, userID, keyID)
}

func compShareH3ContentURL(body []byte) (string, error) {
	if gjson.GetBytes(body, "task.status").String() != "succeeded" {
		return "", errors.New("H3 video is not ready")
	}
	raw := gjson.GetBytes(body, "task.content.url").String()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Hostname() != "compshare-files.cn-wlcb.ufileos.com" {
		return "", errors.New("CompShare returned an unsupported video content URL")
	}
	return u.String(), nil
}

func CompShareH3SettlementAmount(snap *CompShareH3Snapshot, status, res string, seconds float64) (float64, error) {
	switch status {
	case "failed", "cancelled", "blocked", "submission_rejected":
		return 0, nil
	case "succeeded":
	default:
		return 0, errors.New("H3 task is not terminal")
	}
	res = strings.ToLower(res)
	price, ok := snap.Prices[res]
	if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > float64(snap.Duration) {
		return 0, errors.New("H3 terminal usage requires review")
	}
	// Never settle an unexplained upgrade or a different native tier.
	if res != snap.Resolution && !(res == "768p" && (snap.Resolution == "1080p" || snap.Resolution == "2k" || snap.Resolution == "4k")) {
		return 0, errors.New("H3 delivered resolution requires review")
	}
	amount := price * seconds * snap.Multiplier
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 || amount > snap.Credits+0.0000001 {
		return 0, errors.New("H3 terminal cost exceeds reservation")
	}
	return amount, nil
}

func (s *OpenAIGatewayService) settleCompShareH3(ctx context.Context, snap *CompShareH3Snapshot, status, res string, seconds float64) error {
	repo, ok := s.usageBillingRepo.(CompShareH3Repository)
	if !ok {
		return errors.New("H3 billing unavailable")
	}
	refund, err := repo.SettleCompShareH3(ctx, snap.RequestID, snap.UserID, snap.APIKeyID, status, res, seconds)
	if err == nil && s.billingCacheService != nil {
		_ = s.billingCacheService.InvalidateUserBalance(ctx, snap.UserID)
		_ = s.billingCacheService.InvalidateAPIKeyRateLimit(ctx, snap.APIKeyID)
	}
	if err == nil && refund > 0 && s.schedulerSnapshot != nil && s.accountRepo != nil {
		if account, fetchErr := s.accountRepo.GetByID(ctx, snap.AccountID); fetchErr == nil && account != nil {
			_ = s.schedulerSnapshot.UpdateAccountInCache(ctx, account)
		}
	}
	return err
}

func (s *OpenAIGatewayService) forwardCompShareH3(ctx context.Context, c *gin.Context, a *Account, endpoint GrokMediaEndpoint, id string, body []byte, contentType string) (forwardResult *OpenAIForwardResult, forwardErr error) {
	start := time.Now()
	repo, ok := s.usageBillingRepo.(CompShareH3Repository)
	if !ok {
		return nil, errors.New("H3 durable billing unavailable")
	}
	var snap *CompShareH3Snapshot
	if endpoint == GrokMediaEndpointVideosGenerations {
		raw, _ := c.Get(CompShareH3ContextKey)
		snap, _ = raw.(*CompShareH3Snapshot)
		if snap == nil {
			return nil, errors.New("H3 billing reservation required")
		}
		body = snap.Request
	} else {
		raw, _ := c.Get(CompShareH3ContextKey)
		snap, _ = raw.(*CompShareH3Snapshot)
		if snap == nil {
			return nil, errors.New("H3 owner snapshot required")
		}
		if snap.TaskID == "" {
			if snap.Settled {
				c.JSON(200, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "failed", "billing_mode": snap.BillingMode()})
				return &OpenAIForwardResult{}, nil
			}
			c.JSON(202, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "unknown", "message": "Submission receipt is unknown; query this original request only"})
			return &OpenAIForwardResult{}, nil
		}
		id = snap.TaskID
	}
	defer func() {
		if forwardErr != nil && c.Writer.Size() < 0 {
			c.JSON(502, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "unknown", "error": gin.H{"code": "h3_submission_unknown", "message": "Query this original request only; do not resubmit"}})
		}
	}()
	token, _, err := s.getRequestCredential(ctx, c, a)
	if err != nil {
		return nil, err
	}
	if endpoint == GrokMediaEndpointVideoContent {
		return s.forwardGrokMediaVideoContent(ctx, c, a, token, id, start)
	}
	target, err := buildCompShareH3URL(a, s.cfg, endpoint, id)
	if err != nil {
		return nil, err
	}
	SetActualOpenAIUpstreamEndpoint(c, strings.TrimPrefix(target, compShareH3BaseURL))
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	ctx = upstreamCtx
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), endpoint.httpMethod(), target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if endpoint == GrokMediaEndpointVideosGenerations {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", snap.RequestID)
	}
	proxy := ""
	if a.Proxy != nil && a.ProxyID != nil {
		proxy = a.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxy, a.ID, a.Concurrency)
	if err != nil {
		c.JSON(502, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "unknown", "error": gin.H{"code": "h3_submission_unknown", "message": "Continue querying the original request; do not resubmit"}})
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		if endpoint == GrokMediaEndpointVideosGenerations && (resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 422 || resp.StatusCode == 429) {
			if err := s.settleCompShareH3(ctx, snap, "submission_rejected", "", 0); err != nil {
				return nil, err
			}
			c.JSON(resp.StatusCode, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "failed", "error": gin.H{"code": "compshare_submission_rejected", "message": "CompShare rejected the request; the reserved credits were released"}})
		} else {
			c.JSON(502, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "unknown", "error": gin.H{"code": "h3_submission_unknown", "message": "Continue querying the original request; do not resubmit"}})
		}
		return nil, fmt.Errorf("CompShare HTTP %d", resp.StatusCode)
	}
	if !gjson.ValidBytes(raw) {
		return nil, errors.New("invalid CompShare response")
	}
	taskID := gjson.GetBytes(raw, "task.id").String()
	if taskID == "" {
		taskID = gjson.GetBytes(raw, "task_id").String()
	}
	if !arkVideoTaskID.MatchString(taskID) || (endpoint != GrokMediaEndpointVideosGenerations && taskID != snap.TaskID) {
		return nil, errors.New("CompShare task receipt mismatch")
	}
	if endpoint == GrokMediaEndpointVideosGenerations {
		if err := repo.BindCompShareH3(ctx, snap.RequestID, taskID); err != nil {
			return nil, err
		}
		snap.TaskID = taskID
	}
	status := gjson.GetBytes(raw, "task.status").String()
	if status == "" {
		status = "queued"
	}
	if status == "succeeded" {
		if _, err := compShareH3ContentURL(raw); err != nil {
			return nil, err
		}
	}
	resolution := gjson.GetBytes(raw, "task.resolution").String()
	outputSeconds := gjson.GetBytes(raw, "task.usage.output_seconds")
	seconds := outputSeconds.Float()
	if status == "succeeded" && outputSeconds.Type != gjson.Number {
		return nil, errors.New("H3 terminal usage requires a numeric output_seconds receipt")
	}
	switch status {
	case "succeeded", "failed", "cancelled", "blocked":
		if err := s.settleCompShareH3(ctx, snap, status, resolution, seconds); err != nil {
			return nil, err
		}
	}
	task := map[string]any{"id": taskID, "status": status, "resolution": resolution, "usage": map[string]any{"output_seconds": seconds}}
	data := map[string]any{"id": taskID, "request_id": taskID, "status": status, "task": task}
	if status == "failed" || status == "blocked" || status == "cancelled" {
		data["error"] = map[string]string{"code": "h3_task_failed", "message": "H3 task did not complete; reserved credits were released"}
	}
	if status == "blocked" {
		data["status"] = "failed"
	}
	data["billing_mode"] = snap.BillingMode()
	if status == "succeeded" {
		proxyURL := grokMediaContentProxyURL(c, taskID)
		data["url"] = proxyURL
		data["video"] = map[string]any{"url": proxyURL, "duration": seconds}
		data["resolution"] = strings.ToLower(resolution)
		if task, ok := data["task"].(map[string]any); ok {
			task["content"] = map[string]any{"url": proxyURL}
		}
	}
	out, _ := json.Marshal(data)
	writeGrokMediaResponse(c, resp, out, s.responseHeaderFilter)
	return &OpenAIForwardResult{RequestID: snap.RequestID, ResponseID: taskID, Model: CompShareH3Model, UpstreamModel: CompShareH3Model, CompShareH3: snap, Duration: time.Since(start)}, nil
}
