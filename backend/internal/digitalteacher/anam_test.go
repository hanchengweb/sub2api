package digitalteacher

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAnamEngineBoundary(t *testing.T) {
	for _, host := range []string{"connect-us.anam.ai", "connect-eu.anam.ai/v1/webrtc/engines/synthetic"} {
		raw, _ := json.Marshal(map[string]any{"sessionId": uuid.NewString(), "engineHost": host, "engineProtocol": "https", "signallingEndpoint": "/ws", "clientConfig": map[string]any{}})
		require.True(t, validAnamEngine(raw))
	}
	for _, host := range []string{"connect-us.anam.ai.evil.test", "user@connect-us.anam.ai", "connect-us.anam.ai:123", "connect-us.anam.ai/evil", "connect-us.anam.ai#x"} {
		raw, _ := json.Marshal(map[string]any{"sessionId": uuid.NewString(), "engineHost": host, "engineProtocol": "https", "signallingEndpoint": "/ws", "clientConfig": map[string]any{}})
		require.False(t, validAnamEngine(raw))
	}
	require.Empty(t, anamMediaURL("https://lab.anam.ai.evil.test/avatar"))
}
func testAnamGateway(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	var o Owner
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'test',10) RETURNING id`, uuid.NewString()+"@example.invalid").Scan(&o.UserID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name) VALUES($1) RETURNING id`, uuid.NewString()).Scan(&o.GroupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,$2,'test',$3) RETURNING id`, o.UserID, "test-"+uuid.NewString(), o.GroupID).Scan(&o.KeyID))
	g, e := New(db, Options{Provider: "simli", AnamAPIKey: "synthetic-key", AnamUsers: strconv.FormatInt(o.UserID, 10), CryptoSecret: strings.Repeat("s", 32)})
	require.NoError(t, e)
	private := uuid.NewString()
	generated := uuid.NewString()
	photoCalls := 0
	upstream := uuid.NewString()
	label := ""
	starts := 0
	receiptReady := false
	dropCreate := false
	mu := sync.Mutex{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer synthetic-key", r.Header.Get("Authorization"))
		var data any
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/avatars/" + generated:
			data = map[string]any{"id": generated, "displayName": "Generated", "createdByOrganizationId": "private-org", "availableVersions": []string{"cara-4"}}
		case "/v1/avatars":
			if r.Method == "POST" {
				photoCalls++
				require.NoError(t, r.ParseMultipartForm(6<<20))
				require.Equal(t, "cara-4", r.FormValue("avatarModel"))
				f, _, e := r.FormFile("imageFile")
				require.NoError(t, e)
				f.Close()
				_ = json.NewEncoder(w).Encode(map[string]any{"id": generated})
				return
			}
			data = map[string]any{"data": []any{map[string]any{"id": anamAvatar, "displayName": "Mia", "availableVersions": []string{"cara-4"}}, map[string]any{"id": private, "displayName": "Private teacher", "createdByOrganizationId": "private-org", "availableVersions": []string{"cara-4"}}}, "meta": map[string]any{"total": 2}}
		case "/v1/voices":
			data = map[string]any{"data": []any{map[string]any{"id": anamVoice, "displayName": "Richard zh", "country": "CN"}}, "meta": map[string]any{"total": 1}}
		case "/v1/llms":
			data = map[string]any{"data": []any{map[string]any{"id": anamModel, "displayName": "GPT 4.1", "isGlobal": true}}, "meta": map[string]any{"total": 1}}
		case "/v1/engine/session":
			starts++
			var body struct {
				Label   string         `json:"clientLabel"`
				Config  map[string]any `json:"personaConfig"`
				Options map[string]any `json:"sessionOptions"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			label = body.Label
			require.EqualValues(t, 90, body.Config["maxSessionLengthSeconds"])
			require.Equal(t, "zh", body.Config["languageCode"])
			require.EqualValues(t, 1152, body.Options["videoWidth"])
			if dropCreate {
				w.WriteHeader(503)
				return
			}
			data = map[string]any{"sessionId": upstream, "engineHost": "connect-us.anam.ai/v1/webrtc/engines/synthetic", "engineProtocol": "https", "signallingEndpoint": "/ws", "clientConfig": map[string]any{}}
		case "/v1/sessions":
			data = map[string]any{"data": []any{map[string]any{"id": upstream, "clientLabel": label}}}
		case "/v1/sessions/" + upstream + "/stop":
			data = map[string]bool{"accepted": true}
		case "/v1/sessions/" + upstream:
			data = map[string]any{"id": upstream, "clientLabel": label}
			if receiptReady {
				data = map[string]any{"id": upstream, "clientLabel": label, "endTime": time.Now().UTC(), "sessionLengthMs": 23450}
			}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer fake.Close()
	g.anamURL = fake.URL
	_, e = g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: true, MaxMinutes: 1})
	require.NoError(t, e)
	cat, e := g.Catalog(ctx, o)
	require.NoError(t, e)
	require.Len(t, cat.Avatars, 1, "private supplier assets must not be account-wide")
	_, e = db.Exec(`INSERT INTO digital_teacher_assets(user_id,kind,asset_id,provider) VALUES($1,'avatar',$2,'anam')`, o.UserID, private)
	require.NoError(t, e)
	cat, e = g.Catalog(ctx, o)
	require.NoError(t, e)
	require.Len(t, cat.Avatars, 2)
	teacher := Teacher{Provider: "anam", ID: uuid.NewString(), Name: "老师", Subject: "数学", Instructions: "中文", AvatarID: private, VoiceID: anamVoice, LLMModel: anamModel, VoiceModel: "cara-4"}
	_, e = g.SaveTeacher(ctx, o, teacher)
	require.NoError(t, e)
	req := StartRequest{RequestID: uuid.NewString(), TeacherID: teacher.ID, Minutes: 1}
	_, e = g.StudioCreate(ctx, o, req)
	require.Error(t, e)
	require.Equal(t, 0, starts)
	require.NoError(t, g.Grant(ctx, o.UserID, "anam-test", 1, "test"))
	var photo bytes.Buffer
	require.NoError(t, png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 512, 512))))
	photoRequest := uuid.NewString()
	job, e := g.CreateAvatar(ctx, o, photoRequest, "我的老师", true, photo.Bytes(), "cara-4")
	require.NoError(t, e)
	require.Equal(t, "ready", job.State)
	require.Equal(t, generated, job.FaceID)
	again, e := g.CreateAvatar(ctx, o, photoRequest, "我的老师", true, photo.Bytes(), "cara-4")
	require.NoError(t, e)
	require.Equal(t, job.ID, again.ID)
	require.Equal(t, 1, photoCalls)
	_, e = g.RefreshAvatar(ctx, Owner{UserID: o.UserID + 999}, job.ID)
	require.Error(t, e)
	var bound bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM digital_teacher_assets WHERE user_id=$1 AND asset_id=$2 AND provider='anam')`, o.UserID, generated).Scan(&bound))
	require.True(t, bound)

	_, e = g.Create(ctx, o, req)
	require.Error(t, e, "paid legacy route must not start Anam")
	// Switching providers must not consume the new provider's trial quota.
	_, e = db.Exec(`INSERT INTO digital_teacher_studio_sessions(id,user_id,api_key_id,request_id,digest,deadline,closed_at,phase,provider) SELECT gen_random_uuid(),$1,$2,gen_random_uuid()::text,'test',NOW(),NOW(),'closed','simli' FROM generate_series(1,5)`, o.UserID, o.KeyID)
	require.NoError(t, e)
	ticket, e := g.StudioCreate(ctx, o, req)
	require.NoError(t, e)
	require.Equal(t, "anam", ticket["engine"])
	id := ticket["sessionId"].(string)
	cap := ticket["sessionToken"].(string)
	safe, _ := json.Marshal(ticket)
	require.NotContains(t, string(safe), upstream)
	require.NotContains(t, string(safe), "synthetic-key")
	replay, e := g.StudioCreate(ctx, o, req)
	require.NoError(t, e)
	require.Equal(t, ticket, replay)
	require.Equal(t, 1, starts)
	for i := 0; i < 2; i++ {
		raw, e := g.AnamSDK(ctx, cap)
		require.NoError(t, e)
		require.Contains(t, string(raw), upstream)
	}
	require.Equal(t, 1, starts, "SDK retries only read the same session")
	_, e = g.AnamSDK(ctx, cap+"x")
	require.Error(t, e)
	_, e = db.Exec(`UPDATE api_keys SET status='disabled' WHERE id=$1`, o.KeyID)
	require.NoError(t, e)
	_, e = g.AnamSDK(ctx, cap)
	require.Error(t, e)
	_, e = db.Exec(`UPDATE api_keys SET status='active' WHERE id=$1`, o.KeyID)
	require.NoError(t, e)
	_, e = g.StudioStop(ctx, Owner{UserID: o.UserID + 999, KeyID: o.KeyID}, id)
	require.Error(t, e)
	stop, e := g.StudioStop(ctx, o, id)
	require.NoError(t, e)
	require.Equal(t, true, stop["settlement_pending"], "stop accepted is not a closed receipt")
	_, e = g.AnamSDK(ctx, cap)
	require.Error(t, e)
	_, e = db.Exec(`UPDATE digital_teacher_studio_sessions SET deadline=NOW()-interval '1 second' WHERE id=$1`, id)
	require.NoError(t, e)
	list, e := g.StudioList(ctx, o)
	require.NoError(t, e)
	require.Nil(t, list[0].ClosedAt, "a local deadline cannot invent an Anam receipt")
	mu.Lock()
	receiptReady = true
	mu.Unlock()
	stop, e = g.StudioStop(ctx, o, id)
	require.NoError(t, e)
	require.Equal(t, true, stop["stopped"])
	require.Equal(t, 23.45, *stop["duration_seconds"].(*float64))
	minutes, e := g.Minutes(ctx, o.UserID)
	require.NoError(t, e)
	require.Equal(t, 1.0, minutes["available"])
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, o.UserID).Scan(&balance))
	require.Equal(t, 10.0, balance)
	// An upstream create succeeded but its HTTP response was lost. No repeat POST.
	mu.Lock()
	dropCreate = true
	upstream = uuid.NewString()
	receiptReady = false
	mu.Unlock()
	req.RequestID = uuid.NewString()
	_, e = g.StudioCreate(ctx, o, req)
	require.Error(t, e)
	_, e = g.StudioCreate(ctx, o, req)
	require.Error(t, e)
	require.Equal(t, 2, starts)
	_, e = db.Exec(`UPDATE digital_teacher_studio_sessions SET created_at=NOW()-interval '70 seconds' WHERE request_id=$1`, req.RequestID)
	require.NoError(t, e)
	list, e = g.StudioList(ctx, o)
	require.NoError(t, e)
	for _, s := range list {
		if s.RequestID == req.RequestID {
			require.NoError(t, g.reconcileAnamOne(ctx, s))
		}
	}
	mu.Lock()
	receiptReady = true
	mu.Unlock()
	list, e = g.StudioList(ctx, o)
	require.NoError(t, e)
	for _, s := range list {
		if s.RequestID == req.RequestID {
			stop, e = g.StudioStop(ctx, o, s.ID)
			require.NoError(t, e)
			require.Equal(t, true, stop["stopped"])
		}
	}
	_, e = db.Exec(`INSERT INTO digital_teacher_studio_sessions(id,user_id,api_key_id,request_id,digest,deadline,closed_at,phase,provider) SELECT gen_random_uuid(),$1,$2,gen_random_uuid()::text,'test',NOW(),NOW(),'closed','anam' FROM generate_series(1,3)`, o.UserID, o.KeyID)
	require.NoError(t, e)
	_, e = g.StudioCreate(ctx, o, StartRequest{RequestID: uuid.NewString(), TeacherID: teacher.ID, Minutes: 1})
	require.ErrorContains(t, e, "5 次")
	require.Equal(t, 2, starts)
}
