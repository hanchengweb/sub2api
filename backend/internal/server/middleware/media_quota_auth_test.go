//go:build unit

package middleware

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMediaQuotaReadAvailableWithoutMoneyButStillAuthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*service.APIKey, error) {
		if key != "fixture-key" {
			return nil, service.ErrAPIKeyNotFound
		}
		return &service.APIKey{ID: 1, UserID: 1, Key: key, Status: service.StatusActive, User: &service.User{ID: 1, Role: service.RoleUser, Status: service.StatusActive, AccountType: "organization_service", Balance: 0}}, nil
	}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	r := gin.New()
	r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
	for _, path := range []string{"/v1/organization/media-quota", "/v1/videos/:request_id", "/v1/videos/:request_id/content", "/videos/:request_id", "/videos/:request_id/content", "/v1/images/generations"} {
		r.GET(path, func(c *gin.Context) { c.Status(200) })
		r.POST(path, func(c *gin.Context) { c.Status(200) })
	}
	for _, tc := range []struct {
		method, path, key string
		status            int
	}{
		{"GET", "/v1/organization/media-quota", "fixture-key", 200}, {"GET", "/v1/videos/fixture-task", "fixture-key", 200},
		{"GET", "/v1/videos/fixture-task/content", "fixture-key", 200}, {"GET", "/videos/fixture-task/content", "fixture-key", 200},
		{"GET", "/videos/fixture-task", "fixture-key", 200},
		{"GET", "/v1/videos/fixture-task/content", "", 401},
		{"POST", "/v1/videos/fixture-task/content", "fixture-key", 403},
		{"POST", "/v1/organization/media-quota", "fixture-key", 403}, {"POST", "/v1/images/generations", "fixture-key", 403},
		{"GET", "/v1/organization/media-quota", "", 401},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("x-api-key", tc.key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, tc.status, w.Code)
	}
}

func TestVideoReadRejectsRevokedAccessButAllowsExhaustedQuota(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*service.APIKey)
		status int
	}{
		{"quota exhausted", func(k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted }, 200},
		{"key disabled", func(k *service.APIKey) { k.Status = "disabled" }, 401},
		{"user disabled", func(k *service.APIKey) { k.User.Status = "disabled" }, 401},
		{"key expired status", func(k *service.APIKey) { k.Status = service.StatusAPIKeyExpired }, 403},
		{"key expired time", func(k *service.APIKey) { past := time.Now().Add(-time.Minute); k.ExpiresAt = &past }, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) {
				k := &service.APIKey{ID: 1, UserID: 1, Status: service.StatusActive, User: &service.User{ID: 1, Status: service.StatusActive}}
				tc.change(k)
				return k, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			r := gin.New()
			r.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
			r.GET("/v1/videos/:request_id/content", func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest("GET", "/v1/videos/original-task/content", nil)
			req.Header.Set("x-api-key", "fixture-key")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
}
