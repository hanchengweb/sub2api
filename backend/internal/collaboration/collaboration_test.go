package collaboration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func testKey(uid, kid int64) *service.APIKey {
	group := int64(4)
	return &service.APIKey{ID: kid, UserID: uid, Key: fmt.Sprintf("fixture-%d", kid), Status: service.StatusActive, GroupID: &group, Group: &service.Group{ID: 4, Status: service.StatusActive, Hydrated: true}, User: &service.User{ID: uid, Username: fmt.Sprintf("member-%d", uid), Status: service.StatusActive, Balance: 10}}
}

type fixtureKeys struct{ keys map[int64]*service.APIKey }

func (f fixtureKeys) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	k := f.keys[id]
	if k == nil {
		return nil, errors.New("revoked")
	}
	return k, nil
}

type fixtureGateway struct {
	Calls     int
	KeyID     int64
	RequestID string
	Body      []byte
	Result    GatewayResult
	Err       error
}

type blockingGateway struct {
	entered chan string
	release chan struct{}
}

func (g *blockingGateway) Call(ctx context.Context, _ *service.APIKey, _ string, id string, _ []byte) (GatewayResult, error) {
	g.entered <- id
	select {
	case <-ctx.Done():
		return GatewayResult{}, ctx.Err()
	case <-g.release:
		return GatewayResult{200, []byte(`{"choices":[{"message":{"content":"pool reply"}}]}`)}, nil
	}
}

func (f *fixtureGateway) Call(_ context.Context, k *service.APIKey, path, id string, body []byte) (GatewayResult, error) {
	f.Calls++
	f.KeyID = k.ID
	f.RequestID = id
	f.Body = append([]byte(nil), body...)
	return f.Result, f.Err
}
func goodGateway() *fixtureGateway {
	return &fixtureGateway{Result: GatewayResult{200, []byte(`{"choices":[{"message":{"content":"群内回复 @同名助手"}}]}`)}}
}

func TestInternalGatewayUsesFreshFullRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	count := 0
	r.Use(func(c *gin.Context) {
		count++
		require.Equal(t, "Bearer fixture-2", c.GetHeader("Authorization"))
		require.Equal(t, "collab:run", c.GetHeader("X-Client-Request-ID"))
		require.Empty(t, c.GetHeader("Cookie"))
		c.Next()
	})
	r.POST("/v1/chat/completions", func(c *gin.Context) { c.JSON(403, gin.H{"error": gin.H{"code": "INSUFFICIENT_BALANCE"}}) })
	result, err := (InternalGateway{r}).Call(context.Background(), testKey(1, 2), "/v1/chat/completions", "collab:run", []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, 403, result.Status)
	require.Equal(t, 1, count)
	require.Contains(t, string(result.Body), "INSUFFICIENT_BALANCE")
	_, err = (InternalGateway{r}).Call(context.Background(), testKey(1, 2), "https://evil.invalid", "collab:run", nil)
	require.Error(t, err)
}
func TestDisabledAndInvalidRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	authCalls := 0
	Register(r, &Handler{Enabled: false}, func(c *gin.Context) { authCalls++; c.Next() })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/desktop/collaboration/rooms", nil))
	require.Equal(t, 503, w.Code)
	require.Zero(t, authCalls)
	r = gin.New()
	Register(r, &Handler{Enabled: true}, func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), testKey(1, 2)); c.Next() })
	for _, body := range []string{`{"name":"x","payerId":"9"}`, `{} {}`, `{"name":"x","clientRequestId":"bad"}`} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/desktop/collaboration/rooms", strings.NewReader(body)))
		require.Equal(t, 400, w.Code, w.Body.String())
	}
}

func TestCollaborationTextModelCatalog(t *testing.T) {
	for _, model := range []string{"deepseek-chat", "gpt-6.1-sol", "claude-sonnet-4-6", "grok-4.5", "gemini-3-pro", "kimi-k2.6"} {
		require.True(t, textModel(model), model)
	}
	for _, model := range []string{"gpt-image-2", "grok-imagine-video", "gemini-3-pro-image", "gpt-4o-audio-preview", "gpt-4o-transcribe", "text-embedding-3-large", "unknown-family"} {
		require.False(t, textModel(model), model)
	}
	gateway := &fixtureGateway{Result: GatewayResult{200, []byte(`{"data":[{"id":"deepseek-chat"},{"id":"gpt-image-2"},{"id":"gpt-4o-audio-preview"}]}`)}}
	result := Status(context.Background(), testKey(1, 2), gateway)
	require.Len(t, result["models"], 1)
}

