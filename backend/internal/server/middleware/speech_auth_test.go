//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSpeechFreeRouteStillAuthenticatesAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := &service.APIKey{ID: 1, UserID: 2, Key: "speech-fixture", Status: service.StatusActive, User: &service.User{ID: 2, Status: service.StatusActive, Balance: 0}}
	repo := &stubApiKeyRepo{getByKey: func(ctx context.Context, value string) (*service.APIKey, error) {
		if value != key.Key {
			return nil, service.ErrAPIKeyNotFound
		}
		return key, nil
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	r := gin.New()
	r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
	r.POST("/v1/audio/speech", func(c *gin.Context) { c.Status(200) })
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.Status(200) })
	request := func(path, credential string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		r.ServeHTTP(w, req)
		return w.Code
	}
	require.Equal(t, 401, request("/v1/audio/speech", ""))
	require.Equal(t, 401, request("/v1/audio/speech", "invalid"))
	require.Equal(t, 200, request("/v1/audio/speech", key.Key))
	require.Equal(t, 403, request("/v1/chat/completions", key.Key))
	key.User.Status = service.StatusDisabled
	require.Equal(t, 401, request("/v1/audio/speech", key.Key))
}

func TestSpeechBillingExceptionIsExact(t *testing.T) {
	for _, row := range []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/v1/audio/speech", true}, {"GET", "/v1/audio/voices", true},
		{"GET", "/v1/audio/speech", false}, {"POST", "/v1/audio/voices", false},
		{"POST", "/v1/audio/speech/anything", false}, {"POST", "/v1/chat/completions", false},
	} {
		require.Equal(t, row.allowed, isSpeechRequest(row.method, row.path))
	}
}
