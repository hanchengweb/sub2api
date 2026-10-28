package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type videoRuleRouteRepo []service.CompositeModelRoute

func (r videoRuleRouteRepo) ListByGroup(_ context.Context, groupID int64, includeDisabled bool) ([]service.CompositeModelRoute, error) {
	var routes []service.CompositeModelRoute
	for _, route := range r {
		if route.GroupID == groupID && (includeDisabled || route.Enabled) {
			routes = append(routes, route)
		}
	}
	return routes, nil
}
func (videoRuleRouteRepo) Create(context.Context, *service.CompositeModelRoute) error { return nil }
func (videoRuleRouteRepo) Update(context.Context, *service.CompositeModelRoute) error { return nil }
func (videoRuleRouteRepo) Delete(context.Context, int64) error                        { return nil }
func (videoRuleRouteRepo) DeleteByGroup(context.Context, int64) error                 { return nil }

// videoRuleAdminService resolves routes with the real resolver; only storage is stubbed.
type videoRuleAdminService struct {
	*stubAdminService
	platforms map[int64]string
	resolver  *service.CompositeRouteResolver
}

func (s *videoRuleAdminService) GetGroup(_ context.Context, id int64) (*service.Group, error) {
	platform, ok := s.platforms[id]
	if !ok {
		return nil, service.ErrGroupNotFound
	}
	return &service.Group{ID: id, Name: "group", Platform: platform, Status: service.StatusActive}, nil
}

func (s *videoRuleAdminService) PreviewCompositeRoute(ctx context.Context, groupID int64, input service.CompositeRoutePreviewRequest) (*service.CompositeRouteDecision, error) {
	decision, err := s.resolver.Resolve(ctx, groupID, input.Model, input.Endpoint)
	return &decision, err
}

type videoRuleOverrides map[string]service.OrganizationModelOverride

func (o videoRuleOverrides) OrganizationModelOverride(_ context.Context, model string) (service.OrganizationModelOverride, bool) {
	override, ok := o[model]
	return override, ok
}

func TestOrganizationVideoQuotaRuleSeesTheModelTheQuotaGuardSees(t *testing.T) {
	gin.SetMode(gin.TestMode)
	route := func(public, upstream string) service.CompositeModelRoute {
		return service.CompositeModelRoute{GroupID: 4, PublicModel: public, MatchType: service.CompositeRouteMatchExact,
			TargetPlatform: service.PlatformOpenAI, UpstreamModel: upstream, Endpoint: service.CompositeRouteEndpointAny,
			Priority: 1, Enabled: true}
	}
	// Group 4 mirrors production: composite, with toAPI display names rewritten.
	handler := NewGroupHandler(&videoRuleAdminService{
		stubAdminService: newStubAdminService(),
		platforms:        map[int64]string{4: service.PlatformComposite, 7: service.PlatformOpenAI},
		resolver: service.NewCompositeRouteResolver(videoRuleRouteRepo{
			route("t-grok-video-1.5", "grok-video-1.5"),
			route("veo3.1-fast", "veo3.1-fast"),
			route("cheap-video", "seedance-2-fast"),
			route("seedance-2-fast", "veo3.1-fast"),
		}),
	}, nil, nil)
	handler.SetOrganizationModelOverrides(videoRuleOverrides{"old-video": {Model: "cheap-video"}})
	router := gin.New()
	router.GET("/api/v1/admin/groups/:id/media-quota/video-rule", handler.OrganizationVideoQuotaRule)
	ask := func(group, model string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			"/api/v1/admin/groups/"+group+"/media-quota/video-rule?model="+url.QueryEscape(model), nil))
		var body struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.Data
	}

	for _, tc := range []struct {
		group, model, quotaModel string
		supported                bool
	}{
		{"4", "t-grok-video-1.5", "grok-video-1.5", true},
		{"4", "veo3.1-fast", "veo3.1-fast", false},
		{"4", "gemini_omni_flash", "gemini_omni_flash", true},
		// A route can turn a name that has a contract into an upstream model that has none.
		{"4", "seedance-2-fast", "veo3.1-fast", false},
		// The organization override applies before the route, as in the gateway.
		{"4", "old-video", "seedance-2-fast", true},
		// Requests in other groups are not rewritten.
		{"7", "old-video", "old-video", false},
		{"7", "seedance-2-fast", "seedance-2-fast", true},
	} {
		code, data := ask(tc.group, tc.model)
		require.Equal(t, http.StatusOK, code, tc.model)
		require.Equal(t, tc.model, data["model"], tc.model)
		require.Equal(t, tc.quotaModel, data["quota_model"], tc.model)
		require.Equal(t, tc.supported, data["supported"], tc.model)
		_, hasRule := data["rule"]
		require.Equal(t, tc.supported, hasRule, tc.model)
	}

	_, data := ask("4", "gemini_omni_flash")
	require.Equal(t, map[string]any{"min_seconds": 4.0, "max_seconds": 10.0, "default_seconds": 8.0,
		"allowed_seconds": []any{4.0, 6.0, 8.0, 10.0}}, data["rule"])

	code, _ := ask("4", " ")
	require.Equal(t, http.StatusBadRequest, code)
	code, _ = ask("x", "veo3.1-fast")
	require.Equal(t, http.StatusBadRequest, code)
	code, _ = ask("5", "veo3.1-fast")
	require.Equal(t, http.StatusNotFound, code)
}
