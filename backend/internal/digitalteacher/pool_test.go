package digitalteacher

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPoolCallerTrust(t *testing.T) {
	g, e := New(nil, Options{CryptoSecret: strings.Repeat("s", 32), AnamPool: true, OrganizationKeys: "42", AnamPoolGroups: "1"})
	require.NoError(t, e)
	personal := Owner{UserID: 1, KeyID: 12, GroupID: 1}
	_, e = g.Caller(personal, "")
	require.NoError(t, e)
	_, e = g.Caller(personal, strings.Repeat("a", 64))
	require.Error(t, e)
	organization := personal
	organization.KeyID = 42
	for _, bad := range []string{"", "one", strings.Repeat("A", 64), strings.Repeat("a", 65)} {
		_, e = g.Caller(organization, bad)
		require.Error(t, e)
	}
	member, e := g.Caller(organization, strings.Repeat("a", 64))
	require.NoError(t, e)
	require.True(t, g.callerEnabled(member))
	g.anamPool = false
	require.False(t, g.callerEnabled(member))
}

func testPoolGateway(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	var o Owner
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'test',10) RETURNING id`, uuid.NewString()+"@example.invalid").Scan(&o.UserID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name) VALUES($1) RETURNING id`, uuid.NewString()).Scan(&o.GroupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,$2,'pool-test',$3) RETURNING id`, o.UserID, "test-"+uuid.NewString(), o.GroupID).Scan(&o.KeyID))
	g, e := New(db, Options{CryptoSecret: strings.Repeat("s", 32), AnamAPIKey: "synthetic", AnamPool: true, OrganizationKeys: strconv.FormatInt(o.KeyID, 10), AnamPoolGroups: strconv.FormatInt(o.GroupID, 10)})
	require.NoError(t, e)
	a, e := g.Caller(o, strings.Repeat("a", 64))
	require.NoError(t, e)
	b, e := g.Caller(o, strings.Repeat("b", 64))
	require.NoError(t, e)
	privateID := uuid.NewString()
	privateOrg := "private"
	g.anamCache.until = time.Now().Add(time.Hour)
	g.anamCache.avatars = []anamAsset{{ID: anamAvatar, Versions: []string{"cara-4"}}, {ID: privateID, Private: &privateOrg, Versions: []string{"cara-4"}}}
	g.anamCache.voices = []anamAsset{{ID: anamVoice}}
	g.anamCache.models = []anamAsset{{ID: anamModel, Global: true}}
	_, e = db.Exec(`INSERT INTO digital_teacher_assets(user_id,caller_scope,kind,asset_id,provider) VALUES($1,$2,'avatar',$3,'anam')`, o.UserID, a.Scope, privateID)
	require.NoError(t, e)
	cat, e := g.Catalog(ctx, a)
	require.NoError(t, e)
	require.Len(t, cat.Avatars, 2)
	cat, e = g.Catalog(ctx, b)
	require.NoError(t, e)
	require.Len(t, cat.Avatars, 1)
	cat, e = g.Catalog(ctx, o)
	require.NoError(t, e)
	require.Len(t, cat.Avatars, 1)
	teacher := Teacher{Provider: "anam", ID: uuid.NewString(), Name: "member A", Subject: "数学", Instructions: "中文", AvatarID: anamAvatar, VoiceID: anamVoice, LLMModel: anamExternalModel, VoiceModel: "cara-4"}
	_, e = g.SaveTeacher(ctx, a, teacher)
	require.NoError(t, e)
	teacher.Name = "member B"
	_, e = g.SaveTeacher(ctx, b, teacher)
	require.NoError(t, e)
	teachers, e := g.Teachers(ctx, a)
	require.NoError(t, e)
	require.Equal(t, "member A", teachers[0].Name)
	teachers, e = g.Teachers(ctx, b)
	require.NoError(t, e)
	require.Equal(t, "member B", teachers[0].Name)
	teachers, e = g.Teachers(ctx, o)
	require.NoError(t, e)
	require.Empty(t, teachers)
	// A member may not borrow another member's private image.
	teacher.AvatarID = privateID
	_, e = g.SaveTeacher(ctx, b, teacher)
	require.Error(t, e)
	teacher.AvatarID = anamAvatar
	settings, e := g.Settings(ctx, a)
	require.NoError(t, e)
	require.Equal(t, true, settings["configured"])
	require.Equal(t, "anam-cara-4", settings["model"])
	require.Equal(t, 90, settings["trialSeconds"])
	require.Equal(t, map[string]any{"configured": false}, settings["minutes"])
	var mu sync.Mutex
	labels := map[string]string{}
	starts := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/engine/session" {
			var body struct {
				Label  string         `json:"clientLabel"`
				Config map[string]any `json:"personaConfig"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.EqualValues(t, 90, body.Config["maxSessionLengthSeconds"])
			require.Equal(t, anamExternalModel, body.Config["llmId"])
			id := uuid.NewString()
			labels[id] = body.Label
			starts++
			_ = json.NewEncoder(w).Encode(map[string]any{"sessionId": id, "engineHost": "connect-us.anam.ai/v1/webrtc/engines/synthetic", "engineProtocol": "https", "signallingEndpoint": "/ws", "clientConfig": map[string]any{}})
		} else if strings.HasPrefix(r.URL.Path, "/v1/sessions/") {
			id := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")[0]
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "clientLabel": labels[id], "endTime": time.Now().UTC(), "sessionLengthMs": 2500})
		} else {
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	g.anamURL = upstream.URL
	req := StartRequest{TeacherID: teacher.ID, RequestID: uuid.NewString(), Minutes: 1, BrainMode: "agent"}
	first, e := g.StudioCreate(ctx, a, req)
	require.NoError(t, e, "pool access needs neither a personal allowlist nor a minute grant")
	again, e := g.StudioCreate(ctx, a, req)
	require.NoError(t, e)
	require.Equal(t, first["sessionId"], again["sessionId"])
	require.Equal(t, 1, starts)
	second, e := g.StudioCreate(ctx, b, req)
	require.NoError(t, e)
	require.NotEqual(t, first["sessionId"], second["sessionId"])
	_, e = g.StudioStop(ctx, b, first["sessionId"].(string))
	require.ErrorIs(t, e, ErrNotFound)
	history, e := g.StudioList(ctx, b)
	require.NoError(t, e)
	require.Len(t, history, 1)
	require.Equal(t, second["sessionId"], history[0].ID)
	_, e = g.AnamSDK(ctx, first["sessionToken"].(string))
	require.NoError(t, e)
	revoked, e := g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: false, MaxMinutes: 1})
	require.NoError(t, e)
	_, e = g.AnamSDK(ctx, first["sessionToken"].(string))
	require.Error(t, e, "explicit group revocation overrides pool defaults")
	_, e = g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: true, MaxMinutes: 1, Version: revoked.Version})
	require.NoError(t, e)
	stop, e := g.StudioStop(ctx, a, first["sessionId"].(string))
	require.NoError(t, e)
	require.Equal(t, true, stop["stopped"])
	_, e = g.StudioStop(ctx, b, second["sessionId"].(string))
	require.NoError(t, e)
	// Counts belong to a member across all connection keys, not the shared account.
	_, e = db.Exec(`INSERT INTO digital_teacher_studio_sessions(id,user_id,api_key_id,caller_scope,request_id,digest,deadline,closed_at,phase,provider) SELECT gen_random_uuid(),$1,$2,$3,gen_random_uuid()::text,'test',NOW(),NOW(),'closed','anam' FROM generate_series(1,4)`, o.UserID, o.KeyID, a.Scope)
	require.NoError(t, e)
	req.RequestID = uuid.NewString()
	_, e = g.StudioCreate(ctx, a, req)
	require.ErrorContains(t, e, "5 次")
	third, e := g.StudioCreate(ctx, b, req)
	require.NoError(t, e)
	require.Equal(t, 3, starts)
	_, e = g.StudioStop(ctx, b, third["sessionId"].(string))
	require.NoError(t, e)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, o.UserID).Scan(&balance))
	require.Equal(t, 10.0, balance)
	var rows int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM digital_teacher_minutes WHERE user_id=$1`, o.UserID).Scan(&rows))
	require.Zero(t, rows)
	// Avatar job metadata cannot be read via another member's ID.
	job := uuid.NewString()
	_, e = db.Exec(`INSERT INTO digital_teacher_avatar_jobs(id,user_id,caller_scope,request_id,name,digest,preview,state,generation) VALUES($1,$2,$3,$4,'private','test',$5,'failed','cara-4')`, job, o.UserID, a.Scope, uuid.NewString(), []byte{1})
	require.NoError(t, e)
	jobs, e := g.AvatarJobs(ctx, b)
	require.NoError(t, e)
	require.Empty(t, jobs)
	_, e = g.RefreshAvatar(ctx, b, job)
	require.ErrorIs(t, e, ErrNotFound)
	jobs, e = g.AvatarJobs(ctx, a)
	require.NoError(t, e)
	require.Len(t, jobs, 1)
}
