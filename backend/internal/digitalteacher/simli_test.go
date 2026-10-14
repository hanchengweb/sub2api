package digitalteacher

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestSimliReceiptRequiresCompleteUniqueUsage(t *testing.T) {
	s := &Session{ID: uuid.NewString(), CreatedAt: time.Unix(1700000000, 0)}
	row := map[string]any{"id": uuid.NewString(), "sessionAggregator": s.ID, "sessionTotalTime": 11.5, "startTime": 1700000000, "endTime": 1700000012}
	receiptData := func(rows ...map[string]any) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"sessions": rows})
		return b
	}
	seconds, _, safe, err := simliReceipt(receiptData(row), s)
	require.NoError(t, err)
	require.Equal(t, 11.5, seconds)
	require.Contains(t, string(safe), `"supplier_financial_receipt":false`)
	for _, field := range []string{"sessionTotalTime", "startTime", "endTime"} {
		value := row[field]
		delete(row, field)
		_, _, _, err = simliReceipt(receiptData(row), s)
		require.Error(t, err, field)
		row[field] = value
	}
	_, _, _, err = simliReceipt(receiptData(row, row), s)
	require.Error(t, err)
	row["sessionAggregator"] = uuid.NewString()
	_, _, _, err = simliReceipt(receiptData(row), s)
	require.Error(t, err)
	row["sessionAggregator"] = s.ID
	row["sessionTotalTime"] = -1
	_, _, _, err = simliReceipt(receiptData(row), s)
	require.Error(t, err)
	row["sessionTotalTime"] = 60
	_, _, _, err = simliReceipt(receiptData(row), s)
	require.Error(t, err)
}

