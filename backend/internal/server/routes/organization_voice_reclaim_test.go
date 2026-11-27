package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type voiceReclaimHTTPStore struct {
	voiceassets.Store
	user   int64
	state  voiceassets.MemberState
	config voiceassets.ReclaimConfig
	writes int
}

func (s *voiceReclaimHTTPStore) TrackVoiceUsage(context.Context) (time.Time, error) {
	return time.Time{}, nil
}
func (s *voiceReclaimHTTPStore) ReclaimOrganizations(context.Context) ([]int64, error) {
	return nil, nil
}
func (s *voiceReclaimHTTPStore) TouchVoiceUse(context.Context, mediatasks.Owner, string) error {
	return nil
}
func (s *voiceReclaimHTTPStore) RecordMemberState(_ context.Context, user int64, state voiceassets.MemberState) error {
	s.user, s.state = user, state
	s.writes++
	return nil
}
func (s *voiceReclaimHTTPStore) Reclaim(_ context.Context, user int64, config voiceassets.ReclaimConfig) ([]voiceassets.ReclaimCandidate, error) {
	s.user, s.config = user, config
	return []voiceassets.ReclaimCandidate{{UserID: user, ActorID: "11111111-1111-4111-8111-111111111111", AssetID: "22222222-2222-4222-8222-222222222222", AccountID: 99, SpeakerID: "S_internal", Reason: "member_disabled"}}, nil
}

func voiceReclaimRouter(t *testing.T, store *voiceReclaimHTTPStore, accountType string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := handler.NewOrganizationMediaTaskHandler(nil, store, nil, nil, nil, nil)
	t.Cleanup(h.Stop)
	r := gin.New()
	registerOrganizationMediaTasks(r, h, middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		group := int64(4)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 7, GroupID: &group, Status: service.StatusActive,
			User: &service.User{ID: 7, AccountType: accountType}})
		c.Next()
	}))
	return r
}

func TestVoiceReclaimRoutesScopeValidateAndKeepPreviewReadOnly(t *testing.T) {
	t.Setenv("WXM_VOICE_MEMBER_RECLAIM_ENABLED", "true")
	t.Setenv("WXM_VOICE_MEMBER_RECLAIM_EXECUTE", "true")
	t.Setenv("WXM_VOICE_EXPIRY_RECLAIM_ENABLED", "true")
	t.Setenv("WXM_VOICE_EXPIRY_RECLAIM_EXECUTE", "true")
	t.Setenv("WXM_VOICE_EXPIRY_IDLE_DAYS", "180")
	store := &voiceReclaimHTTPStore{}
	r := voiceReclaimRouter(t, store, "organization_service")
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"actor_id":"11111111-1111-4111-8111-111111111111","state":"disabled","version":1}`, 200},
		{`{"actor_id":"11111111-1111-4111-8111-111111111111","state":"deleted","version":1,"user_id":8}`, 400},
		{`{"actor_id":"bad","state":"deleted","version":1}`, 400},
		{`{"actor_id":"11111111-1111-4111-8111-111111111111","state":"disabled","version":0}`, 400},
		{`{"actor_id":"11111111-1111-4111-8111-111111111111","state":"disabled","version":2} {}`, 400},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/v1/voice-assets/member-state", strings.NewReader(tc.body)))
		require.Equal(t, tc.status, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), "S_internal")
	}
	require.EqualValues(t, 7, store.user)
	require.Equal(t, 1, store.writes)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/voice-assets/reclaim-preview", nil))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.EqualValues(t, 7, store.user)
	require.False(t, store.config.MemberExecute || store.config.ExpiryExecute)
	require.Equal(t, 1, store.writes)
	require.NotContains(t, w.Body.String(), "S_internal")
	personal := voiceReclaimRouter(t, store, "personal")
	w = httptest.NewRecorder()
	personal.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/voice-assets/reclaim-preview", nil))
	require.Equal(t, 403, w.Code)
}

func TestVoiceReclaimDisabledRoutesDoNotWrite(t *testing.T) {
	t.Setenv("WXM_VOICE_MEMBER_RECLAIM_ENABLED", "false")
	t.Setenv("WXM_VOICE_MEMBER_RECLAIM_EXECUTE", "true")
	t.Setenv("WXM_VOICE_EXPIRY_RECLAIM_ENABLED", "false")
	t.Setenv("WXM_VOICE_EXPIRY_RECLAIM_EXECUTE", "true")
	store := &voiceReclaimHTTPStore{}
	r := voiceReclaimRouter(t, store, "organization_service")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/v1/voice-assets/member-state", strings.NewReader(`{"actor_id":"11111111-1111-4111-8111-111111111111","state":"deleted","version":1}`)))
	require.Equal(t, 503, w.Code)
	require.Zero(t, store.writes)
	require.Zero(t, store.user)
}
