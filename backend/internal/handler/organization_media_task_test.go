package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

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
