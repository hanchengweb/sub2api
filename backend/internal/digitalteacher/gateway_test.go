package digitalteacher

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestReceiptValidation(t *testing.T) {
	a := json.RawMessage(`{"duration":30,"credits_consumed":"1.0","mode":"FULL","end_at":"2026-10-12T00:00:00Z","is_sandbox":false}`)
	credit, seconds, _, safe, err := receipt(a)
	require.NoError(t, err)
	require.Equal(t, 1.0, credit)
	require.Equal(t, 30.0, seconds)
	var decoded any
	require.NoError(t, json.Unmarshal(safe, &decoded))
	pretty, _ := json.MarshalIndent(decoded, "", " ")
	require.True(t, sameReceipt(safe, pretty))
	_, _, ended, _, err := receipt(json.RawMessage(strings.Replace(string(a), "2026-10-12T00:00:00Z", "2026-10-12T00:00:00", 1)))
	require.NoError(t, err)
	require.Equal(t, time.UTC, ended.Location())
	for _, bad := range []string{`{"end_at":null}`, `{"mode":"FULL","duration":1,"credits_consumed":"NaN","end_at":"2026-10-12T00:00:00Z"}`, `{"mode":"CUSTOM","duration":1,"credits_consumed":1,"end_at":"2026-10-12T00:00:00Z"}`} {
		_, _, _, _, err := receipt(json.RawMessage(bad))
		require.Error(t, err)
	}
}
func TestCryptoRequiresStableSecret(t *testing.T) {
	_, err := New(nil, Options{})
	require.Error(t, err)
	g, err := New(nil, Options{CryptoSecret: strings.Repeat("a", 32)})
	require.NoError(t, err)
	sealed, err := g.seal("synthetic-provider-token")
	require.NoError(t, err)
	require.NotContains(t, sealed, "synthetic")
	plain, err := g.unseal(sealed)
	require.NoError(t, err)
	require.Equal(t, "synthetic-provider-token", plain)
	_, err = g.unseal(sealed[:10] + "broken")
	require.Error(t, err)
}