func testSimliGateway(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	g, err := New(db, Options{APIKey: "legacy-test", SimliAPIKey: "simli-test", Provider: "simli", CryptoSecret: strings.Repeat("s", 32)})
	require.NoError(t, err)
	var creates, connects atomic.Int64
	var mu sync.Mutex
	var aggregator string
	var history []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/compose/webrtc/livekit" {
			if r.URL.Query().Get("session_token") != "synthetic-simli-provider-token" {
				http.Error(w, "unauthorized", 401)
				return
			}
			upstream, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if e != nil {
				return
			}
			defer upstream.Close()
			connects.Add(1)
			_ = upstream.WriteMessage(websocket.TextMessage, []byte("READY"))
			for {
				kind, data, e := upstream.ReadMessage()
				if e != nil {
					return
				}
				if string(data) == "DONE" {
					return
				}
				_ = upstream.WriteMessage(kind, data)
			}
		}
		if r.Header.Get("x-simli-api-key") != "simli-test" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch r.URL.Path {
		case "/compose/token":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			aggregator, _ = body["sessionAggregator"].(string)
			mu.Unlock()
			creates.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"session_token": "synthetic-simli-provider-token"})
		case "/history/sessions":
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"sessions": history})
		case "/faces":
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	g.simliURL = fake.URL
	g.simliWSURL = strings.Replace(fake.URL, "http:", "ws:", 1)
	var o Owner
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES($1,'test',10) RETURNING id`, uuid.NewString()+"@example.invalid").Scan(&o.UserID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name) VALUES($1) RETURNING id`, uuid.NewString()).Scan(&o.GroupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,$2,'test',$3) RETURNING id`, o.UserID, "sk-test-"+uuid.NewString(), o.GroupID).Scan(&o.KeyID))
	_, err = g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: true, SandboxEnabled: true, MaxMinutes: 1})
	require.NoError(t, err)
	legacy := Teacher{ID: uuid.NewString(), Name: "林老师", Subject: "函数与卷积", Instructions: "用中文讲解", Opening: "欢迎", AvatarID: uuid.NewString(), VoiceID: uuid.NewString()}
	teacher := g.currentTeacher(legacy)
	require.Equal(t, legacy.Instructions, teacher.Instructions)
	require.Equal(t, SimliAvatar, teacher.AvatarID)
	_, err = g.SaveTeacher(ctx, o, teacher)
	require.NoError(t, err)
	req := func() StartRequest {
		return StartRequest{RequestID: uuid.NewString(), TeacherID: teacher.ID, Minutes: 1}
	}
	_, err = g.Create(ctx, o, req())
	require.Error(t, err)
	require.EqualValues(t, 0, creates.Load())
	require.NoError(t, g.Grant(ctx, o.UserID, "test-simli", 2, "test"))
	_, err = g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), Sandbox: true})
	require.Error(t, err)
	unstarted, err := g.Create(ctx, o, req())
	require.NoError(t, err)
	_, err = g.Stop(ctx, o, unstarted.SessionID)
	require.NoError(t, err)
	ended, err := g.Get(ctx, o, unstarted.SessionID)
	require.NoError(t, err)
	require.Equal(t, "failed", ended.Phase)
	ticket, err := g.Create(ctx, o, req())
	require.NoError(t, err)
	require.Equal(t, "simli", ticket.Provider)
	require.NotContains(t, ticket.SessionToken, "synthetic-simli")
	mu.Lock()
	require.Equal(t, ticket.SessionID, aggregator)
	mu.Unlock()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, e := g.ResolveCapability(r.Context(), r.URL.Query().Get("session_token"))
		if e != nil {
			http.Error(w, "invalid", 401)
			return
		}
		if e = g.ServeSimli(w, r, s); e != nil {
			http.Error(w, "not available", 409)
		}
	}))
	defer relay.Close()
	wsURL := strings.Replace(relay.URL, "http:", "ws:", 1) + "?session_token=" + ticket.SessionToken
	_, _, err = websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{"https://untrusted.invalid"}})
	require.Error(t, err)
	require.EqualValues(t, 0, connects.Load())
	client, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{"null"}})
	require.NoError(t, err)
	defer client.Close()
	_, ready, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, "READY", string(ready))
	_, _, err = websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{"null"}})
	require.Error(t, err)
	require.EqualValues(t, 1, connects.Load())
	require.NoError(t, client.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3, 4}))
	kind, data, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, kind)
	require.Equal(t, []byte{1, 2, 3, 4}, data)
	_, err = g.Stop(ctx, o, ticket.SessionID)
	require.NoError(t, err)
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err = client.ReadMessage()
	require.Error(t, err)
	s, err := g.Get(ctx, o, ticket.SessionID)
	require.NoError(t, err)
	require.Nil(t, s.SettledAt)
	// A complete, stable supplier history record is the only source of minutes.
	mu.Lock()
	history = []map[string]any{{"id": uuid.NewString(), "sessionAggregator": s.ID, "sessionTotalTime": 12.0, "startTime": s.CreatedAt.Unix(), "endTime": s.CreatedAt.Unix() + 12}}
	mu.Unlock()
	g.now = func() time.Time { return s.CreatedAt.Add(time.Minute) }
	require.NoError(t, g.reconcileOne(ctx, s.ID, false))
	_, err = db.Exec(`UPDATE digital_teacher_sessions SET receipt_seen_at=NOW()-INTERVAL '6 seconds' WHERE id=$1`, s.ID)
	require.NoError(t, err)
	require.NoError(t, g.reconcileOne(ctx, s.ID, false))
	require.NoError(t, g.reconcileOne(ctx, s.ID, false))
	settled, err := g.Get(ctx, o, s.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", settled.Phase)
	require.Nil(t, settled.ProviderCredits)
	minutes, err := g.Minutes(ctx, o.UserID)
	require.NoError(t, err)
	require.InDelta(t, 0.2, minutes["consumed"], 1e-8)
	require.Equal(t, 0.0, minutes["reserved"])
	var balance, frozen float64
	require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, o.UserID).Scan(&balance, &frozen))
	require.Equal(t, 10.0, balance)
	require.Equal(t, 0.0, frozen)
}
