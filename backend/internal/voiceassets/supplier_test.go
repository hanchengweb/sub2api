package voiceassets

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOfficialTrainingAndQueryUseFixedAPIWithoutCustomSpeaker(t *testing.T) {
	paths := []string{}
	s := &OfficialSupplier{Client: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.String())
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "fixture", r.Header.Get("X-Api-Key"))
		require.Empty(t, r.Header.Get("X-Api-Resource-Id"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "S_test", body["speaker_id"])
		require.NotContains(t, body, "custom_speaker_id")
		if strings.HasSuffix(r.URL.Path, "voice_clone") {
			require.Contains(t, body, "audio")
		} else {
			require.Len(t, body, 1)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":2,"speaker_id":"S_test","speaker_status":[{"model_type":5,"demo_audio":"https://private.example/signed-secret"}]}`))}, nil
	})}}
	p := mediatasks.Provider{Key: "fixture"}
	r := s.Train(context.Background(), p, "request", "S_test", "wav", wavSample(), "")
	require.Equal(t, "ready", r.Status)
	r = s.Query(context.Background(), p, "request", "S_test")
	require.Equal(t, "ready", r.Status)
	b, _ := json.Marshal(r)
	require.NotContains(t, string(b), "signed-secret")
	require.Equal(t, []string{"https://openspeech.bytedance.com/api/v3/tts/voice_clone", "https://openspeech.bytedance.com/api/v3/tts/get_voice"}, paths)
}
func TestCloneReadinessRequiresClone2ModelAndMatchingSpeaker(t *testing.T) {
	for _, body := range []string{`{"status":2,"speaker_id":"S_test","speaker_status":[{"model_type":2}]}`, `{"status":4,"speaker_id":"S_other","speaker_status":[{"model_type":5}]}`, `{"status":2,"speaker_id":"S_test","code":100}`} {
		s := &OfficialSupplier{Client: &http.Client{Transport: fixtureTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		require.Equal(t, "indeterminate", s.Query(context.Background(), mediatasks.Provider{}, "req", "S_test").Status)
	}
}
