//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDesktopDriveOnlyExactRegisteredRoutesSkipBilling(t *testing.T) {
	for _, test := range []struct {
		method, route string
		want          bool
	}{
		{"GET", "/v1/desktop/drive", true},
		{"GET", "/v1/desktop/drive/:resource_id", true},
		{"GET", "/v1/desktop/drive/:resource_id/items/:item_id", true},
		{"GET", "/v1/desktop/drive/:resource_id/items/:item_id/original", true},
		{"POST", "/v1/desktop/drive", true},
		{"POST", "/v1/desktop/drive/:resource_id/folders", true},
		{"POST", "/v1/desktop/drive/:resource_id/upload", true},
		{"PATCH", "/v1/desktop/drive/:resource_id/items/:item_id", true},
		{"DELETE", "/v1/desktop/drive/:resource_id", true},
		{"GET", "/v1/desktop/drive/some/arbitrary/resource", false},
		{"POST", "/v1/desktop/drive/:resource_id/items/:item_id", false},
		{"POST", "/v1/chat/completions", false},
		{"GET", "/v1/desktop/drive-admin", false},
		{"PUT", "/v1/desktop/drive", false},
	} {
		require.Equal(t, test.want, isDesktopDriveRequest(test.method, test.route), test.method+" "+test.route)
	}
}

func TestDesktopDriveZeroBalanceKeepsAuthenticationBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	past := time.Now().Add(-time.Hour)
	for _, test := range []struct {
		name   string
		change func(*service.APIKey)
		want   int
	}{
		{"zero balance", func(*service.APIKey) {}, 200},
		{"disabled key", func(k *service.APIKey) { k.Status = service.StatusAPIKeyDisabled }, 401},
		{"disabled user", func(k *service.APIKey) { k.User.Status = service.StatusDisabled }, 401},
		{"disabled group", func(k *service.APIKey) { k.Group.Status = service.StatusDisabled }, 403},
		{"expired", func(k *service.APIKey) { k.ExpiresAt = &past }, 403},
		{"organization service", func(k *service.APIKey) { k.User.AccountType = service.AccountTypeOrganizationService }, 403},
		{"quota key", func(k *service.APIKey) { k.Quota = 1 }, 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			group := int64(4)
			key := &service.APIKey{ID: 1, UserID: 7, Key: "personal-test", Status: service.StatusActive, GroupID: &group, Group: &service.Group{ID: 4, Status: service.StatusActive, Hydrated: true}, User: &service.User{ID: 7, Status: service.StatusActive, Balance: 0}}
			test.change(key)
			repo := &stubApiKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { return key, nil }}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			r := gin.New()
			auth := gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg))
			r.GET("/v1/desktop/drive", auth, func(c *gin.Context) {
				k, _ := GetAPIKeyFromContext(c)
				if service.ValidateDesktopDriveKey(k) != nil {
					c.Status(403)
					return
				}
				c.Status(200)
			})
			r.POST("/v1/chat/completions", auth, func(c *gin.Context) { c.Status(200) })
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/desktop/drive", nil)
			req.Header.Set("Authorization", "Bearer personal-test")
			r.ServeHTTP(w, req)
			require.Equal(t, test.want, w.Code, w.Body.String())
			if test.name == "zero balance" {
				w = httptest.NewRecorder()
				req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				req.Header.Set("Authorization", "Bearer personal-test")
				r.ServeHTTP(w, req)
				require.Equal(t, 403, w.Code)
				require.Contains(t, w.Body.String(), "INSUFFICIENT_BALANCE")
			}
		})
	}
}
