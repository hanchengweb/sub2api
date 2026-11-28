package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const voiceReclaimAuthTestKey = "voice-reclaim-organization-test-key"

const voiceReclaimMemberBody = `{"actor_id":"11111111-1111-4111-8111-111111111111","state":"deleted","version":1}`

type voiceReclaimAuthKeyRepo struct {
	service.APIKeyRepository
	apiKey *service.APIKey
}

func (r *voiceReclaimAuthKeyRepo) GetByKeyForAuth(_ context.Context, key string) (*service.APIKey, error) {
	if key != r.apiKey.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.apiKey
	return &clone, nil
}

func (r *voiceReclaimAuthKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error {
	return nil
}

func voiceReclaimAuthKey() *service.APIKey {
	group := &service.Group{ID: 4, Status: service.StatusActive, Hydrated: true,
		Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard}
	return &service.APIKey{ID: 2, UserID: 7, Key: voiceReclaimAuthTestKey, Status: service.StatusActive,
		GroupID: &group.ID, Group: group,
		User: &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive,
			AccountType: service.AccountTypeOrganizationService, Balance: 10}}
}

// serveVoiceReclaim 走真实的 API key 中间件和真实的路由注册，只替换存储。
func serveVoiceReclaim(t *testing.T, key *service.APIKey, method, path, body string) (*httptest.ResponseRecorder, *voiceReclaimHTTPStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keys := service.NewAPIKeyService(&voiceReclaimAuthKeyRepo{apiKey: key}, nil, nil, nil, nil, nil, cfg)
	store := &voiceReclaimHTTPStore{}
	h := handler.NewOrganizationMediaTaskHandler(nil, store, nil, nil, nil, nil)
	t.Cleanup(h.Stop)
	r := gin.New()
	registerOrganizationMediaTasks(r, h, middleware.NewAPIKeyAuthMiddleware(keys, nil, cfg))
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key.Key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w, store
}

// 组织余额不大于 0、key 额度用尽时，成员停用通知和回收预览仍要到达处理函数。
func TestVoiceReclaimRoutesIgnoreOrganizationBalanceAndKeyQuota(t *testing.T) {
	t.Setenv("WXM_VOICE_MEMBER_RECLAIM_ENABLED", "true")
	for _, tc := range []struct {
		name   string
		mutate func(*service.APIKey)
	}{
		{"zero balance", func(k *service.APIKey) { k.User.Balance = 0 }},
		{"negative balance", func(k *service.APIKey) { k.User.Balance = -3 }},
		{"quota exhausted status", func(k *service.APIKey) { k.Status = service.StatusAPIKeyQuotaExhausted }},
		{"quota used up", func(k *service.APIKey) { k.Quota, k.QuotaUsed = 5, 5 }},
		{"zero balance and quota exhausted", func(k *service.APIKey) {
			k.User.Balance, k.Status = 0, service.StatusAPIKeyQuotaExhausted
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := voiceReclaimAuthKey()
			tc.mutate(key)
			w, store := serveVoiceReclaim(t, key, http.MethodPut, "/v1/voice-assets/member-state", voiceReclaimMemberBody)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.Equal(t, 1, store.writes)
			require.EqualValues(t, 7, store.user)

			w, store = serveVoiceReclaim(t, key, http.MethodGet, "/v1/voice-assets/reclaim-preview", "")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.EqualValues(t, 7, store.user)
			require.Zero(t, store.writes)
		})
	}
}

// 放行的只是计费拦截：鉴权、过期和账号类型检查照旧。
func TestVoiceReclaimRoutesKeepAuthenticationChecks(t *testing.T) {
	t.Setenv("WXM_VOICE_MEMBER_RECLAIM_ENABLED", "true")
	past := time.Now().Add(-time.Hour)
	for _, tc := range []struct {
		name   string
		mutate func(*service.APIKey)
		status int
		code   string
	}{
		{"expired status", func(k *service.APIKey) { k.Status = service.StatusAPIKeyExpired }, 403, "API_KEY_EXPIRED"},
		{"expired time", func(k *service.APIKey) { k.ExpiresAt = &past }, 403, "API_KEY_EXPIRED"},
		{"disabled key", func(k *service.APIKey) { k.Status = service.StatusDisabled }, 401, "API_KEY_DISABLED"},
		{"disabled organization", func(k *service.APIKey) { k.User.Status = service.StatusDisabled }, 401, "USER_INACTIVE"},
		{"personal account", func(k *service.APIKey) {
			k.User.AccountType = service.AccountTypePersonal
		}, 403, "media_organization_required"},
	} {
		for _, route := range []struct{ method, path, body string }{
			{http.MethodPut, "/v1/voice-assets/member-state", voiceReclaimMemberBody},
			{http.MethodGet, "/v1/voice-assets/reclaim-preview", ""},
		} {
			t.Run(tc.name+" "+route.method, func(t *testing.T) {
				key := voiceReclaimAuthKey()
				tc.mutate(key)
				w, store := serveVoiceReclaim(t, key, route.method, route.path, route.body)
				require.Equal(t, tc.status, w.Code, w.Body.String())
				require.Contains(t, w.Body.String(), tc.code)
				require.Zero(t, store.writes)
				require.Zero(t, store.user)
			})
		}
	}
}
