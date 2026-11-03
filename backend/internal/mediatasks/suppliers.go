package mediatasks

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type OfficialSupplier struct {
	Client   *http.Client
	Download func(context.Context, string) ([]byte, error)
}

func NewOfficialSupplier() *OfficialSupplier {
	return &OfficialSupplier{Client: &http.Client{Timeout: 120 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Download: DownloadModel}
}
func uncertain(code string) Outcome { return Outcome{Status: "indeterminate", ErrorCode: code} }
func rejected(code string) Outcome {
	z := int64(0)
	return Outcome{Status: "failed", ErrorCode: code, SettlementUnits: &z}
}
func rejectedHTTP(status int) Outcome {
	switch status {
	case 401, 403:
		return rejected("media_provider_unauthorized")
	case 402:
		return rejected("media_provider_balance_insufficient")
	case 429:
		return rejected("media_provider_rate_limited")
	case 400, 404, 405, 413, 415, 422:
		return rejected("media_provider_rejected")
	default:
		return uncertain("media_submit_indeterminate")
	}
}
func (s *OfficialSupplier) Submit(ctx context.Context, p Provider, o Owner, requestID string, r Request) Outcome {
	if p.Kind == Audio {
		return s.speech(ctx, p, o, requestID, r)
	}
	body, _ := json.Marshal(map[string]any{"prompt": r.Prompt, "model": TripoNativeModel, "texture": true, "pbr": true, "geometry_quality": "standard", "texture_quality": "standard", "texture_version": "v3.0-20250812", "quad": false, "generate_parts": false, "face_limit": 50000})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://openapi.tripo3d.com/v3/generation/text-to-model", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+p.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.Client.Do(req)
	if err != nil {
		return uncertain("media_submit_indeterminate")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 400 && res.StatusCode < 500 {
		return rejectedHTTP(res.StatusCode)
	}
	if res.StatusCode != 200 {
		return uncertain("media_submit_indeterminate")
	}
	var receipt struct {
		Code *int `json:"code"`
		Data struct {
			TaskID string `json:"task_id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&receipt) != nil || receipt.Code == nil {
		return uncertain("media_submit_indeterminate")
	}
	if *receipt.Code != 0 {
		if *receipt.Code == 2010 {
			return rejected("media_provider_balance_insufficient")
		}
		return rejected("media_provider_rejected")
	}
	if receipt.Data.TaskID == "" || len(receipt.Data.TaskID) > 256 || strings.ContainsAny(receipt.Data.TaskID, "/\\?#") {
		return uncertain("media_submit_indeterminate")
	}
	return Outcome{Status: "running", ProviderID: receipt.Data.TaskID}
}
func (s *OfficialSupplier) speech(ctx context.Context, p Provider, o Owner, requestID string, r Request) Outcome {
	additions, _ := json.Marshal(map[string]any{"context_texts": []string{ContextTexts(r)}})
	body, _ := json.Marshal(map[string]any{"user": map[string]string{"uid": fmt.Sprintf("org-%d", o.UserID)}, "req_params": map[string]any{"text": r.Input, "speaker": r.Voice, "audio_params": map[string]any{"format": "mp3", "sample_rate": 24000, "speech_rate": (r.Speed - 1) * 100}, "additions": string(additions)}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://openspeech.bytedance.com/api/v3/tts/unidirectional/sse", bytes.NewReader(body))
	req.Header.Set("X-Api-Key", p.Key)
	req.Header.Set("X-Api-Resource-Id", SpeechModel)
	req.Header.Set("X-Api-Request-Id", requestID)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.Client.Do(req)
	if err != nil {
		return uncertain("media_submit_indeterminate")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 400 && res.StatusCode < 500 {
		return rejectedHTTP(res.StatusCode)
	}
	if res.StatusCode != 200 {
		return uncertain("media_submit_indeterminate")
	}
	return ParseSpeechSSE(res.Body)
}
func ParseSpeechSSE(reader io.Reader) Outcome {
	scanner := bufio.NewScanner(io.LimitReader(reader, 24<<20))
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var audio []byte
	var usage json.RawMessage
	terminal := false
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event struct {
			Code  *int            `json:"code"`
			Data  string          `json:"data"`
			Usage json.RawMessage `json:"usage"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event) != nil || event.Code == nil {
			return uncertain("speech_stream_invalid")
		}
		switch *event.Code {
		case 0:
			if terminal {
				return uncertain("speech_stream_invalid")
			}
			if event.Data != "" {
				b, err := base64.StdEncoding.DecodeString(event.Data)
				if err != nil || len(audio)+len(b) > MaxAudioBytes {
					return uncertain("speech_stream_invalid")
				}
				audio = append(audio, b...)
			}
		case 20000000:
			if terminal {
				return uncertain("speech_stream_invalid")
			}
			terminal = true
			var receipt struct {
				TextWords *int64 `json:"text_words"`
			}
			if json.Unmarshal(event.Usage, &receipt) == nil && receipt.TextWords != nil && *receipt.TextWords >= 0 {
				usage, _ = json.Marshal(receipt)
			}
		case 45000000:
			if len(audio) == 0 && !terminal {
				return rejected("media_provider_unauthorized")
			}
			return uncertain("speech_provider_failed")
		case 40402003:
			if len(audio) == 0 && !terminal {
				return rejected("media_provider_rejected")
			}
			return uncertain("speech_provider_failed")
		default:
			return uncertain("speech_provider_failed")
		}
	}
	if scanner.Err() != nil || !terminal || !ValidMP3(audio) {
		return uncertain("speech_stream_incomplete")
	}
	// Quantity remains the reserved product character allowance; provider usage
	// is an independent receipt and is never substituted into that quantity.
	return Outcome{Status: "succeeded", Progress: 100, Content: audio, ContentType: "audio/mpeg", ProviderUsage: usage}
}
func (s *OfficialSupplier) Poll(ctx context.Context, p Provider, id string) Outcome {
	if p.Kind != Model3D || id == "" || len(id) > 256 || strings.ContainsAny(id, "/\\?#") {
		return uncertain("media_provider_task_invalid")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://openapi.tripo3d.com/v3/tasks/"+url.PathEscape(id), nil)
	req.Header.Set("Authorization", "Bearer "+p.Key)
	res, err := s.Client.Do(req)
	if err != nil {
		return uncertain("media_poll_unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return uncertain("media_poll_unavailable")
	}
	var receipt struct {
		Code *int `json:"code"`
		Data struct {
			TaskID   string   `json:"task_id"`
			Status   string   `json:"status"`
			Progress int      `json:"progress"`
			Credits  *float64 `json:"credits_consumed"`
			Output   struct {
				ModelURL string `json:"model_url"`
			} `json:"output"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&receipt) != nil || receipt.Code == nil || *receipt.Code != 0 || receipt.Data.TaskID != id {
		return uncertain("media_poll_unavailable")
	}
	usage, _ := json.Marshal(map[string]any{"credits_consumed": receipt.Data.Credits})
	out := Outcome{Status: "running", ProviderID: id, Progress: receipt.Data.Progress, ProviderUsage: usage}
	if out.Progress < 0 || out.Progress > 100 {
		out.Progress = 0
	}
	switch receipt.Data.Status {
	case "queued", "running":
		out.Status = receipt.Data.Status
		return out
	case "success":
		content, err := s.Download(ctx, receipt.Data.Output.ModelURL)
		if err != nil {
			out.Status = "indeterminate"
			out.ErrorCode = "model3d_content_unavailable"
			return out
		}
		out.Status = "succeeded"
		out.Progress = 100
		out.Content = content
		out.ContentType = "model/gltf-binary"
		return out
	case "failed", "cancelled", "banned", "expired":
		if receipt.Data.Credits == nil || *receipt.Data.Credits < 0 {
			out.Status = "indeterminate"
			out.ErrorCode = "model3d_failure_usage_unknown"
			return out
		}
		units := int64(0)
		if *receipt.Data.Credits > 0 {
			units = 1
		}
		out.Status = "failed"
		out.ErrorCode = "model3d_provider_failed"
		out.SettlementUnits = &units
		return out
	default:
		out.Status = "indeterminate"
		out.ErrorCode = "model3d_status_unknown"
		return out
	}
}
