package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 组织用自己的服务密钥查询视频模型的额度时长规则，回答必须与额度守卫实际执行的一致：
// 模型名先经组织改道表、再经组合路由改写，才去查规则。
func TestOrganizationVideoRuleAnswersWhatTheQuotaGuardEnforces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	route := func(id int64, public, upstream string) service.CompositeModelRoute {
		return service.CompositeModelRoute{ID: id, GroupID: 4, PublicModel: public,
			MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformOpenAI,
			UpstreamModel: upstream, Endpoint: service.CompositeRouteEndpointAny, Priority: 1, Enabled: true}
	}
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{routes: []service.CompositeModelRoute{
		route(1, "t-grok-video-1.5", "grok-video-1.5"),
		route(2, "cheap-video", "seedance-2-fast"),
		route(3, "veo3.1-fast", "veo3.1-fast"),
	}})
	ask := func(user *service.User, query string) (int, map[string]any) {
		router := gin.New()
		router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := int64(4)
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				GroupID: &groupID,
				Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite},
				User:    user,
			})
			c.Next()
		})))
		router.GET("/v1/organization/media-quota/video-rule",
			organizationVideoQuotaRuleHandler(resolver, fakeOrgOverrides{"old-video": {Model: "cheap-video"}}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/organization/media-quota/video-rule?"+query, nil))
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	org := &service.User{ID: 62, AccountType: "organization_service", OrganizationID: "org-wemos"}

	code, body := ask(org, "model=t-grok-video-1.5")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "grok-video-1.5", body["quota_model"])
	require.Equal(t, true, body["supported"])
	require.Equal(t, map[string]any{"min_seconds": 6.0, "max_seconds": 30.0, "default_seconds": 8.0}, body["rule"])

	_, body = ask(org, "model=old-video")
	require.Equal(t, "seedance-2-fast", body["quota_model"], "the override applies before the route")
	require.Equal(t, map[string]any{"min_seconds": 4.0, "max_seconds": 15.0, "default_seconds": 5.0}, body["rule"])

	_, body = ask(org, "model=veo3.1-fast")
	require.Equal(t, false, body["supported"])
	require.NotContains(t, body, "rule")

	code, _ = ask(&service.User{ID: 7, AccountType: "personal"}, "model=t-grok-video-1.5")
	require.Equal(t, http.StatusForbidden, code)
	code, _ = ask(org, "model=")
	require.Equal(t, http.StatusBadRequest, code)
}
