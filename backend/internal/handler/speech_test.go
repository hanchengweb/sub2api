//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func speechFixture(t *testing.T, upstream http.HandlerFunc) (*SpeechHandler, *redis.Client, *service.APIKey, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: m.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	s := httptest.NewServer(upstream)
	t.Cleanup(s.Close)
	h := &SpeechHandler{endpoint: s.URL, client: s.Client(), redis: rdb}
	key := &service.APIKey{ID: 11, UserID: 23, Status: service.StatusActive}
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key) })
	r.GET("/v1/audio/voices", h.Voices)
	r.POST("/v1/audio/speech", middleware.RequestBodyLimit(8192), h.Synthesize)
	return h, rdb, key, r
}

func speechRequest(r *gin.Engine, input string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(input)))
	return w
}

func TestSpeechRejectsUntrustedInputsBeforeInference(t *testing.T) {
	calls := 0
	_, _, key, r := speechFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	for _, input := range []string{`{}`, `{"input":" "}`, `{"input":"hi","voice":"Serena"}`, `{"input":"hi","model":"private-model"}`, `{"input":"hi","url":"https://example.com"}`, `{"input":"hi","response_format":"mp3"}`, `{"input":"hi"} {}`, fmt.Sprintf(`{"input":%q}`, strings.Repeat("你", 201)), strings.Repeat("x", 8193)} {
		w := speechRequest(r, input)
		require.Contains(t, []int{400, 413}, w.Code, input)
	}
	key.Status = service.StatusAPIKeyExpired
	require.Equal(t, 403, speechRequest(r, `{"input":"你好"}`).Code)
	key.Status = service.StatusActive
	past := time.Now().Add(-time.Second)
	key.ExpiresAt = &past
	require.Equal(t, 403, speechRequest(r, `{"input":"你好"}`).Code)
	require.Zero(t, calls)
}

func TestSpeechAudioNoCredentialsOrInternalNames(t *testing.T) {
	wave := append([]byte("RIFF0000WAVE"), make([]byte, 32)...)
	_, _, _, r := speechFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, map[string]any{"input": "你好", "voice": "warm", "response_format": "wav"}, body)
		_, _ = w.Write(wave)
	})
	w := speechRequest(r, `{"input":"你好","model":"speech-v1"}`)
	require.Equal(t, 200, w.Code)
	require.Equal(t, wave, w.Body.Bytes())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, "audio/wav", w.Header().Get("Content-Type"))
}

func TestSpeechFailureAndBudgetAreBounded(t *testing.T) {
	calls := 0
	_, rdb, key, r := speechFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(502)
		_, _ = w.Write([]byte("upstream-model /private/path secret"))
	})
	w := speechRequest(r, `{"input":"你好"}`)
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "private")
	require.NotContains(t, w.Body.String(), "secret")
	day := time.Now().UTC().Format("20060102")
	require.NoError(t, rdb.Set(context.Background(), fmt.Sprintf("wxm:speech:chars:%d:%s", key.UserID, day), 9999, time.Hour).Err())
	key.ID++ // another key belonging to the same user cannot reset the budget
	require.Equal(t, 429, speechRequest(r, `{"input":"你好"}`).Code)
	require.Equal(t, 1, calls)
	key.UserID++
	require.Equal(t, 503, speechRequest(r, `{"input":"你好"}`).Code)
	require.Equal(t, 2, calls)
	require.NoError(t, rdb.Close())
	require.Equal(t, 503, speechRequest(r, `{"input":"你好"}`).Code)
	require.Equal(t, 2, calls)
}

func TestSpeechCatalogRequiresReadyWorker(t *testing.T) {
	h, _, _, r := speechFixture(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/healthz", r.URL.Path)
		w.WriteHeader(200)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/audio/voices", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "温柔")
	require.NotContains(t, w.Body.String(), "Serena")
	h.endpoint = ""
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/audio/voices", nil))
	require.Equal(t, 503, w.Code)
}
