package digitalteacher

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStudioRoomBoundary(t *testing.T) {
	require.True(t, validStudioRoom("https://simli.daily.co/room"))
	for _, v := range []string{"https://simli.daily.co.evil.test/a", "http://simli.daily.co/a", "https://user@simli.daily.co/a", "https://simli.daily.co/", "https://simli.daily.co/a#x"} {
		require.False(t, validStudioRoom(v))
	}
	require.Error(t, (Teacher{Provider: "simli", ID: uuid.NewString(), Name: "老师", Subject: "数学", Instructions: "中文", AvatarID: SimliAvatar, VoiceID: studioVoice, LLMModel: "arbitrary"}).Validate())
}
func testStudioGateway(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	var o Owner
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'test',10) RETURNING id`, uuid.NewString()+"@example.invalid").Scan(&o.UserID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name) VALUES($1) RETURNING id`, uuid.NewString()).Scan(&o.GroupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,$2,'test',$3) RETURNING id`, o.UserID, "test-"+uuid.NewString(), o.GroupID).Scan(&o.KeyID))
	g, e := New(db, Options{SimliAPIKey: "synthetic", Provider: "simli", StudioUsers: strconv.FormatInt(o.UserID, 10), CryptoSecret: strings.Repeat("s", 32)})
	require.NoError(t, e)
	var starts atomic.Int64
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/voices":
			require.Empty(t, r.Header.Get("x-simli-api-key"))
			_, _ = w.Write([]byte(`{"voices":[]}`))
		case "/auto/start/configurable":
			starts.Add(1)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "ElevenLabs", body["ttsProvider"])
			require.EqualValues(t, 60, body["maxSessionLength"])
			require.Equal(t, "eleven_multilingual_v2", body["ttsModel"])
			require.Equal(t, "artalk", body["model"])
			require.NotContains(t, body, "ttsAPIKey")
			_ = json.NewEncoder(w).Encode(map[string]string{"roomUrl": "https://simli.daily.co/synthetic-room", "sessionId": uuid.NewString()})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	g.simliURL = fake.URL
	g.elevenURL = fake.URL
	_, e = g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: true, MaxMinutes: 1})
	require.NoError(t, e)
	teacher := Teacher{Provider: "simli", ID: uuid.NewString(), Name: "林老师", Subject: "大学数学", Instructions: "用中文讲解", AvatarID: SimliAvatar, VoiceID: studioVoice, VoiceModel: studioVoiceModels[0], LLMModel: studioModels[0]}
	_, e = g.SaveTeacher(ctx, o, teacher)
	require.NoError(t, e)
	req := StartRequest{RequestID: uuid.NewString(), TeacherID: teacher.ID, Minutes: 1}
	_, e = g.StudioCreate(ctx, o, req)
	require.Error(t, e)
	require.EqualValues(t, 0, starts.Load())
	require.NoError(t, g.Grant(ctx, o.UserID, "studio-test", 1, "test"))
	ticket, e := g.StudioCreate(ctx, o, req)
	require.NoError(t, e)
	id := ticket["sessionId"].(string)
	replay, e := g.StudioCreate(ctx, o, req)
	require.NoError(t, e)
	require.Equal(t, ticket, replay)
	require.EqualValues(t, 1, starts.Load())
	_, e = db.Exec(`UPDATE api_keys SET status='disabled' WHERE id=$1`, o.KeyID)
	require.NoError(t, e)
	_, e = g.StudioCreate(ctx, o, req)
	require.Error(t, e, "a replay must not return a room after key revocation")
	_, e = db.Exec(`UPDATE api_keys SET status='active' WHERE id=$1`, o.KeyID)
	require.NoError(t, e)
	_, e = g.StudioStop(ctx, Owner{UserID: o.UserID + 999, KeyID: o.KeyID}, id)
	require.Error(t, e)
	list, e := g.StudioList(ctx, o)
	require.NoError(t, e)
	safe, _ := json.Marshal(list)
	require.NotContains(t, string(safe), "daily.co")
	require.NotContains(t, string(safe), "synthetic")
	_, e = g.StudioCreate(ctx, o, StartRequest{RequestID: uuid.NewString(), TeacherID: teacher.ID, Minutes: 1})
	require.Error(t, e)
	stopped, e := g.StudioStop(ctx, o, id)
	require.NoError(t, e)
	require.Equal(t, true, stopped["settlement_pending"])
	_, e = g.StudioCreate(ctx, o, req)
	require.Error(t, e)
	require.EqualValues(t, 1, starts.Load())
	_, e = db.Exec(`UPDATE digital_teacher_studio_sessions SET deadline=NOW()-interval '1 second' WHERE id=$1`, id)
	require.NoError(t, e)
	list, e = g.StudioList(ctx, o)
	require.NoError(t, e)
	require.NotNil(t, list[0].ClosedAt)
	minutes, e := g.Minutes(ctx, o.UserID)
	require.NoError(t, e)
	require.Equal(t, 1.0, minutes["available"])
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, o.UserID).Scan(&balance))
	require.Equal(t, 10.0, balance)
	_, e = db.Exec(`INSERT INTO digital_teacher_studio_sessions(id,user_id,api_key_id,request_id,digest,deadline,closed_at) SELECT gen_random_uuid(),$1,$2,gen_random_uuid()::text,'test',NOW(),NOW() FROM generate_series(1,4)`, o.UserID, o.KeyID)
	require.NoError(t, e)
	_, e = g.StudioCreate(ctx, o, StartRequest{RequestID: uuid.NewString(), TeacherID: teacher.ID, Minutes: 1})
	require.ErrorContains(t, e, "5 次")
	require.EqualValues(t, 1, starts.Load())
}
