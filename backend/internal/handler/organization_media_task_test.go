package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOrganizationModel3DCatalogAdvertisesImageAndText(t *testing.T) {
	t.Setenv("WXM_MODEL3D_IMAGE_ENABLED", "true")
	gin.SetMode(gin.TestMode)
	accounts := &mediaTaskAccountRepoFixture{accounts: []service.Account{nativeMediaAccount(mediatasks.Model3D)}}
	h := NewOrganizationMediaTaskHandler(nil, nil, accounts, nil,
		mediaTaskGroupRepoFixture{group: publishedMediaGroup(mediatasks.TripoModel)}, nil)
	h.enabled = true
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/v1/media/models", nil)
	group := int64(7)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group,
		User: &service.User{ID: 1, AccountType: "organization_service"}})
	h.Models(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"modes":["text","image"]`)
	require.Contains(t, w.Body.String(), `"image_available":true`)
	require.Contains(t, w.Body.String(), `"id":"tripo-v3.1-standard"`)
}

func TestOrganizationModel3DImageGateClosedCatalogAndError(t *testing.T) {
	t.Setenv("WXM_MODEL3D_IMAGE_ENABLED", "")
	gin.SetMode(gin.TestMode)
	accounts := &mediaTaskAccountRepoFixture{accounts: []service.Account{nativeMediaAccount(mediatasks.Model3D)}}
	h := NewOrganizationMediaTaskHandler(nil, nil, accounts, nil,
		mediaTaskGroupRepoFixture{group: publishedMediaGroup(mediatasks.TripoModel)}, nil)
	h.enabled = true
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/v1/media/models", nil)
	group := int64(7)
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group,
		User: &service.User{ID: 1, AccountType: "organization_service"}})
	h.Models(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"modes":["text"]`)
	require.Contains(t, w.Body.String(), `"image_available":false`)

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	mediaTaskError(c, mediatasks.ErrImageUnavailable)
	require.Equal(t, 422, w.Code)
	require.Contains(t, w.Body.String(), `"code":"model3d_image_unavailable"`)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func nativeMediaAccount(kind string) service.Account {
	provider, base, model, native := "volcengine_speech", "https://openspeech.bytedance.com", mediatasks.SpeechModel, mediatasks.SpeechModel
	if kind == mediatasks.Model3D {
		provider, base, model, native = "tripo", "https://openapi.tripo3d.com/v3", mediatasks.TripoModel, mediatasks.TripoNativeModel
	}
	return service.Account{ID: 9, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Extra: map[string]any{"media_provider": provider}, Credentials: map[string]any{"base_url": base, "api_key": "fixture", "model_mapping": map[string]any{model: native}}}
}

type mediaTaskAccountRepoFixture struct {
	service.AccountRepository
	accounts []service.Account
	group    int64
}
type mediaTaskGroupRepoFixture struct {
	service.GroupRepository
	group *service.Group
}

func (r mediaTaskGroupRepoFixture) GetByID(context.Context, int64) (*service.Group, error) {
	return r.group, nil
}

type mediaTaskCompositeRepoFixture struct {
	service.CompositeModelRouteRepository
	routes []service.CompositeModelRoute
}

func (r mediaTaskCompositeRepoFixture) ListByGroup(context.Context, int64, bool) ([]service.CompositeModelRoute, error) {
	return r.routes, nil
}
func publishedMediaGroup(model string) *service.Group {
	return &service.Group{ID: 7, Status: service.StatusActive, Platform: service.PlatformOpenAI, ModelsListConfig: service.GroupModelsListConfig{Enabled: true, Models: []string{model}}}
}

