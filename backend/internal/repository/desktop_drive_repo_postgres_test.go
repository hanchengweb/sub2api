package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Explicitly opt in to a dedicated loopback cluster. Application DSNs are never
// accepted; each run creates and drops only its own randomly named database.
func TestDesktopDriveRepositoryPostgres(t *testing.T) {
	if os.Getenv("WINDHUB_DRIVE_PG_TEST") != "1" {
		t.Skip("requires isolated PostgreSQL on 127.0.0.1:55473, user drive_test")
	}
	const baseDSN = "host=127.0.0.1 port=55473 user=drive_test sslmode=disable connect_timeout=5 dbname="
	admin, err := sql.Open("postgres", baseDSN+"postgres")
	require.NoError(t, err)
	name := fmt.Sprintf("desktop_drive_test_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	db, err := sql.Open("postgres", baseDSN+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_, err := admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		require.NoError(t, err)
		require.NoError(t, admin.Close())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const migrationName = "245_desktop_drive.sql"
	previous := fstest.MapFS{}
	entries, err := migrations.FS.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") && entry.Name() < migrationName {
			data, err := migrations.FS.ReadFile(entry.Name())
			require.NoError(t, err)
			previous[entry.Name()] = &fstest.MapFile{Data: data}
		}
	}
	require.NoError(t, applyMigrationsFS(ctx, db, previous))
	var owner, other int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance) VALUES ('drive-owner@example.invalid','fixture-hash',0) RETURNING id`).Scan(&owner))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,balance) VALUES ('drive-other@example.invalid','fixture-hash',9.75) RETURNING id`).Scan(&other))
	snapshot := func() string {
		var result string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT md5(string_agg(row_to_json(u)::text, '' ORDER BY id)) FROM (SELECT id,email,password_hash,balance FROM users) u`).Scan(&result))
		return result
	}
	before := snapshot()
	migration, err := migrations.FS.ReadFile(migrationName)
	require.NoError(t, err)
	broken := fstest.MapFS{migrationName: &fstest.MapFile{Data: []byte(string(migration) + "\nSELECT 1/0;")}}
	require.ErrorContains(t, applyMigrationsFS(ctx, db, broken), "division by zero")
	var absent bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('desktop_drives') IS NULL`).Scan(&absent))
	require.True(t, absent, "failed migration must roll back all drive DDL")
	require.Equal(t, before, snapshot())
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db), "startup replay must not reapply schema")
	require.Equal(t, before, snapshot(), "existing account credentials and balances are unchanged")
	var receipts int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE filename=$1`, migrationName).Scan(&receipts))
	require.Equal(t, 1, receipts)

	repo := NewDesktopDriveRepository(db)
	const drive = "11111111-1111-4111-8111-111111111111"
	const secondDrive = "11111111-1111-4111-8111-111111111112"
	const otherDrive = "11111111-1111-4111-8111-111111111113"
	const folder = "22222222-2222-4222-8222-222222222221"
	const nestedFolder = "22222222-2222-4222-8222-222222222222"
	const nestedFile = "22222222-2222-4222-8222-222222222223"
	const rootFile = "22222222-2222-4222-8222-222222222224"
	const requestID = "33333333-3333-4333-8333-333333333333"
	fingerprint := strings.Repeat("a", 64)
	file := func(id, parent, name string) service.DesktopDriveItem {
		return service.DesktopDriveItem{ID: id, ParentID: parent, Name: name, ItemType: "file", FileName: name, MediaType: "text/plain", SizeBytes: 6, SHA256: fingerprint}
	}
	request := service.DesktopDriveRequest{Fingerprint: fingerprint, Result: json.RawMessage(`{"id":"` + drive + `"}`)}
	require.NoError(t, repo.WithUser(ctx, owner, func(store service.DesktopDriveStore) error {
		require.NoError(t, store.CreateDrive(ctx, service.DesktopDrive{ID: drive, Name: "个人资料"}))
		require.NoError(t, store.CreateDrive(ctx, service.DesktopDrive{ID: secondDrive, Name: "空云盘"}))
		require.NoError(t, store.CreateItem(ctx, drive, service.DesktopDriveItem{ID: folder, Name: "课程", ItemType: "folder"}))
		require.NoError(t, store.CreateItem(ctx, drive, service.DesktopDriveItem{ID: nestedFolder, ParentID: folder, Name: "第一课", ItemType: "folder"}))
		require.NoError(t, store.CreateItem(ctx, drive, file(nestedFile, nestedFolder, "资料.txt")))
		require.NoError(t, store.CreateItem(ctx, drive, file(rootFile, "", "根目录.txt")))
		exists, err := store.FileExists(ctx, rootFile)
		require.NoError(t, err)
		require.True(t, exists, "a live file is retained regardless of its drive")
		exists, err = store.FileExists(ctx, folder)
		require.NoError(t, err)
		require.False(t, exists, "folder metadata must not preserve orphaned raw bytes")
		return store.SaveRequest(ctx, requestID, request)
	}))
	require.NoError(t, repo.WithUser(ctx, other, func(store service.DesktopDriveStore) error {
		require.NoError(t, store.CreateDrive(ctx, service.DesktopDrive{ID: otherDrive, Name: "Other"}))
		invisible, err := store.Drive(ctx, drive)
		require.NoError(t, err)
		require.Nil(t, invisible)
		item, err := store.Item(ctx, drive, nestedFile)
		require.NoError(t, err)
		require.Nil(t, item)
		exists, err := store.FileExists(ctx, nestedFile)
		require.NoError(t, err)
		require.False(t, exists, "another user's metadata never protects a raw object")
		items, total, err := store.Items(ctx, drive, "", 0, 50)
		require.NoError(t, err)
		require.Empty(t, items)
		require.Zero(t, total)
		require.NoError(t, store.Delete(ctx, drive, ""))
		garbage, err := store.Garbage(ctx)
		require.NoError(t, err)
		require.Empty(t, garbage)
		replay, err := store.Request(ctx, requestID)
		require.NoError(t, err)
		require.Nil(t, replay)
		return nil
	}))
	require.ErrorIs(t, repo.WithUser(ctx, other, func(store service.DesktopDriveStore) error {
		return store.Rename(ctx, drive, "", "stolen")
	}), sql.ErrNoRows)
	require.ErrorIs(t, repo.WithUser(ctx, owner, func(store service.DesktopDriveStore) error {
		return store.CreateItem(ctx, secondDrive, file("44444444-4444-4444-8444-444444444444", folder, "bad.txt"))
	}), sql.ErrNoRows, "parent must belong to the same drive")
	require.ErrorIs(t, repo.WithUser(ctx, other, func(store service.DesktopDriveStore) error {
		return store.CreateItem(ctx, drive, file("44444444-4444-4444-8444-444444444445", "", "bad.txt"))
	}), sql.ErrNoRows, "inserts must not attach to another owner's drive")
	_, err = db.ExecContext(ctx, `INSERT INTO desktop_drive_items(id,user_id,drive_id,name,item_type) VALUES('44444444-4444-4444-8444-444444444446',$1,$2,'bad','folder')`, other, drive)
	require.ErrorContains(t, err, "foreign key", "schema must also reject an owner/drive mismatch")
	_, err = db.ExecContext(ctx, `INSERT INTO desktop_drive_items(id,user_id,drive_id,parent_id,name,item_type) VALUES('44444444-4444-4444-8444-444444444447',$1,$2,$3,'bad','folder')`, owner, secondDrive, folder)
	require.ErrorContains(t, err, "foreign key", "schema must also reject a cross-drive parent")

	require.NoError(t, repo.WithUser(ctx, owner, func(store service.DesktopDriveStore) error {
		bytes, objects, err := store.Usage(ctx)
		require.NoError(t, err)
		require.EqualValues(t, 12, bytes)
		require.EqualValues(t, 6, objects, "empty drives count toward object limits")
		items, total, err := store.Items(ctx, drive, "", 0, 50)
		require.NoError(t, err)
		require.EqualValues(t, 2, total)
		require.Equal(t, folder, items[0].ID, "folders sort ahead of files")
		require.NoError(t, store.Rename(ctx, drive, rootFile, "重命名.txt"))
		return store.Delete(ctx, drive, folder)
	}))
	require.NoError(t, repo.WithUser(ctx, owner, func(store service.DesktopDriveStore) error {
		for _, id := range []string{folder, nestedFolder, nestedFile} {
			item, err := store.Item(ctx, drive, id)
			require.NoError(t, err)
			require.Nil(t, item, "recursive deletion removes the entire subtree")
		}
		item, err := store.Item(ctx, drive, rootFile)
		require.NoError(t, err)
		require.Equal(t, "重命名.txt", item.FileName)
		exists, err := store.FileExists(ctx, rootFile)
		require.NoError(t, err)
		require.True(t, exists)
		exists, err = store.FileExists(ctx, nestedFile)
		require.NoError(t, err)
		require.False(t, exists)
		garbage, err := store.Garbage(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{nestedFile}, garbage)
		return store.Delete(ctx, drive, "")
	}))
	_, err = db.ExecContext(ctx, `UPDATE desktop_drive_requests SET created_at=now()-interval '5 years' WHERE user_id=$1 AND request_id=$2`, owner, requestID)
	require.NoError(t, err)
	require.NoError(t, repo.WithUser(ctx, owner, func(store service.DesktopDriveStore) error {
		garbage, err := store.Garbage(ctx)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{nestedFile, rootFile}, garbage)
		exists, err := store.FileExists(ctx, rootFile)
		require.NoError(t, err)
		require.False(t, exists, "drive deletion makes the raw file eligible for cleanup")
		replay, err := store.Request(ctx, requestID)
		require.NoError(t, err)
		require.NotNil(t, replay, "deletions and age must not permit re-execution")
		require.JSONEq(t, string(request.Result), string(replay.Result))
		return nil
	}))
	require.Error(t, repo.WithUser(ctx, owner, func(store service.DesktopDriveStore) error {
		return store.SaveRequest(ctx, requestID, request)
	}), "a duplicate idempotency key cannot overwrite its receipt")
}
