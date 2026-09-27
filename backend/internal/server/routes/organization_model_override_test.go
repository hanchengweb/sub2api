package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeOrgOverrides map[string]service.OrganizationModelOverride

func (f fakeOrgOverrides) OrganizationModelOverride(_ context.Context, model string) (service.OrganizationModelOverride, bool) {
	override, ok := f[model]
	return override, ok
}

var bEndOverrides = fakeOrgOverrides{
	"doubao-seedream-5-0-pro": {Model: "v-doubao-seedream-5-0-pro", Sizes: map[string]string{"1.5K": "1536x1536"}},
}

// runOrgOverrideRequest 按线上分组 4 的两条豆包路由搭一个网关，返回处理器看到的
// 请求体和计费/日志用的公开模型名。orgID 为空表示 C 端用户。
func runOrgOverrideRequest(t *testing.T, orgID, body string) (string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{routes: []service.CompositeModelRoute{
		{ID: 19, GroupID: 4, PublicModel: "v-doubao-seedream-5-0-pro", MatchType: service.CompositeRouteMatchExact,
			TargetPlatform: service.PlatformOpenAI, UpstreamModel: "v-doubao-seedream-5-0-pro",
			Endpoint: service.CompositeRouteEndpointAny, Priority: 1, Enabled: true},
		{ID: 62, GroupID: 4, PublicModel: "doubao-seedream-5-0-pro", MatchType: service.CompositeRouteMatchExact,
			TargetPlatform: service.PlatformOpenAI, UpstreamModel: "doubao-seedream-5-0-pro",
			Endpoint: service.CompositeRouteEndpointAny, Priority: 1, Enabled: true},
	}})
	router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(4)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite},
			User:    &service.User{ID: 49, OrganizationID: orgID},
		})
		c.Next()
	})))
	router.Use(compositeTargetPlatformMiddleware(resolver, bEndOverrides))

	var gotBody, publicModel string
	router.POST("/v1/images/generations", func(c *gin.Context) {
		raw, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		gotBody = string(raw)
		publicModel, _ = service.RequestedPublicModelFromContext(c.Request.Context())
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	return gotBody, publicModel
}

// B 端组织请求豆包 5.0 Pro：改到火山官方那条路由，1.5K 档位换成像素。
//
// 换像素是为了计费稳定：官方的 1.5K 档位由模型按提示词定宽高比，竖图会回
// 1344x1792 这类尺寸，按最长边分档会被当成 2K 多收钱；传 1536x1536 就原样出这个尺寸。
func TestOrganizationRequestIsRerouted(t *testing.T) {
	body, public := runOrgOverrideRequest(t, "org-college",
		`{"model":"doubao-seedream-5-0-pro","prompt":"x","size":"1.5K"}`)
	require.JSONEq(t, `{"model":"v-doubao-seedream-5-0-pro","prompt":"x","size":"1536x1536"}`, body)
	require.Equal(t, "v-doubao-seedream-5-0-pro", public, "计费和日志要看到改写后的名字")
}

// C 端用户（没有组织身份）完全不受影响。
func TestRetailRequestIsNotRerouted(t *testing.T) {
	body, public := runOrgOverrideRequest(t, "",
		`{"model":"doubao-seedream-5-0-pro","prompt":"x","size":"1.5K"}`)
	require.JSONEq(t, `{"model":"doubao-seedream-5-0-pro","prompt":"x","size":"1.5K"}`, body)
	require.Equal(t, "doubao-seedream-5-0-pro", public)
}

// 改道表里没有的模型，组织请求照常走。
func TestOrganizationRequestWithoutOverrideIsUntouched(t *testing.T) {
	body, _ := runOrgOverrideRequest(t, "org-college", `{"model":"deepseek-v4-flash","messages":[]}`)
	require.JSONEq(t, `{"model":"deepseek-v4-flash","messages":[]}`, body)
}

// 只改表里列出的尺寸，别的尺寸原样交给上游。
func TestOrganizationRequestKeepsUnmappedSize(t *testing.T) {
	body, _ := runOrgOverrideRequest(t, "org-college",
		`{"model":"doubao-seedream-5-0-pro","prompt":"x","size":"2K"}`)
	require.JSONEq(t, `{"model":"v-doubao-seedream-5-0-pro","prompt":"x","size":"2K"}`, body)
}
