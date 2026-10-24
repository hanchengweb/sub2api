package collaboration

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// This optional contract test runs the actual Electron main-process client in
// Node, not a hand-reimplemented HTTP mock. Both opt-ins are required; every
// request is remapped by the test-only fetch adapter to an ephemeral loopback
// server. It never reads application credentials, databases or upstream APIs.
func TestDesktopBridgeContract(t *testing.T) {
	clientSource := os.Getenv("WXM_DESKTOP_COLLABORATION_CLIENT")
	if clientSource == "" || os.Getenv("WINDHUB_COLLABORATION_PG_TEST") != "1" {
		t.Skip("requires explicit desktop source and isolated PostgreSQL opt-in")
	}
	clientSource, err := filepath.Abs(clientSource)
	require.NoError(t, err)
	info, err := os.Stat(clientSource)
	require.NoError(t, err)
	require.False(t, info.IsDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	const base = "host=127.0.0.1 port=55479 user=collaboration_test sslmode=disable connect_timeout=5 dbname="
	admin, err := sql.Open("postgres", base+"postgres")
	require.NoError(t, err)
	name := fmt.Sprintf("collaboration_bridge_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	db, err := sql.Open("postgres", base+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	keys := fixtureKeys{keys: map[int64]*service.APIKey{}}
	byToken := map[string]*service.APIKey{}
	for i := 1; i <= 2; i++ {
		var uid, kid int64
		require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,username,balance) VALUES($1,'fixture',$2,10) RETURNING id`, fmt.Sprintf("bridge-%d@example.invalid", i), fmt.Sprintf("Bridge member %d", i)).Scan(&uid))
		require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name) VALUES($1,$2,'bridge fixture') RETURNING id`, uid, fmt.Sprintf("fixture-%d", i)).Scan(&kid))
		key := testKey(uid, kid)
		keys.keys[kid] = key
		byToken[key.Key] = key
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	auth := func(c *gin.Context) {
		key := byToken[strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")]
		if key == nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "fixture authentication required"})
			return
		}
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Next()
	}
	r.GET("/v1/models", auth, func(c *gin.Context) {
		c.JSON(200, gin.H{"data": []gin.H{{"id": "deepseek-chat", "name": "DeepSeek"}, {"id": "gpt-image-2"}}})
	})
	type call struct {
		KeyID     int64
		RequestID string
	}
	called := make(chan call, 8)
	r.POST("/v1/chat/completions", auth, func(c *gin.Context) {
		key, _ := middleware.GetAPIKeyFromContext(c)
		called <- call{key.ID, c.GetHeader("X-Client-Request-ID")}
		c.JSON(200, gin.H{"choices": []gin.H{{"finish_reason": "stop", "message": gin.H{"content": "真实跨端契约的合成模型回复"}}}})
	})
	store := NewStore(db)
	gateway := InternalGateway{Engine: r}
	Register(r, &Handler{Store: store, Gateway: gateway, Enabled: true}, auth)
	server := httptest.NewServer(r)
	defer server.Close()
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	(&Worker{Store: store, Keys: keys, Gateway: gateway}).Start(workerCtx)
	command := exec.CommandContext(ctx, node, filepath.Join("testdata", "desktop-bridge.cjs"))
	command.Env = append(os.Environ(),
		"WXM_DESKTOP_COLLABORATION_CLIENT="+clientSource,
		"WXM_COLLABORATION_TEST_ENDPOINT="+server.URL,
		"WXM_COLLABORATION_TEST_DIRECTORY="+t.TempDir())
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), `"bridgeContract":"passed"`)
	t.Logf("%s", output)
	stopWorker()
	require.Len(t, called, 2, "two explicit agent mentions should produce exactly two synthetic gateway calls")
	for len(called) > 0 {
		got := <-called
		require.EqualValues(t, 2, got.KeyID, "requester must pay, not room owner")
		require.True(t, strings.HasPrefix(got.RequestID, "collab:"))
	}
}
