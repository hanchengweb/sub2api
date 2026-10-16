package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const arkVideoBaseURL = "https://ark.cn-beijing.volces.com/api/v3"

// A provider label or a model name is not proof of the upstream. Only the
// configured official HTTPS endpoint and an API-key account enable this adapter.
func (a *Account) IsArkVideoAccount() bool {
	if a == nil || a.Platform != PlatformOpenAI || a.Type != AccountTypeAPIKey {
		return false
	}
	raw, _ := a.Credentials["base_url"].(string)
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "ark.cn-beijing.volces.com") &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		strings.TrimRight(u.Path, "/") == "/api/v3"
}

var arkVideoTaskID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var arkVideoPromptParameter = regexp.MustCompile(`(?i)--\s*(dur(?:ation)?|frames?|fps|rs|resolution|rt|ratio)\b`)

func buildArkVideoURL(account *Account, cfg *config.Config, endpoint GrokMediaEndpoint, requestID string) (string, error) {
	if !account.IsArkVideoAccount() {
		return "", fmt.Errorf("official Ark account is required")
	}
	if _, err := grokOperatorPolicyValidator(cfg)(arkVideoBaseURL); err != nil {
		return "", fmt.Errorf("Ark base URL rejected by URL security policy")
	}
	base := arkVideoBaseURL + "/contents/generations/tasks"
	switch endpoint {
	case GrokMediaEndpointVideosGenerations:
		return base, nil
	case GrokMediaEndpointVideoStatus:
		if !arkVideoTaskID.MatchString(requestID) {
			return "", fmt.Errorf("invalid Ark task ID")
		}
		return base + "/" + requestID, nil
	default:
		return "", fmt.Errorf("Ark supports video generation and task lookup only")
	}
}

// Translate the gateway's bounded prompt/first-frame contract. Native content,
// frame counts, auto duration, edits and extensions are intentionally rejected:
// they would bypass moderation or the organization's reserved video seconds.
func normalizeSeedanceArkRequest(body []byte, model string) ([]byte, error) {
	if !strings.HasPrefix(model, "doubao-seedance-2-0-") && !strings.HasPrefix(model, "doubao-seedance-2-5-") {
		return nil, fmt.Errorf("unsupported official Seedance model")
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(body, &input); err != nil || input == nil {
		return nil, fmt.Errorf("Seedance requires a JSON object")
	}
	allowed := map[string]bool{"model": true, "prompt": true, "duration": true, "resolution": true, "aspect_ratio": true, "image": true, "video_generation_mode": true, "seed": true, "watermark": true, "n": true}
	for key := range input {
		if !allowed[key] {
			return nil, fmt.Errorf("unsupported Seedance parameter: %s", key)
		}
	}
	var prompt string
	if err := json.Unmarshal(input["prompt"], &prompt); err != nil || strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("Seedance prompt is required")
	}
	if arkVideoPromptParameter.MatchString(prompt) {
		return nil, fmt.Errorf("set video duration, resolution and ratio as JSON fields, not prompt flags")
	}
	duration := 5
	if raw, ok := input["duration"]; ok {
		if err := json.Unmarshal(raw, &duration); err != nil || string(raw) == "null" || duration <= 0 {
			return nil, fmt.Errorf("Seedance requires a fixed positive integer duration")
		}
	}
	if _, err := MediaQuotaVideoUnits(model, duration); err != nil {
		return nil, err
	}
	if raw, ok := input["n"]; ok && string(raw) != "1" {
		return nil, fmt.Errorf("Seedance supports one video per request")
	}
	resolution := "720p"
	if raw, ok := input["resolution"]; ok {
		if err := json.Unmarshal(raw, &resolution); err != nil {
			return nil, fmt.Errorf("invalid Seedance resolution")
		}
		resolution = strings.ToLower(resolution)
	}
	validResolution := resolution == "480p" || resolution == "720p" || resolution == "1080p"
	if strings.HasPrefix(model, "doubao-seedance-2-0-fast-") || strings.HasPrefix(model, "doubao-seedance-2-0-mini-") {
		validResolution = resolution == "480p" || resolution == "720p"
	} else if strings.HasPrefix(model, "doubao-seedance-2-0-") && resolution == "4k" {
		validResolution = true
	}
	if !validResolution {
		return nil, fmt.Errorf("unsupported Seedance resolution")
	}
	ratio := "adaptive"
	if raw, ok := input["aspect_ratio"]; ok {
		if err := json.Unmarshal(raw, &ratio); err != nil {
			return nil, fmt.Errorf("invalid Seedance aspect_ratio")
		}
	}
	switch ratio {
	case "adaptive", "16:9", "4:3", "1:1", "3:4", "9:16", "21:9":
	default:
		return nil, fmt.Errorf("unsupported Seedance aspect_ratio")
	}
	content := []map[string]any{{"type": "text", "text": prompt}}
	if raw, ok := input["image"]; ok {
		var imageURL string
		if json.Unmarshal(raw, &imageURL) != nil {
			var image map[string]string
			if json.Unmarshal(raw, &image) == nil {
				imageURL = firstNonEmpty(image["url"], image["image_url"])
			}
		}
		imageURL = strings.TrimSpace(imageURL)
		if imageURL == "" {
			return nil, fmt.Errorf("invalid Seedance first-frame image")
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}, "role": "first_frame"})
		// Ark 2.5 requires adaptive for first-frame generation. The source image
		// determines the ratio; the tenant's generic default must not reject it.
		if strings.HasPrefix(model, "doubao-seedance-2-5-") {
			ratio = "adaptive"
		}
	}
	if raw, ok := input["video_generation_mode"]; ok {
		var mode string
		if json.Unmarshal(raw, &mode) != nil || (mode != "text_to_video" && mode != "first_frame_image_to_video") ||
			(mode == "first_frame_image_to_video") != (len(content) > 1) {
			return nil, fmt.Errorf("Seedance mode must match text-only or first-frame input")
		}
	}
	payload := map[string]any{"model": model, "content": content, "duration": duration, "resolution": resolution, "ratio": ratio}
	for _, key := range []string{"seed", "watermark"} {
		if raw, ok := input[key]; ok {
			payload[key] = raw
		}
	}
	return json.Marshal(payload)
}

