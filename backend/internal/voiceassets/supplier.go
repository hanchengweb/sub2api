package voiceassets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
)

type OfficialSupplier struct{ Client *http.Client }

func NewOfficialSupplier() *OfficialSupplier {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &OfficialSupplier{Client: &http.Client{Transport: transport, Timeout: 85 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (s *OfficialSupplier) Train(ctx context.Context, p mediatasks.Provider, id, speaker, format string, audio []byte, demo string) TrainingResult {
	body := map[string]any{"speaker_id": speaker, "audio": map[string]string{"data": base64.StdEncoding.EncodeToString(audio), "format": format}, "language": 0}
	if demo != "" {
		body["extra_params"] = map[string]string{"demo_text": demo}
	}
	return s.call(ctx, p, id, "https://openspeech.bytedance.com/api/v3/tts/voice_clone", speaker, body)
}
func (s *OfficialSupplier) Query(ctx context.Context, p mediatasks.Provider, id, speaker string) TrainingResult {
	return s.call(ctx, p, id, "https://openspeech.bytedance.com/api/v3/tts/get_voice", speaker, map[string]string{"speaker_id": speaker})
}
func (s *OfficialSupplier) call(ctx context.Context, p mediatasks.Provider, id, target, speaker string, body any) TrainingResult {
	unknown := TrainingResult{Status: "indeterminate", ErrorCode: "voice_clone_unconfirmed"}
	b, e := json.Marshal(body)
	if e != nil {
		return unknown
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(b))
	if e != nil {
		return unknown
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", p.Key)
	req.Header.Set("X-Api-Request-Id", id)
	res, e := s.Client.Do(req)
	if e != nil {
		return unknown
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 && res.StatusCode < 500 {
		return TrainingResult{Status: "failed", ErrorCode: "voice_clone_rejected"}
	}
	if res.StatusCode != 200 {
		return unknown
	}
	var r struct {
		Status        *int   `json:"status"`
		SpeakerID     string `json:"speaker_id"`
		Code          *int   `json:"code"`
		SpeakerStatus []struct {
			ModelType int `json:"model_type"`
		} `json:"speaker_status"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&r) != nil || r.Status == nil || r.SpeakerID != speaker || (r.Code != nil && *r.Code != 0) {
		return unknown
	}
	switch *r.Status {
	case 0:
		return TrainingResult{Status: "untrained"}
	case 1:
		return TrainingResult{Status: "training"}
	case 2, 4:
		for _, status := range r.SpeakerStatus {
			if status.ModelType == 5 {
				return TrainingResult{Status: "ready"}
			}
		}
		return unknown
	case 3:
		return TrainingResult{Status: "failed", ErrorCode: "voice_clone_training_failed"}
	default:
		return unknown
	}
}
