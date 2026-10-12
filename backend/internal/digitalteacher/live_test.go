package digitalteacher

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Explicit opt-in, sandbox only. This exercises real provider start/stop and
// receipt settlement; media delivery is verified separately in Electron.
func TestLiveAvatarSandbox(t *testing.T) {
	if os.Getenv("WINDHUB_DIGITAL_TEACHER_LIVE_TEST") != "1" {
		t.Skip("live sandbox acceptance requires explicit opt-in")
	}
	key := os.Getenv("LIVEAVATAR_API_KEY")
	require.NotEmpty(t, key)
	const dsn = "host=127.0.0.1 port=55439 user=digitalteacher_test sslmode=disable connect_timeout=5 dbname="
	admin, err := sql.Open("postgres", dsn+"postgres")
	require.NoError(t, err)
	defer admin.Close()
	name := fmt.Sprintf("digital_teacher_live_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	defer func() { _, _ = admin.Exec("DROP DATABASE " + name + " WITH (FORCE)") }()
	db, err := sql.Open("postgres", dsn+name)
	require.NoError(t, err)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	var o Owner
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance)VALUES('live-test@example.invalid','test',0)RETURNING id`).Scan(&o.UserID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name)VALUES('live sandbox')RETURNING id`).Scan(&o.GroupID))
	require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name,group_id)VALUES($1,$2,'test',$3)RETURNING id`, o.UserID, "sk-test-"+uuid.NewString(), o.GroupID).Scan(&o.KeyID))
	g, err := New(db, Options{APIKey: key, CryptoSecret: strings.Repeat("s", 32)})
	require.NoError(t, err)
	_, err = g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: true, SandboxEnabled: true, MaxMinutes: 1})
	require.NoError(t, err)
	require.NoError(t, g.Grant(ctx, o.UserID, "live-sandbox-grant", 1, "sandbox acceptance"))
	before, err := g.call(ctx, "GET", "/v1/users/credits", "", nil)
	require.NoError(t, err)
	t.Logf("credits before: %s", before)
	ticket, err := g.Create(ctx, o, StartRequest{RequestID: uuid.NewString(), Sandbox: true})
	if err != nil {
		t.Fatalf("create failed: %v, cause %v", err, errors.Unwrap(err))
	}
	defer func() { _, _ = g.Stop(context.Background(), o, ticket.SessionID) }()
	s, err := g.Get(ctx, o, ticket.SessionID)
	require.NoError(t, err)
	data, err := g.SDKStart(ctx, s)
	if err != nil {
		t.Fatalf("start failed: %v, cause %v", err, errors.Unwrap(err))
	}
	require.NotContains(t, string(data), "livekit_agent_token")
	t.Log("provider video-room start confirmed; credentials omitted")
	_, err = g.Stop(ctx, o, s.ID)
	if err != nil {
		t.Logf("initial stop pending: %v", err)
	}
	for i := 0; i < 18; i++ {
		time.Sleep(5 * time.Second)
		err = g.reconcileOne(ctx, s.ID, true)
		if err != nil {
			t.Logf("receipt pending: %v", err)
		}
		s, err = g.Get(ctx, o, s.ID)
		require.NoError(t, err)
		if s.SettledAt != nil {
			break
		}
	}
	if s.SettledAt == nil {
		data, e := g.call(ctx, "GET", "/v1/sessions/"+s.UpstreamID, "", nil)
		if e == nil {
			t.Logf("unsettled receipt: %s", data)
		}
	}
	require.NotNil(t, s.SettledAt)
	require.Equal(t, 0.0, *s.ProviderCredits)
	require.Equal(t, 0.0, *s.Charged)
	after, err := g.call(ctx, "GET", "/v1/users/credits", "", nil)
	require.NoError(t, err)
	t.Logf("credits after: %s; session settled", after)
}