func (r *mediaTaskAccountRepoFixture) ListSchedulableByGroupID(_ context.Context, group int64) ([]service.Account, error) {
	r.group = group
	return r.accounts, nil
}
func (r *mediaTaskAccountRepoFixture) ListByGroup(_ context.Context, group int64) ([]service.Account, error) {
	r.group = group
	return r.accounts, nil
}
func TestOrganizationMediaOfficialRoutesNeverSelectReseller(t *testing.T) {
	for _, kind := range []string{mediatasks.Audio, mediatasks.Model3D} {
		model := mediatasks.SpeechModel
		if kind == mediatasks.Model3D {
			model = mediatasks.TripoModel
		}
		a := nativeMediaAccount(kind)
		require.True(t, mediaTaskAccount(a, kind, model))
		a.Credentials["base_url"] = "https://toapis.cn/v1"
		require.False(t, mediaTaskAccount(a, kind, model))
		a = nativeMediaAccount(kind)
		delete(a.Extra, "media_provider")
		require.False(t, mediaTaskAccount(a, kind, model))
		a = nativeMediaAccount(kind)
		a.Credentials["model_mapping"] = map[string]any{"*": model}
		require.False(t, mediaTaskAccount(a, kind, model))
		a = nativeMediaAccount(kind)
		delete(a.Credentials, "api_key")
		repo := &mediaTaskAccountRepoFixture{accounts: []service.Account{a}}
		_, err := (mediaTaskRoutes{accounts: repo, groups: mediaTaskGroupRepoFixture{group: publishedMediaGroup(model)}}).Resolve(context.Background(), mediatasks.Owner{UserID: 1, GroupID: 7}, kind, model)
		require.ErrorIs(t, err, mediatasks.ErrCredentials)
	}
}
func TestCloneSlotsRequireExplicitPublishedModelAndPrepaidAllowList(t *testing.T) {
	a := nativeMediaAccount(mediatasks.Audio)
	a.Extra["voice_clone_speaker_ids"] = []string{"S_prepaid", "custom_speaker_id", "https://example.invalid/voice"}
	repo := &mediaTaskAccountRepoFixture{accounts: []service.Account{a}}
	r := mediaTaskRoutes{accounts: repo, groups: mediaTaskGroupRepoFixture{group: publishedMediaGroup(mediatasks.SpeechModel)}}
	o := mediatasks.Owner{UserID: 1, GroupID: 7}
	_, err := r.CloneSlots(context.Background(), o)
	require.ErrorIs(t, err, mediatasks.ErrRoute)
	a.Credentials["model_mapping"] = map[string]any{mediatasks.CloneModel: mediatasks.CloneModel}
	repo.accounts = []service.Account{a}
	r.groups = mediaTaskGroupRepoFixture{group: publishedMediaGroup(mediatasks.CloneModel)}
	slots, err := r.CloneSlots(context.Background(), o)
	require.NoError(t, err)
	require.Len(t, slots, 1)
	require.Equal(t, "S_prepaid", slots[0].SpeakerID)
	a.Credentials["base_url"] = "https://toapis.cn/v1"
	repo.accounts = []service.Account{a}
	slots, err = r.CloneSlots(context.Background(), o)
	require.NoError(t, err)
	require.Empty(t, slots)
}
func TestOrganizationMediaReopenUsesOriginalAccountAndGroup(t *testing.T) {
	repo := &mediaTaskAccountRepoFixture{accounts: []service.Account{nativeMediaAccount(mediatasks.Model3D)}}
	routes := mediaTaskRoutes{accounts: repo}
	task := &mediatasks.Task{Owner: mediatasks.Owner{UserID: 1, APIKeyID: 7, GroupID: 3}, Kind: mediatasks.Model3D, Model: mediatasks.TripoModel, AccountID: 9}
	p, err := routes.Reopen(context.Background(), mediatasks.Owner{UserID: 1, APIKeyID: 88, GroupID: 10}, task)
	require.NoError(t, err)
	require.EqualValues(t, 3, repo.group)
	require.EqualValues(t, 9, p.AccountID)
	_, err = routes.Reopen(context.Background(), mediatasks.Owner{UserID: 2, GroupID: 3}, task)
	require.ErrorIs(t, err, mediatasks.ErrNotFound)
	task.AccountID = 10
	_, err = routes.Reopen(context.Background(), mediatasks.Owner{UserID: 1, GroupID: 3}, task)
	require.ErrorIs(t, err, mediatasks.ErrRoute)
}

func TestOrganizationMediaRequiresPublishedModelAndExactEndpoint(t *testing.T) {
	ctx := context.Background()
	o := mediatasks.Owner{UserID: 1, GroupID: 7}
	model := mediatasks.TripoModel
	g := publishedMediaGroup(model)
	accounts := &mediaTaskAccountRepoFixture{accounts: []service.Account{nativeMediaAccount(mediatasks.Model3D)}}
	routes := mediaTaskRoutes{accounts: accounts, groups: mediaTaskGroupRepoFixture{group: g}}
	_, err := routes.Resolve(ctx, o, mediatasks.Model3D, model)
	require.NoError(t, err)
	g.ModelsListConfig.Models = []string{mediatasks.SpeechModel}
	_, err = routes.Resolve(ctx, o, mediatasks.Model3D, model)
	require.ErrorIs(t, err, mediatasks.ErrRoute)
	g.ModelsListConfig.Models = []string{model}
	g.Platform = service.PlatformComposite
	route := service.CompositeModelRoute{GroupID: 7, PublicModel: model, MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformOpenAI, UpstreamModel: mediatasks.TripoNativeModel, Endpoint: service.CompositeRouteEndpointModel3D, Enabled: true}
	composite := &mediaTaskCompositeRepoFixture{routes: []service.CompositeModelRoute{route}}
	routes.composite = service.NewCompositeRouteResolver(composite)
	_, err = routes.Resolve(ctx, o, mediatasks.Model3D, model)
	require.NoError(t, err)
	for _, endpoint := range []string{service.CompositeRouteEndpointAny, service.CompositeRouteEndpointChatCompletions, service.CompositeRouteEndpointAudio, "unrecognized"} {
		composite.routes[0].Endpoint = endpoint
		_, err = routes.Resolve(ctx, o, mediatasks.Model3D, model)
		require.ErrorIs(t, err, mediatasks.ErrRoute)
	}
}
