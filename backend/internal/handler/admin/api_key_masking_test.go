package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 组织 key 签发要求至少 32 位，脱敏必须对最短长度也只露首尾几位。
const (
	maskingOrgKey         = "orgkey0123456789abcdefghijklWXYZ"
	maskingOrgKeyMasked   = "orgkey...WXYZ"
	maskingPersonalKey    = "sk-personal0123456789abcdefghijklmnQRST"
	maskingOrphanKey      = "sk-orphan0123456789abcdefghijklmnopUVWX"
	maskingOrphanedMasked = "sk-orp...UVWX"
)

func maskingOrgUser() *service.User {
	return &service.User{ID: 7, Email: "org@service.invalid", Status: service.StatusActive,
		AccountType: service.AccountTypeOrganizationService}
}

func maskingPersonalUser() *service.User {
	return &service.User{ID: 8, Email: "person@example.com", Status: service.StatusActive,
		AccountType: service.AccountTypePersonal}
}

func maskingKey(id, userID int64, key string, owner *service.User) service.APIKey {
	return service.APIKey{ID: id, UserID: userID, Key: key, Name: "masking", Status: service.StatusActive, User: owner}
}

func maskingRouter(keys []service.APIKey) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.users = []service.User{*maskingOrgUser(), *maskingPersonalUser()}
	svc.apiKeys = keys
	r := gin.New()
	userHandler := NewUserHandler(svc, nil, nil, nil, nil, nil, nil)
	groupHandler := NewGroupHandler(svc, nil, nil)
	keyHandler := NewAdminAPIKeyHandler(svc, nil)
	r.GET("/admin/users/:id/api-keys", userHandler.GetUserAPIKeys)
	r.GET("/admin/groups/:id/api-keys", groupHandler.GetGroupAPIKeys)
	r.PUT("/admin/api-keys/:id", keyHandler.UpdateGroup)
	return r
}

func maskingRequest(t *testing.T, r *gin.Engine, method, path, body string) string {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return w.Body.String()
}

func TestAdminUserAPIKeysMaskOrganizationKeys(t *testing.T) {
	// 与仓储一致：按用户列出 key 时不加载属主。
	body := maskingRequest(t, maskingRouter([]service.APIKey{maskingKey(10, 7, maskingOrgKey, nil)}),
		http.MethodGet, "/admin/users/7/api-keys", "")
	require.NotContains(t, body, maskingOrgKey)
	require.Contains(t, body, `"key":"`+maskingOrgKeyMasked+`"`)

	body = maskingRequest(t, maskingRouter([]service.APIKey{maskingKey(11, 8, maskingPersonalKey, nil)}),
		http.MethodGet, "/admin/users/8/api-keys", "")
	require.Contains(t, body, `"key":"`+maskingPersonalKey+`"`)
}

func TestAdminUserAPIKeysNeverFallBackToPlaintext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.apiKeys = []service.APIKey{maskingKey(10, 7, maskingOrgKey, nil)}
	svc.getUserErr = service.ErrUserNotFound
	r := gin.New()
	r.GET("/admin/users/:id/api-keys", NewUserHandler(svc, nil, nil, nil, nil, nil, nil).GetUserAPIKeys)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/7/api-keys", nil))
	require.NotContains(t, w.Body.String(), maskingOrgKey)
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())

	// 没有 key 时不需要查属主，返回空列表的行为不变。
	svc.apiKeys = nil
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/7/api-keys", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

func TestAdminGroupAPIKeysMaskOrganizationAndUnknownOwners(t *testing.T) {
	body := maskingRequest(t, maskingRouter([]service.APIKey{
		maskingKey(10, 7, maskingOrgKey, maskingOrgUser()),
		maskingKey(11, 8, maskingPersonalKey, maskingPersonalUser()),
		maskingKey(12, 9, maskingOrphanKey, nil),
	}), http.MethodGet, "/admin/groups/4/api-keys", "")
	require.NotContains(t, body, maskingOrgKey)
	require.NotContains(t, body, maskingOrphanKey)
	require.Contains(t, body, `"key":"`+maskingOrgKeyMasked+`"`)
	require.Contains(t, body, `"key":"`+maskingOrphanedMasked+`"`)
	require.Contains(t, body, `"key":"`+maskingPersonalKey+`"`)
}

func TestAdminUpdateAPIKeyMasksOrganizationKey(t *testing.T) {
	for _, request := range []string{`{}`, `{"group_id":0}`, `{"reset_rate_limit_usage":true}`} {
		body := maskingRequest(t, maskingRouter([]service.APIKey{maskingKey(10, 7, maskingOrgKey, maskingOrgUser())}),
			http.MethodPut, "/admin/api-keys/10", request)
		require.NotContains(t, body, maskingOrgKey, request)
		require.Contains(t, body, `"key":"`+maskingOrgKeyMasked+`"`, request)

		body = maskingRequest(t, maskingRouter([]service.APIKey{maskingKey(11, 8, maskingPersonalKey, maskingPersonalUser())}),
			http.MethodPut, "/admin/api-keys/11", request)
		require.Contains(t, body, `"key":"`+maskingPersonalKey+`"`, request)
	}
}

type maskingUsageRepo struct {
	service.UsageLogRepository
	logs []service.UsageLog
}

func (r *maskingUsageRepo) ListWithFilters(_ context.Context, params pagination.PaginationParams, _ usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	return r.logs, &pagination.PaginationResult{Total: int64(len(r.logs)), Page: params.Page, PageSize: params.PageSize, Pages: 1}, nil
}

func TestAdminUsageListMasksOrganizationKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 与仓储一致：日志上的 key 不带属主，属主在日志的 User 上。
	entry := func(id, userID int64, key string, owner *service.User) service.UsageLog {
		apiKey := maskingKey(id, userID, key, nil)
		return service.UsageLog{ID: id, UserID: userID, APIKeyID: id, User: owner, APIKey: &apiKey}
	}
	repo := &maskingUsageRepo{logs: []service.UsageLog{
		entry(10, 7, maskingOrgKey, maskingOrgUser()),
		entry(11, 8, maskingPersonalKey, maskingPersonalUser()),
		entry(12, 9, maskingOrphanKey, nil),
	}}
	r := gin.New()
	r.GET("/admin/usage", NewUsageHandler(service.NewUsageService(repo, nil, nil, nil), nil, nil, nil).List)
	body := maskingRequest(t, r, http.MethodGet, "/admin/usage", "")
	require.NotContains(t, body, maskingOrgKey)
	require.NotContains(t, body, maskingOrphanKey)
	require.Contains(t, body, `"key":"`+maskingOrgKeyMasked+`"`)
	require.Contains(t, body, `"key":"`+maskingOrphanedMasked+`"`)
	require.Contains(t, body, `"key":"`+maskingPersonalKey+`"`)
}