func arkVideoContentURL(body []byte) (string, error) {
	if gjson.GetBytes(body, "status").String() != "succeeded" {
		return "", fmt.Errorf("Ark video is not ready")
	}
	raw := gjson.GetBytes(body, "content.video_url").String()
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") ||
		!strings.EqualFold(u.Hostname(), "ark-content-generation-cn-beijing.tos-cn-beijing.volces.com") {
		return "", fmt.Errorf("Ark returned an unsupported video content URL")
	}
	return u.String(), nil
}

func (s *OpenAIGatewayService) forwardArkVideo(ctx context.Context, c *gin.Context, account *Account, endpoint GrokMediaEndpoint, requestID string, body []byte, contentType string) (*OpenAIForwardResult, error) {
	start := time.Now()
	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}
	if endpoint == GrokMediaEndpointVideoContent {
		return s.forwardGrokMediaVideoContent(ctx, c, account, token, requestID, start)
	}
	targetURL, err := buildArkVideoURL(account, s.cfg, endpoint, requestID)
	if err != nil {
		return nil, err
	}
	info := ParseGrokMediaRequest(contentType, body)
	upstreamModel := account.GetMappedModel(info.Model)
	if endpoint == GrokMediaEndpointVideosGenerations {
		body, err = normalizeSeedanceArkRequest(body, upstreamModel)
		if err != nil {
			writeGrokMediaErrorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return nil, err
		}
	}
	upstreamCtx, release := detachUpstreamContext(ctx)
	defer release()
	req, err := http.NewRequestWithContext(WithHTTPUpstreamRedirectsDisabled(upstreamCtx), endpoint.httpMethod(), targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if endpoint.RequiresRequestBody() {
		req.Header.Set("Content-Type", "application/json")
	}
	account.ApplyHeaderOverrides(req.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer resp.Body.Close()
	upstreamID := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("x-tt-logid"))
	if resp.StatusCode >= 400 {
		return s.handleGrokMediaErrorResponse(ctx, resp, c, account, upstreamID, info.Model)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Ark video redirects are not allowed")
	}
	responseBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if json.Unmarshal(responseBody, &data) != nil || data == nil {
		return nil, fmt.Errorf("invalid Ark video response")
	}
	if endpoint == GrokMediaEndpointVideosGenerations {
		id := gjson.GetBytes(responseBody, "id").String()
		if !arkVideoTaskID.MatchString(id) {
			return nil, fmt.Errorf("Ark did not return a video task ID")
		}
		data["request_id"], data["status"] = id, "queued"
	} else if gjson.GetBytes(responseBody, "status").String() == "expired" {
		// Ark expiry is a terminal provider failure, not a local polling timeout.
		data["status"] = "failed"
	} else if gjson.GetBytes(responseBody, "status").String() == "succeeded" {
		if _, err := arkVideoContentURL(responseBody); err != nil {
			return nil, err
		}
		proxy := grokMediaContentProxyURL(c, requestID)
		data["video"] = map[string]any{"url": proxy, "duration": data["duration"]}
		data["content"].(map[string]any)["video_url"] = proxy
	}
	responseBody, err = json.Marshal(data)
	if err != nil {
		return nil, err
	}
	writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
	usage := grokMediaUsageFromResponse(endpoint, info, responseBody)
	return &OpenAIForwardResult{RequestID: upstreamID, ResponseID: usage.ResponseID, Usage: usage.Usage,
		Model: info.Model, BillingModel: info.Model, UpstreamModel: upstreamModel, ResponseHeaders: resp.Header.Clone(),
		Duration: time.Since(start), TaskFailed: usage.TaskFailed, ImageCount: usage.ImageCount, VideoCount: usage.VideoCount,
		VideoResolution: usage.VideoResolution, VideoDurationSeconds: usage.VideoDurationSeconds}, nil
}