// Never accepts an environment DSN. The opt-in cluster is disposable, loopback
// only and fixed to a different port from any application or earlier fixture.
func TestCollaborationPostgres(t *testing.T) {
	if os.Getenv("WINDHUB_COLLABORATION_PG_TEST") != "1" {
		t.Skip("requires isolated loopback PostgreSQL on 55479")
	}
	const base = "host=127.0.0.1 port=55479 user=collaboration_test sslmode=disable connect_timeout=5 dbname="
	admin, err := sql.Open("postgres", base+"postgres")
	require.NoError(t, err)
	name := fmt.Sprintf("collaboration_test_%d", time.Now().UnixNano())
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
	ctx := context.Background()
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	t.Run("additive migration rolls back atomically", func(t *testing.T) {
		migration, e := migrations.FS.ReadFile("246_desktop_collaboration.sql")
		require.NoError(t, e)
		tx, e := db.BeginTx(ctx, nil)
		require.NoError(t, e)
		defer tx.Rollback()
		_, e = tx.ExecContext(ctx, `CREATE SCHEMA collaboration_rollback_fixture; SET LOCAL search_path=collaboration_rollback_fixture; CREATE TABLE users(id bigint PRIMARY KEY); CREATE TABLE api_keys(id bigint PRIMARY KEY);`)
		require.NoError(t, e)
		_, e = tx.ExecContext(ctx, string(migration)+"\nSELECT 1/0;")
		require.ErrorContains(t, e, "division by zero")
		require.NoError(t, tx.Rollback())
		var exists bool
		require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='collaboration_rollback_fixture')`).Scan(&exists))
		require.False(t, exists)
		// Rerunning the real migration runner leaves its checksum and schema stable.
		require.NoError(t, repository.ApplyMigrations(ctx, db))
	})
	keys := fixtureKeys{map[int64]*service.APIKey{}}
	for i := int64(1); i <= 3; i++ {
		var uid, kid int64
		require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,username,balance) VALUES($1,'fixture',$2,10) RETURNING id`, fmt.Sprintf("collab-%d@example.invalid", i), fmt.Sprintf("member-%d", i)).Scan(&uid))
		require.NoError(t, db.QueryRow(`INSERT INTO api_keys(user_id,key,name) VALUES($1,$2,'fixture') RETURNING id`, uid, fmt.Sprintf("collab-key-%d", i)).Scan(&kid))
		keys.keys[kid] = testKey(uid, kid)
	}
	owner := keys.keys[1]
	member := keys.keys[2]
	outsider := keys.keys[3]
	s := NewStore(db)
	write := func(k *service.APIKey, op, room, id string, in Input) any {
		t.Helper()
		if in.ClientRequestID == "" {
			in.ClientRequestID = uuid.NewString()
		}
		v, e := s.Write(ctx, k, op, room, id, in)
		require.NoError(t, e)
		return v
	}
	room := write(owner, "create", "", "", Input{Name: "Shared", ProjectName: "Project"}).(*Room)
	inviteRequest := uuid.NewString()
	invite := write(owner, "invite", room.ID, "", Input{ClientRequestID: inviteRequest}).(map[string]any)
	replay := write(owner, "invite", room.ID, "", Input{ClientRequestID: inviteRequest}).(map[string]any)
	require.Equal(t, "", replay["token"])
	require.Equal(t, true, replay["tokenAlreadyIssued"])
	var stored string
	require.NoError(t, db.QueryRow(`SELECT result::text FROM desktop_collaboration_requests WHERE user_id=$1 AND request_id=$2`, owner.UserID, inviteRequest).Scan(&stored))
	require.NotContains(t, stored, invite["token"])
	write(member, "join", "", "", Input{Token: invite["token"].(string)})
	for _, op := range []string{"room", "messages", "runs", "tasks"} {
		_, e := s.Read(ctx, outsider.UserID, op, room.ID, 0, 100)
		require.ErrorIs(t, e, denied)
	}
	_, err = s.Write(ctx, member, "addAgent", room.ID, "", Input{ClientRequestID: uuid.NewString(), Name: "Denied", Instructions: "x", Model: "deepseek-chat"})
	require.ErrorIs(t, err, denied)
	agent := write(owner, "addAgent", room.ID, "", Input{Name: "同名助手", Instructions: "Summarize shared discussion", Model: "deepseek-chat"}).(Agent)
	otherAgent := write(owner, "addAgent", room.ID, "", Input{Name: "同名助手", Instructions: "Analyze shared discussion", Model: "deepseek-chat"}).(Agent)
	t.Run("human messages and retries", func(t *testing.T) {
		req := Input{ClientRequestID: uuid.NewString(), Text: "@同名助手 plain text does not invoke AI"}
		first := write(member, "message", room.ID, "", req).(map[string]any)
		require.Empty(t, first["runs"])
		write(member, "message", room.ID, "", req)
		req.Text = "different"
		_, e := s.Write(ctx, member, "message", room.ID, "", req)
		require.ErrorIs(t, e, conflict)
		data, e := s.Read(ctx, member.UserID, "messages", room.ID, 0, 1)
		require.NoError(t, e)
		require.Len(t, data.(map[string]any)["items"], 1)
	})
	t.Run("structured mentions requester key durable completion", func(t *testing.T) {
		req := Input{ClientRequestID: uuid.NewString(), Text: "Please answer", MentionIDs: []string{agent.ID, otherAgent.ID, agent.ID}}
		first := write(member, "message", room.ID, "", req).(map[string]any)
		require.Len(t, first["runs"], 2)
		write(member, "message", room.ID, "", req)
		write(owner, "message", room.ID, "", Input{Text: "FUTURE_SECRET_MUST_NOT_ENTER_CONTEXT"})
		gateway := goodGateway()
		worker := &Worker{s, keys, gateway}
		require.NoError(t, worker.Step(ctx))
		require.NoError(t, worker.Step(ctx))
		require.Equal(t, 2, gateway.Calls)
		require.Equal(t, member.ID, gateway.KeyID)
		require.True(t, strings.HasPrefix(gateway.RequestID, "collab:"))
		require.NotContains(t, string(gateway.Body), "FUTURE_SECRET")
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM desktop_collaboration_runs WHERE room_id=$1`, room.ID).Scan(&count))
		require.Equal(t, 2, count)
		require.NoError(t, worker.Step(ctx))
		require.Equal(t, 2, gateway.Calls, "AI mentions cannot recursively schedule more runs")
		messages, e := s.Read(ctx, member.UserID, "messages", room.ID, 0, 100)
		require.NoError(t, e)
		var answers int
		for _, m := range messages.(map[string]any)["items"].([]Message) {
			if m.Kind == "agent" {
				answers++
				require.NotEmpty(t, m.RunID)
				require.NotEmpty(t, m.ReplyToID)
			}
		}
		require.Equal(t, 2, answers)
	})
	t.Run("unknown is never retried", func(t *testing.T) {
		write(member, "message", room.ID, "", Input{Text: "unknown", MentionIDs: []string{agent.ID}})
		gateway := goodGateway()
		gateway.Err = context.DeadlineExceeded
		worker := &Worker{s, keys, gateway}
		require.NoError(t, worker.Step(ctx))
		require.NoError(t, worker.Step(ctx))
		require.Equal(t, 1, gateway.Calls)
		var status string
		require.NoError(t, db.QueryRow(`SELECT status FROM desktop_collaboration_runs WHERE room_id=$1 ORDER BY created_at DESC LIMIT 1`, room.ID).Scan(&status))
		require.Equal(t, "outcome_unknown", status)
		write(member, "message", room.ID, "", Input{Text: "crash", MentionIDs: []string{agent.ID}})
		_, err := worker.claim(ctx)
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE desktop_collaboration_runs SET started_at=now()-interval '7 minutes' WHERE status='running'`)
		require.NoError(t, err)
		require.NoError(t, worker.Step(ctx))
		require.Equal(t, 1, gateway.Calls)
	})
	t.Run("truncated reply remains visible and incomplete", func(t *testing.T) {
		write(member, "message", room.ID, "", Input{Text: "long reply", MentionIDs: []string{agent.ID}})
		gateway := goodGateway()
		gateway.Result.Body = []byte(`{"choices":[{"finish_reason":"length","message":{"content":"PARTIAL_REPLY"}}]}`)
		require.NoError(t, (&Worker{s, keys, gateway}).Step(ctx))
		var status, code, text string
		require.NoError(t, db.QueryRow(`SELECT r.status,r.error_code,m.text FROM desktop_collaboration_runs r JOIN desktop_collaboration_messages m ON m.run_id=r.id WHERE r.room_id=$1 ORDER BY r.created_at DESC LIMIT 1`, room.ID).Scan(&status, &code, &text))
		require.Equal(t, "failed", status)
		require.Equal(t, "RESPONSE_TRUNCATED", code)
		require.Contains(t, text, "PARTIAL_REPLY")
		require.Contains(t, text, "内容未完成")
	})
	t.Run("tasks compare and swap", func(t *testing.T) {
		title := "Task"
		assignee := strconv.FormatInt(member.UserID, 10)
		task := write(owner, "task", room.ID, "", Input{Title: &title, AssigneeID: &assignee}).(Task)
		status := "doing"
		updated := write(member, "updateTask", room.ID, task.ID, Input{Status: &status, Revision: 1}).(Task)
		require.EqualValues(t, 2, updated.Revision)
		_, e := s.Write(ctx, owner, "updateTask", room.ID, task.ID, Input{ClientRequestID: uuid.NewString(), Status: &status, Revision: 1})
		require.ErrorIs(t, e, conflict)
	})
	t.Run("room files download and explicit bounded context", func(t *testing.T) {
		requestID := uuid.NewString()
		value, e := s.Upload(ctx, member, room.ID, requestID, "notes.md", []byte("SHARED_FILE_MARKER"))
		require.NoError(t, e)
		asset := value.(Asset)
		value, e = s.Upload(ctx, member, room.ID, requestID, "notes.md", []byte("SHARED_FILE_MARKER"))
		require.NoError(t, e)
		require.Equal(t, asset.ID, value.(Asset).ID)
		_, e = s.Upload(ctx, member, room.ID, requestID, "notes.md", []byte("changed"))
		require.ErrorIs(t, e, conflict)
		_, e = s.Assets(ctx, outsider.UserID, room.ID, asset.ID)
		require.ErrorIs(t, e, denied)
		otherRoom := write(owner, "create", "", "", Input{Name: "private room"}).(*Room)
		_, e = s.Assets(ctx, owner.UserID, otherRoom.ID, asset.ID)
		require.ErrorIs(t, e, denied)
		_, e = s.Write(ctx, owner, "message", otherRoom.ID, "", Input{ClientRequestID: uuid.NewString(), Text: "steal attachment", AttachmentIDs: []string{asset.ID}})
		require.ErrorIs(t, e, denied)
		message := write(member, "message", room.ID, "", Input{Text: "Read this shared file", MentionIDs: []string{agent.ID}, AttachmentIDs: []string{asset.ID}}).(map[string]any)["message"].(Message)
		require.Len(t, message.Attachments, 1)
		gateway := goodGateway()
		require.NoError(t, (&Worker{s, keys, gateway}).Step(ctx))
		require.Contains(t, string(gateway.Body), "SHARED_FILE_MARKER")
		r := gin.New()
		Register(r, &Handler{Store: s, Gateway: goodGateway(), Enabled: true}, func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), member); c.Next() })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/desktop/collaboration/rooms/"+room.ID+"/assets/"+asset.ID+"/content", nil))
		require.Equal(t, 200, w.Code)
		require.Equal(t, "SHARED_FILE_MARKER", w.Body.String())
		require.Contains(t, w.Header().Get("Content-Disposition"), "attachment;")
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var upload bytes.Buffer
		form := multipart.NewWriter(&upload)
		require.NoError(t, form.WriteField("clientRequestId", uuid.NewString()))
		file, e := form.CreateFormFile("file", "upload.txt")
		require.NoError(t, e)
		_, e = file.Write([]byte("via http"))
		require.NoError(t, e)
		require.NoError(t, form.Close())
		req := httptest.NewRequest("POST", "/v1/desktop/collaboration/rooms/"+room.ID+"/assets", &upload)
		req.Header.Set("Content-Type", form.FormDataContentType())
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		_, e = s.Upload(ctx, member, room.ID, uuid.NewString(), "huge.bin", make([]byte, maxAssetBytes+1))
		require.Error(t, e)
		_, note, _ := extractSharedText("image.png", []byte("text disguised as image"), 8000)
		require.Contains(t, note, "未解析")
		_, note, _ = extractSharedText("bad.txt", []byte{0xff}, 8000)
		require.Contains(t, note, "未解析")
		text, note, used := extractSharedText("long.txt", []byte(strings.Repeat("字", 9000)), 8000)
		require.Len(t, []rune(text), 8000)
		require.Equal(t, 8000, used)
		require.Contains(t, note, "部分内容")
		_, e = s.Write(ctx, member, "message", room.ID, "", Input{ClientRequestID: uuid.NewString(), Text: "too many", AttachmentIDs: []string{asset.ID, asset.ID, asset.ID, asset.ID, asset.ID, asset.ID, asset.ID, asset.ID, asset.ID}})
		require.Error(t, e)
		large, e := s.Upload(ctx, member, room.ID, uuid.NewString(), "large.txt", []byte(strings.Repeat("x", 9000)))
		require.NoError(t, e)
		later, e := s.Upload(ctx, member, room.ID, uuid.NewString(), "later.txt", []byte("AFTER_BUDGET"))
		require.NoError(t, e)
		withFiles := write(member, "message", room.ID, "", Input{Text: "bounded attachments", MentionIDs: []string{agent.ID}, AttachmentIDs: []string{large.(Asset).ID, later.(Asset).ID}}).(map[string]any)["message"].(Message)
		require.Contains(t, withFiles.Attachments[0].ContextNote, "部分内容")
		require.Contains(t, withFiles.Attachments[1].ContextNote, "未读取")
		reloaded, e := s.Read(ctx, owner.UserID, "messages", room.ID, withFiles.Seq-1, 1)
		require.NoError(t, e)
		reloadedMessage := reloaded.(map[string]any)["items"].([]Message)[0]
		require.Equal(t, withFiles.Attachments, reloadedMessage.Attachments, "another member's refreshed view must retain the same extraction notes")
		gateway = goodGateway()
		require.NoError(t, (&Worker{s, keys, gateway}).Step(ctx))
		require.Contains(t, string(gateway.Body), "later.txt")
		require.Contains(t, string(gateway.Body), "未读取")
		require.NotContains(t, string(gateway.Body), "AFTER_BUDGET")
	})
	t.Run("concurrent idempotency", func(t *testing.T) {
		req := Input{ClientRequestID: uuid.NewString(), Text: "once", MentionIDs: []string{agent.ID}}
		var wg sync.WaitGroup
		failures := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, e := s.Write(ctx, member, "message", room.ID, "", req); failures <- e }()
		}
		wg.Wait()
		close(failures)
		for e := range failures {
			require.NoError(t, e)
		}
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM desktop_collaboration_messages WHERE room_id=$1 AND text='once'`, room.ID).Scan(&count))
		require.Equal(t, 1, count)
		// Concurrent workers can only execute this queued row once.
		gateway := goodGateway()
		worker := &Worker{s, keys, gateway}
		job1, e := worker.claim(ctx)
		require.NoError(t, e)
		job2, e := worker.claim(ctx)
		require.NoError(t, e)
		require.NotNil(t, job1)
		require.Nil(t, job2)
		require.NoError(t, worker.finish(job1, "failed", "FIXTURE", "fixture", ""))
	})
	t.Run("encoded message pages stay below desktop response bound", func(t *testing.T) {
		largeRoom := write(owner, "create", "", "", Input{Name: "long history"}).(*Room)
		for i := 0; i < 40; i++ {
			write(owner, "message", largeRoom.ID, "", Input{Text: strings.Repeat("<界", 6000)})
		}
		cursor := int64(0)
		seen := map[string]bool{}
		pages := 0
		for {
			value, e := s.Read(ctx, owner.UserID, "messages", largeRoom.ID, cursor, 100)
			require.NoError(t, e)
			result := value.(map[string]any)
			encoded, e := json.Marshal(map[string]any{"code": 0, "message": "success", "data": result})
			require.NoError(t, e)
			require.Less(t, len(encoded), 2<<20)
			items := result["items"].([]Message)
			require.NotEmpty(t, items)
			for _, m := range items {
				require.False(t, seen[m.ID])
				seen[m.ID] = true
				require.Equal(t, cursor+1, m.Seq)
				cursor = m.Seq
			}
			require.Equal(t, cursor, result["cursor"])
			pages++
			if !result["hasMore"].(bool) {
				break
			}
			require.Less(t, pages, 40)
		}
		require.Len(t, seen, 40)
		require.Greater(t, pages, 1)
	})
	t.Run("bounded workers avoid slow room head of line", func(t *testing.T) {
		write(owner, "message", room.ID, "", Input{Text: "parallel one", MentionIDs: []string{agent.ID}})
		write(owner, "message", room.ID, "", Input{Text: "parallel two", MentionIDs: []string{agent.ID}})
		gateway := &blockingGateway{entered: make(chan string, 4), release: make(chan struct{})}
		worker := &Worker{s, keys, gateway}
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		worker.Start(runCtx)
		for i := 0; i < 2; i++ {
			select {
			case <-gateway.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("slow first run blocked second worker")
			}
		}
		close(gateway.release)
		require.Eventually(t, func() bool {
			var n int
			err := db.QueryRow(`SELECT count(*) FROM desktop_collaboration_runs WHERE room_id=$1 AND status IN ('running','queued')`, room.ID).Scan(&n)
			return err == nil && n == 0
		}, 5*time.Second, 20*time.Millisecond)
	})
	t.Run("removing agent during run suppresses reply", func(t *testing.T) {
		removable := write(owner, "addAgent", room.ID, "", Input{Name: "temporary", Instructions: "respond", Model: "deepseek-chat"}).(Agent)
		write(owner, "message", room.ID, "", Input{Text: "remove mid run", MentionIDs: []string{removable.ID}})
		worker := &Worker{s, keys, goodGateway()}
		job, e := worker.claim(ctx)
		require.NoError(t, e)
		require.NotNil(t, job)
		write(owner, "removeAgent", room.ID, removable.ID, Input{})
		require.NoError(t, worker.finish(job, "completed", "", "", "must not be published"))
		var n int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM desktop_collaboration_messages WHERE run_id=$1`, job.ID).Scan(&n))
		require.Zero(t, n)
	})
	t.Run("member revocation and replay authorization", func(t *testing.T) {
		req := Input{ClientRequestID: uuid.NewString(), Text: "revoked", MentionIDs: []string{agent.ID}}
		write(member, "message", room.ID, "", req)
		write(owner, "removeMember", room.ID, strconv.FormatInt(member.UserID, 10), Input{})
		_, e := s.Write(ctx, member, "message", room.ID, "", req)
		require.ErrorIs(t, e, denied)
		_, e = s.Read(ctx, member.UserID, "messages", room.ID, 0, 100)
		require.ErrorIs(t, e, denied)
		_, e = s.Write(ctx, member, "join", "", "", Input{ClientRequestID: uuid.NewString(), Token: invite["token"].(string)})
		require.Error(t, e)
		gateway := goodGateway()
		require.NoError(t, (&Worker{s, keys, gateway}).Step(ctx))
		require.Zero(t, gateway.Calls)
	})
	t.Run("HTTP envelope cross user and CAS", func(t *testing.T) {
		r := gin.New()
		Register(r, &Handler{Store: s, Gateway: goodGateway(), Enabled: true}, func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), owner); c.Next() })
		body, _ := json.Marshal(Input{ClientRequestID: uuid.NewString(), Name: "HTTP"})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/v1/desktop/collaboration/rooms", bytes.NewReader(body)))
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"code":0`)
		w = httptest.NewRecorder()
		req := httptest.NewRequest("DELETE", "/v1/desktop/collaboration/rooms/"+room.ID+"/agents/"+otherAgent.ID, nil)
		req.Header.Set("Idempotency-Key", uuid.NewString())
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
		require.Contains(t, w.Body.String(), `"data":{}`)
	})
}
