package routes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// An explicit opt-in fixed loopback cluster prevents an environment or saved
// application DSN from ever pointing this destructive fixture at real data.
func TestDesktopDriveHTTPPostgres(t *testing.T) {
	if os.Getenv("WINDHUB_DRIVE_PG_TEST") != "1" {
		t.Skip("requires disposable loopback PostgreSQL cluster")
	}
	const dsn = "host=127.0.0.1 port=55473 user=drive_test sslmode=disable connect_timeout=5 dbname="
	admin, err := sql.Open("postgres", dsn+"postgres")
	require.NoError(t, err)
	name := fmt.Sprintf("desktop_drive_http_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	ctx := context.Background()
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	var owner, other int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES ('owner@example.invalid','fixture',0) RETURNING id`).Scan(&owner))
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,balance) VALUES ('other@example.invalid','fixture',0) RETURNING id`).Scan(&other))
	root := t.TempDir()
	driveService := service.NewDesktopDriveService(repository.NewDesktopDriveRepository(db), root)
	old := service.DefaultIdempotencyCoordinator()
	cfg := service.DefaultIdempotencyConfig()
	cfg.ObserveOnly = false
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repository.NewIdempotencyRepository(nil, db), cfg))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(old) })
	gin.SetMode(gin.TestMode)
	router := func(user, keyID int64) *gin.Engine {
		groupID := int64(4)
		key := &service.APIKey{ID: keyID, UserID: user, Key: "test-personal", Status: service.StatusActive, GroupID: &groupID, Group: &service.Group{ID: 4, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard}, User: &service.User{ID: user, Username: "个人账号", Status: service.StatusActive, Balance: 0, Role: service.RoleUser}}
		authCfg := &config.Config{RunMode: config.RunModeStandard}
		keySvc := service.NewAPIKeyService(&keyBillingRouteAPIKeyRepo{apiKey: key}, nil, nil, nil, &keyBillingRouteRateRepo{}, nil, authCfg)
		r := gin.New()
		RegisterDesktopDriveRoutes(r, handler.NewDesktopDriveHandler(driveService), middleware.NewAPIKeyAuthMiddleware(keySvc, nil, authCfg))
		return r
	}
	r := router(owner, 11)
	request := func(engine *gin.Engine, method, path, key string, body []byte, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-personal")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}
	jsonWrite := func(method, path, key string, body any) *httptest.ResponseRecorder {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		return request(r, method, path, key, data, "application/json")
	}
	data := func(w *httptest.ResponseRecorder) map[string]any {
		require.Equal(t, 200, w.Code, w.Body.String())
		var envelope struct{ Data map[string]any }
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		return envelope.Data
	}
	const base = "/v1/desktop/drive"
	initial := data(request(r, "GET", base, "", nil, ""))
	require.Empty(t, initial["resourceFolders"])
	require.Equal(t, fmt.Sprintf("WH-%d", owner), initial["account"].(map[string]any)["reference"])
	createKey := uuid.NewString()
	created := data(jsonWrite("POST", base, createKey, map[string]any{"name": "个人云盘", "clientRequestId": createKey}))
	driveID := created["id"].(string)
	drivePath := base + "/" + driveID
	folderKey := uuid.NewString()
	folder := data(jsonWrite("POST", drivePath+"/folders", folderKey, map[string]any{"name": "课程", "clientRequestId": folderKey}))
	parent := folder["id"].(string)
	upload := func(key, text string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("parent_id", parent))
		f, err := writer.CreateFormFile("files", "资料.txt")
		require.NoError(t, err)
		_, err = f.Write([]byte(text))
		require.NoError(t, err)
		f, err = writer.CreateFormFile("files", "原件.pdf")
		require.NoError(t, err)
		_, err = f.Write([]byte("%PDF-1.7\nfixture bytes"))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		return request(r, "POST", drivePath+"/upload", key, body.Bytes(), writer.FormDataContentType())
	}
	uploadKey := uuid.NewString()
	uploaded := data(upload(uploadKey, "资料正文"))
	// The success may be the upload that fills the account. Replaying it
	// cannot require another temporary copy or any remaining storage quota.
	_, err = db.Exec(`UPDATE desktop_drive_items SET size_bytes=$1 WHERE user_id=$2 AND drive_id=$3 AND item_type='file' AND file_name='资料.txt'`, service.DesktopDriveLimitBytes, owner, driveID)
	require.NoError(t, err)
	replayed := data(upload(uploadKey, "资料正文"))
	require.Equal(t, uploaded, replayed)
	_, err = db.Exec(`UPDATE desktop_drive_items SET size_bytes=$1 WHERE user_id=$2 AND drive_id=$3 AND item_type='file' AND file_name='资料.txt'`, int64(len("资料正文")), owner, driveID)
	require.NoError(t, err)
	conflict := upload(uploadKey, "变更后的正文")
	require.Equal(t, 409, conflict.Code, conflict.Body.String())
	items := uploaded["items"].([]any)
	textID := items[0].(map[string]any)["id"].(string)
	pdfID := items[1].(map[string]any)["id"].(string)
	content := data(request(r, "GET", drivePath+"/items/"+textID, "", nil, ""))
	require.Equal(t, "资料正文", content["content"])
	pdf := data(request(r, "GET", drivePath+"/items/"+pdfID, "", nil, ""))
	require.Equal(t, "", pdf["content"])
	require.Equal(t, true, pdf["contentUnavailable"])
	original := request(router(owner, 999), "GET", drivePath+"/items/"+textID+"/original", "", nil, "")
	require.Equal(t, 200, original.Code)
	require.Equal(t, "资料正文", original.Body.String())
	require.Contains(t, original.Header().Get("Content-Disposition"), "attachment")
	for _, path := range []string{drivePath, drivePath + "/items/" + textID, drivePath + "/items/" + textID + "/original"} {
		w := request(router(other, 44), "GET", path, "", nil, "")
		require.Equal(t, 404, w.Code, w.Body.String())
	}
	listed := data(request(r, "GET", drivePath+"?parent_id="+parent, "", nil, ""))
	require.EqualValues(t, 2, listed["total"])
	renamed := data(jsonWrite("PATCH", drivePath+"/items/"+textID, uuid.NewString(), map[string]any{"name": "新文件名.txt"}))
	require.Equal(t, "新文件名.txt", renamed["name"])
	deleteKey := uuid.NewString()
	data(jsonWrite("DELETE", drivePath+"/items/"+parent, deleteKey, nil))
	data(jsonWrite("DELETE", drivePath+"/items/"+parent, deleteKey, nil))
	require.Equal(t, 404, request(r, "GET", drivePath+"/items/"+textID, "", nil, "").Code)
	// A previous upload replay after deletion returns its receipt without
	// recreating the files, including when the general 24h cache is gone.
	_, err = db.Exec(`DELETE FROM idempotency_records WHERE scope=$1`, fmt.Sprintf("desktop.drive.user:%d", owner))
	require.NoError(t, err)
	data(upload(uploadKey, "资料正文"))
	remaining := data(request(r, "GET", drivePath, "", nil, ""))
	require.EqualValues(t, 0, remaining["total"])
	entries, err := os.ReadDir(filepath.Join(root, "desktop-drive", fmt.Sprint(owner)))
	require.NoError(t, err)
	require.Empty(t, entries, "deleted files and temporary uploads must be removed")
	largeKey := uuid.NewString()
	largeUpload := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for index := range 100 {
			name := strings.Repeat("资料", 75) + fmt.Sprintf("%03d.txt", index)
			file, err := writer.CreateFormFile("files", name)
			require.NoError(t, err)
			_, err = file.Write([]byte("x"))
			require.NoError(t, err)
		}
		require.NoError(t, writer.Close())
		return request(r, "POST", drivePath+"/upload", largeKey, body.Bytes(), writer.FormDataContentType())
	}
	largeFirst := largeUpload()
	require.Greater(t, largeFirst.Body.Len(), 64<<10)
	require.Equal(t, data(largeFirst), data(largeUpload()), "large Unicode file results must replay without generic cache truncation")
	data(jsonWrite("DELETE", drivePath, uuid.NewString(), nil))
	_, err = db.Exec(`DELETE FROM idempotency_records WHERE scope=$1`, fmt.Sprintf("desktop.drive.user:%d", owner))
	require.NoError(t, err)
	data(jsonWrite("POST", base, createKey, map[string]any{"name": "个人云盘", "clientRequestId": createKey}))
	require.Empty(t, data(request(r, "GET", base, "", nil, ""))["resourceFolders"])
	var balances string
	require.NoError(t, db.QueryRow(`SELECT string_agg(balance::text,',' ORDER BY id) FROM users`).Scan(&balances))
	require.NotContains(t, balances, "-")
	for _, invalid := range []string{"", "not-a-uuid"} {
		w := jsonWrite("POST", base, invalid, map[string]any{"name": "拒绝"})
		require.Equal(t, 400, w.Code)
	}
	require.NotContains(t, strings.ToLower(original.Header().Get("Content-Disposition")), "desktop-drive")
}
