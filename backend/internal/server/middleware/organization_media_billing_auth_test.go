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

type organizationBillingKeyRepo struct {
	service.APIKeyRepository
	apiKey *service.APIKey
}

func (r *organizationBillingKeyRepo) GetByKeyForAuth(context.Context, string) (*service.APIKey, error) {
	clone := *r.apiKey
	return &clone, nil
}

func (r *organizationBillingKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

// 组织余额或 key 额度用尽时，豁免清单之外的计费接口仍然被拦截。
func TestOrganizationMediaBillingStaysEnforcedOutsideExactRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := []struct{ method, path string }{
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/v1/images/generations"},
		{http.MethodPost, "/v1/videos/generations"},
		{http.MethodPut, "/v1/voice-assets/:id"},
		{http.MethodPost, "/v1/voice-assets/member-state"},
		{http.MethodPut, "/v1/voice-assets/reclaim-preview"},
	}
	for _, tc := range []struct {
		name    string
		balance float64
		status  string
		want    int
	}{
		{"zero balance", 0, service.StatusActive, http.StatusForbidden},
		{"quota exhausted", 10, service.StatusAPIKeyQuotaExhausted, http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &service.Group{ID: 4, Status: service.StatusActive, Hydrated: true,
				Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard}
			key := &service.APIKey{ID: 2, UserID: 7, Key: "organization-billing-test-key", Status: tc.status,
				GroupID: &group.ID, Group: group,
				User: &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive,
					AccountType: service.AccountTypeOrganizationService, Balance: tc.balance}}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			keys := service.NewAPIKeyService(&organizationBillingKeyRepo{apiKey: key}, nil, nil, nil, nil, nil, cfg)
			r := gin.New()
			reached := 0
			for _, route := range routes {
				r.Handle(route.method, route.path, gin.HandlerFunc(NewAPIKeyAuthMiddleware(keys, nil, cfg)), func(c *gin.Context) {
					reached++
					c.Status(http.StatusNoContent)
				})
			}
			for _, route := range routes {
				req := httptest.NewRequest(route.method, route.path, nil)
				req.Header.Set("Authorization", "Bearer "+key.Key)
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, tc.want, w.Code, "%s %s: %s", route.method, route.path, w.Body.String())
			}
			require.Zero(t, reached)
		})
	}
}