// This suite intentionally uses only a named loopback test cluster. It cannot
// take an arbitrary DSN or accidentally migrate a production database.
func TestGatewayPostgres(t *testing.T) {
	if os.Getenv("WINDHUB_DIGITAL_TEACHER_TEST") != "1" {
		t.Skip("requires dedicated local PostgreSQL 55439")
	}
	const dsn = "host=127.0.0.1 port=55439 user=digitalteacher_test sslmode=disable connect_timeout=5 dbname="
	admin, err := sql.Open("postgres", dsn+"postgres")
	require.NoError(t, err)
	defer admin.Close()
	name := fmt.Sprintf("digital_teacher_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	defer func() { _, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)"); require.NoError(t, err) }()
	db, err := sql.Open("postgres", dsn+name)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(20)
	ctx := context.Background()
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	avatar, voice := uuid.NewString(), uuid.NewString()
	var created, started atomic.Int64
	var failStart atomic.Bool
	var mutex sync.Mutex
	ended := map[string]string{}
	sandbox := map[string]bool{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data any
		switch {
		case r.URL.Path == "/v1/avatars/public":
			data = map[string]any{"results": []any{map[string]any{"id": avatar, "name": "Public teacher", "status": "ACTIVE"}}, "next": nil}
		case r.URL.Path == "/v1/voices":
			data = map[string]any{"results": []any{map[string]any{"id": voice, "name": "Chinese voice", "language": "zh"}}, "next": nil}
		case r.URL.Path == "/v1/contexts" && r.Method == "POST":
			data = map[string]any{"id": uuid.NewString()}
		case r.URL.Path == "/v1/sessions/token":
			created.Add(1)
			id := uuid.NewString()
			var b struct {
				Sandbox bool `json:"is_sandbox"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			mutex.Lock()
			sandbox[id] = b.Sandbox
			mutex.Unlock()
			data = map[string]any{"session_id": id, "session_token": "synthetic-provider-token:" + id}
		case r.URL.Path == "/v1/sessions/start":
			started.Add(1)
			if failStart.Load() {
				http.Error(w, "unknown start outcome", 503)
				return
			}
			id := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-provider-token:")
			data = map[string]any{"session_id": id, "livekit_url": "wss://room.example.invalid", "livekit_client_token": "room-client", "livekit_agent_token": "must-not-leak"}
		case r.URL.Path == "/v1/sessions/stop":
			var b struct {
				ID string `json:"session_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			mutex.Lock()
			if ended[b.ID] == "" {
				ended[b.ID] = time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339)
			}
			mutex.Unlock()
			data = map[string]any{}
		case strings.HasPrefix(r.URL.Path, "/v1/sessions/") && r.Method == "GET":
			id := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
			mutex.Lock()
			end, isSandbox := ended[id], sandbox[id]
			mutex.Unlock()
			credits := 1.0
			if isSandbox {
				credits = 0
			}
			data = map[string]any{"id": id, "credits_consumed": credits, "duration": 30, "end_at": end, "mode": "FULL", "is_sandbox": isSandbox}
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1000, "data": data})
	}))
	defer fake.Close()
	g, err := New(db, Options{APIKey: "synthetic-only", CryptoSecret: strings.Repeat("t", 32)})
	require.NoError(t, err)
	g.upstream = fake.URL
	owner := func() Owner {
		var o Owner
		require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance)VALUES($1,'test',10)RETURNING id`, uuid.NewString()+"@example.invalid").Scan(&o.UserID))
		require.NoError(t, db.QueryRow(`INSERT INTO groups(name,is_exclusive)VALUES($1,TRUE)RETURNING id`, uuid.NewString()).Scan(&o.GroupID))
		_, err := db.Exec(`INSERT INTO user_allowed_groups(user_id,group_id)VALUES($1,$2)`, o.UserID, o.GroupID)
		require.NoError(t, err)
		require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id,quota,rate_limit_5h)VALUES($1,$2,'test',$3,20,20)RETURNING id`, o.UserID, "sk-test-"+uuid.NewString(), o.GroupID).Scan(&o.KeyID))
		_, err = g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: true, SandboxEnabled: true, PricePerMinute: 3, MaxMinutes: 2})
		require.NoError(t, err)
		return o
	}
	teacher := func(o Owner) Teacher {
		v := Teacher{ID: uuid.NewString(), Name: "林老师", Subject: "英语", Instructions: "帮助学生练习口语", AvatarID: avatar, VoiceID: voice}
		_, err := g.SaveTeacher(ctx, o, v)
		require.NoError(t, err)
		return v
	}
	settle := func(id string) {
		_, err := db.Exec(`UPDATE digital_teacher_sessions SET receipt_seen_at=NOW()-INTERVAL '6 seconds' WHERE id=$1`, id)
		require.NoError(t, err)
		require.NoError(t, g.reconcileOne(ctx, id, false))
	}
	totals := func(o Owner, balance, frozen, used float64) {
		var b, f, u float64
		require.NoError(t, db.QueryRow(`SELECT balance,frozen_balance FROM users WHERE id=$1`, o.UserID).Scan(&b, &f))
		require.NoError(t, db.QueryRow(`SELECT quota_used FROM api_keys WHERE id=$1`, o.KeyID).Scan(&u))
		require.InDelta(t, balance, b, 1e-8)
		require.InDelta(t, frozen, f, 1e-8)
		require.InDelta(t, used, u, 1e-8)
	}

	t.Run("concurrent create and start charge once and release unused minutes", func(t *testing.T) {
		o := owner()
		v := teacher(o)
		require.NoError(t, g.Grant(ctx, o.UserID, "initial-grant", 5, "test grant"))
		require.NoError(t, g.Grant(ctx, o.UserID, "initial-grant", 5, "test grant"))
		require.Error(t, g.Grant(ctx, o.UserID, "initial-grant", 6, "test grant"))
		req := StartRequest{RequestID: uuid.NewString(), TeacherID: v.ID, Minutes: 2}
		before := created.Load()
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, e := g.Create(ctx, o, req)
				if e != nil {
					t.Logf("create: %v; cause: %v", e, errors.Unwrap(e))
				}
			}()
		}
		wg.Wait()
		require.Equal(t, before+1, created.Load())
		list, err := g.List(ctx, o)
		require.NoError(t, err)
		require.Len(t, list, 1)
		s := list[0]
		require.Equal(t, "ready", s.Phase)
		totals(o, 4, 6, 6)
		require.NotContains(t, g.capability(s), "synthetic-provider")
		capability := g.capability(s)
		payload, err := base64.RawURLEncoding.DecodeString(strings.Split(capability, ".")[1])
		require.NoError(t, err)
		var claims capabilityClaims
		require.NoError(t, json.Unmarshal(payload, &claims))
		require.Equal(t, "FULL", claims.StartData.Mode)
		require.Equal(t, "FULL", claims.StartData.AgentType)
		resolved, err := g.ResolveCapability(ctx, capability)
		require.NoError(t, err)
		require.Equal(t, s.ID, resolved.ID)
		require.NotContains(t, s.UpstreamToken, "synthetic-provider")
		_, err = g.ResolveCapability(ctx, g.capability(s)+"x")
		require.Error(t, err)
		stranger := owner()
		_, err = g.Get(ctx, stranger, s.ID)
		require.Error(t, err)
		beforeStart := started.Load()
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, _ = g.SDKStart(ctx, s) }()
		}
		wg.Wait()
		require.Equal(t, beforeStart+1, started.Load())
		s, err = g.Get(ctx, o, s.ID)
		require.NoError(t, err)
		data, err := g.SDKStart(ctx, s)
		require.NoError(t, err)
		require.NotContains(t, string(data), "agent")
		require.NotContains(t, string(data), "synthetic-provider")
		_, err = g.Stop(ctx, o, s.ID)
		require.NoError(t, err)
		settle(s.ID)
		_, err = g.Stop(ctx, o, s.ID)
		require.NoError(t, err)
		totals(o, 8.5, 0, 1.5)
		m, err := g.Minutes(ctx, o.UserID)
		require.NoError(t, err)
		require.Equal(t, 0.5, m["consumed"])
		require.Equal(t, 0.0, m["reserved"])
		s, err = g.Get(ctx, o, s.ID)
		require.NoError(t, err)
		require.Equal(t, "completed", s.Phase)
		_, err = g.SDKStart(ctx, s)
		require.Error(t, err)
		_, err = g.Create(ctx, o, req)
		require.Error(t, err)
	})
	t.Run("sandbox works at zero balance and never charges", func(t *testing.T) {
		o := owner()
		_, err := db.Exec(`UPDATE users SET balance=0 WHERE id=$1`, o.UserID)
		require.NoError(t, err)
		ticket, err := g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), Sandbox: true})
		require.NoError(t, err)
		s, err := g.Get(ctx, o, ticket.SessionID)
		require.NoError(t, err)
		_, err = g.SDKStart(ctx, s)
		require.NoError(t, err)
		_, err = g.Stop(ctx, o, s.ID)
		require.NoError(t, err)
		settle(s.ID)
		totals(o, 0, 0, 0)
	})
	t.Run("free plan requires explicit minutes and balances remain untouched", func(t *testing.T) {
		o := owner()
		p, err := g.Plan(ctx, o.GroupID)
		require.NoError(t, err)
		p.PricePerMinute = 0
		_, err = g.SetPlan(ctx, p)
		require.NoError(t, err)
		v := teacher(o)
		req := StartRequest{RequestID: uuid.NewString(), TeacherID: v.ID, Minutes: 1}
		_, err = g.Create(ctx, o, req)
		require.Error(t, err)
		before := created.Load()
		_, err = g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), Sandbox: true})
		require.Error(t, err)
		require.Equal(t, before, created.Load())
		require.NoError(t, g.Grant(ctx, o.UserID, "test-trial-grant", 1, "bounded trial"))
		ticket, err := g.Create(ctx, o, req)
		require.NoError(t, err)
		s, err := g.Get(ctx, o, ticket.SessionID)
		require.NoError(t, err)
		_, err = g.Stop(ctx, o, s.ID)
		require.NoError(t, err)
		totals(o, 10, 0, 0)
	})
	t.Run("refund preserves spending in a newly opened rate window", func(t *testing.T) {
		o := owner()
		v := teacher(o)
		ticket, err := g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), TeacherID: v.ID, Minutes: 2})
		require.NoError(t, err)
		totals(o, 4, 6, 6)
		// Other requests reset two windows after this session's reservation.
		_, err = db.Exec(`UPDATE api_keys SET usage_5h=2,window_5h_start=NOW()+INTERVAL '1 second',usage_1d=3,window_1d_start=NOW()+INTERVAL '1 second' WHERE id=$1`, o.KeyID)
		require.NoError(t, err)
		_, err = g.Stop(ctx, o, ticket.SessionID)
		require.NoError(t, err)
		totals(o, 10, 0, 0)
		var five, day, week float64
		require.NoError(t, db.QueryRow(`SELECT usage_5h,usage_1d,usage_7d FROM api_keys WHERE id=$1`, o.KeyID).Scan(&five, &day, &week))
		require.Equal(t, 2.0, five)
		require.Equal(t, 3.0, day)
		require.Equal(t, 0.0, week)
	})
	t.Run("revoked group cannot start and unstarted reservation refunds", func(t *testing.T) {
		o := owner()
		v := teacher(o)
		ticket, err := g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), TeacherID: v.ID, Minutes: 1})
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM user_allowed_groups WHERE user_id=$1`, o.UserID)
		require.NoError(t, err)
		s, err := g.Get(ctx, o, ticket.SessionID)
		require.NoError(t, err)
		_, err = g.SDKStart(ctx, s)
		require.Error(t, err)
		_, err = g.Stop(ctx, o, s.ID)
		require.NoError(t, err)
		totals(o, 10, 0, 0)
	})
	t.Run("unknown start is never redispatched and recovery settles", func(t *testing.T) {
		o := owner()
		v := teacher(o)
		ticket, err := g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), TeacherID: v.ID, Minutes: 1})
		require.NoError(t, err)
		s, err := g.Get(ctx, o, ticket.SessionID)
		require.NoError(t, err)
		failStart.Store(true)
		_, err = g.SDKStart(ctx, s)
		require.Error(t, err)
		failStart.Store(false)
		s, err = g.Get(ctx, o, s.ID)
		require.NoError(t, err)
		require.True(t, s.StopRequested)
		_, err = g.SDKStart(ctx, s)
		require.Error(t, err)
		require.NoError(t, g.reconcileOne(ctx, s.ID, false))
		settle(s.ID)
		totals(o, 8.5, 0, 1.5)
	})
	t.Run("quota limit and unpaid minutes reject before supplier call", func(t *testing.T) {
		o := owner()
		v := teacher(o)
		_, err := db.Exec(`UPDATE api_keys SET quota=1 WHERE id=$1`, o.KeyID)
		require.NoError(t, err)
		before := created.Load()
		_, err = g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), TeacherID: v.ID, Minutes: 2})
		require.Error(t, err)
		require.Equal(t, before, created.Load())
		totals(o, 10, 0, 0)
	})
	t.Run("Simli gateway lifecycle", func(t *testing.T) { testSimliGateway(t, db) })
	t.Run("owned photo creation and recovery", func(t *testing.T) { testAvatarJobs(t, db) })
}
