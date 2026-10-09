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
	for _, path := range []string{"/v1/organization/media-quota", "/v1/videos/:request_id", "/v1/images/generations"} {
		r.GET(path, func(c *gin.Context) { c.Status(200) })
		r.POST(path, func(c *gin.Context) { c.Status(200) })
	}
	for _, tc := range []struct {
		method, path, key string
		status            int
	}{
		{"GET", "/v1/organization/media-quota", "fixture-key", 200}, {"GET", "/v1/videos/fixture-task", "fixture-key", 200},
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
