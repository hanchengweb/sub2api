package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type voiceSlotManagerFixture struct {
	calls int
	err   error
	actor int64
}

func (m *voiceSlotManagerFixture) OrganizationSlots(_ context.Context, id int64, _ []int64) (*voiceassets.SlotSummary, error) {
	m.calls++
	return &voiceassets.SlotSummary{UserID: id, Allocatable: 2}, m.err
}
func (m *voiceSlotManagerFixture) SetOrganizationSlots(_ context.Context, id int64, _ []int64, c voiceassets.SlotChange, actor int64) (*voiceassets.SlotSummary, error) {
	m.calls++
	m.actor = actor
	return &voiceassets.SlotSummary{UserID: id, Managed: true, Version: c.ExpectedVersion + 1, Total: c.Total, Available: c.Total, RequestID: c.RequestID}, m.err
}

func voiceSlotTestRouter(m *voiceSlotManagerFixture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.users = []service.User{{ID: 42, AccountType: service.AccountTypeOrganizationService, OrganizationIssuer: "wemoreai-ops", OrganizationEnvironment: "test", OrganizationID: "college", AllowedGroups: []int64{4}}}
	h := NewAdminAPIKeyHandler(svc, nil)
	h.SetVoiceSlotManager(m)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9}) })
	r.GET("/org/:issuer/:environment/:organization_id/voice-clone-slots", h.OrganizationVoiceCloneSlots)
	r.PUT("/org/:issuer/:environment/:organization_id/voice-clone-slots", h.SetOrganizationVoiceCloneSlots)
	return r
}

func TestVoiceSlotAdminStrictRequestAndEnvelope(t *testing.T) {
	id := uuid.NewString()
	valid := `{"request_id":"` + id + `","expected_user_id":42,"expected_version":0,"total_slots":2,"reason":"contract"}`
	for _, body := range []string{
		`{"request_id":"` + id + `","expected_user_id":42,"expected_version":0,"total_slots":2.0,"reason":"contract"}`,
		`{"request_id":"` + id + `","expected_user_id":42,"expected_version":0,"total_slots":true,"reason":"contract"}`,
		`{"request_id":"` + id + `","expected_user_id":42,"expected_version":0,"total_slots":null,"reason":"contract"}`,
		`{"request_id":"` + id + `","expected_user_id":42,"total_slots":2,"reason":"contract"}`,
		valid + ` {}`, valid + strings.Repeat(" ", 16<<10) + `{}`, valid[:len(valid)-1] + `,"speaker_id":"S_private"}`,
	} {
		m := &voiceSlotManagerFixture{}
		r := voiceSlotTestRouter(m)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("PUT", "/org/wemoreai-ops/test/college/voice-clone-slots", bytes.NewBufferString(body))
		req.Header.Set("Idempotency-Key", id)
		r.ServeHTTP(rec, req)
		require.Equal(t, 400, rec.Code, body)
		require.Zero(t, m.calls)
		require.NotContains(t, rec.Body.String(), "S_private")
	}
	m := &voiceSlotManagerFixture{}
	r := voiceSlotTestRouter(m)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/org/wemoreai-ops/test/college/voice-clone-slots", bytes.NewBufferString(valid))
	req.Header.Set("Idempotency-Key", id)
	r.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
	require.EqualValues(t, 9, m.actor)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.EqualValues(t, 0, payload["code"])
	data := payload["data"].(map[string]any)
	require.Equal(t, "test", data["organization_environment"])
	require.Equal(t, "college", data["organization_id"])
	require.Equal(t, "wemoreai-ops", data["organization_issuer"])
	require.Equal(t, id, data["request_id"])
	require.NotContains(t, data, "speaker_id")
	require.NotContains(t, data, "credentials")
}

func TestVoiceSlotAdminErrorsStayFixedAndIdentityBound(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{voiceassets.ErrSlotVersion, 409, "voice_clone_slot_version_conflict"}, {voiceassets.ErrSlotIdentity, 409, "organization_identity_mismatch"},
		{voiceassets.ErrSlotInventory, 409, "voice_clone_slot_inventory_insufficient"}, {voiceassets.ErrSlotIdempotency, 409, "voice_clone_slot_idempotency_conflict"},
		{voiceassets.ErrSlotBelowUsed, 409, "voice_clone_slot_below_used"}, {errors.New("private-provider-detail"), 503, "voice_clone_slots_unavailable"},
	} {
		r := voiceSlotTestRouter(&voiceSlotManagerFixture{err: tc.err})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", "/org/wemoreai-ops/test/college/voice-clone-slots", nil))
		require.Equal(t, tc.status, rec.Code)
		require.Contains(t, rec.Body.String(), tc.code)
		require.NotContains(t, rec.Body.String(), "private-provider-detail")
	}
	m := &voiceSlotManagerFixture{}
	r := voiceSlotTestRouter(m)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", "/org/wemoreai-ops/production/college/voice-clone-slots", nil))
	require.Equal(t, 409, rec.Code)
	require.Zero(t, m.calls)
}
