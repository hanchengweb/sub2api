package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const driveRepoTestUser int64 = 73
const driveRepoTestDrive = "11111111-1111-4111-8111-111111111111"
const driveRepoTestItem = "22222222-2222-4222-8222-222222222222"

func desktopDriveRepoTest(t *testing.T) (service.DesktopDriveRepository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, mock.ExpectationsWereMet()); _ = db.Close() })
	return NewDesktopDriveRepository(db), mock
}

func desktopDriveExpectUser(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectExec(`SELECT pg_advisory_xact_lock\(1464353874, hashint8\(\$1::bigint\)\)`).
		WithArgs(driveRepoTestUser).WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestDesktopDriveRepositoryBindsOwnerForEveryObjectRead(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	mock.ExpectQuery(`FROM desktop_drives d WHERE d.user_id = \$1 AND d.id = \$2`).
		WithArgs(driveRepoTestUser, driveRepoTestDrive).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "count", "status", "scope"}))
	mock.ExpectQuery(`FROM desktop_drive_items WHERE user_id = \$1 AND drive_id = \$2 AND id = \$3`).
		WithArgs(driveRepoTestUser, driveRepoTestDrive, driveRepoTestItem).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "type", "filename", "mime", "size", "parent", "sha"}))
	mock.ExpectCommit()
	err := repo.WithUser(context.Background(), driveRepoTestUser, func(tx service.DesktopDriveStore) error {
		drive, err := tx.Drive(context.Background(), driveRepoTestDrive)
		require.NoError(t, err)
		require.Nil(t, drive, "an object outside the bound owner is absent")
		item, err := tx.Item(context.Background(), driveRepoTestDrive, driveRepoTestItem)
		require.NoError(t, err)
		require.Nil(t, item)
		return nil
	})
	require.NoError(t, err)
}

func TestDesktopDriveRepositoryParentAndDriveGuardsPreventOrphans(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	mock.ExpectExec(`(?s)INSERT INTO desktop_drive_items.*WHERE EXISTS \(SELECT 1 FROM desktop_drives WHERE user_id = \$1 AND id = \$2\).*WHERE user_id = \$1 AND drive_id = \$2.*item_type = 'folder'`).
		WithArgs(driveRepoTestUser, driveRepoTestDrive, driveRepoTestItem, driveRepoTestItem, "Nested", "folder", "", "", int64(0), "").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	err := repo.WithUser(context.Background(), driveRepoTestUser, func(tx service.DesktopDriveStore) error {
		return tx.CreateItem(context.Background(), driveRepoTestDrive,
			service.DesktopDriveItem{ID: driveRepoTestItem, ParentID: driveRepoTestItem, Name: "Nested", ItemType: "folder"})
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestDesktopDriveRepositoryUsageCountsEmptyDrives(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	mock.ExpectQuery(`(?s)SELECT COALESCE\(sum\(size_bytes\), 0\), count\(\*\) \+ \(SELECT count\(\*\) FROM desktop_drives WHERE user_id = \$1\).*FROM desktop_drive_items WHERE user_id = \$1`).
		WithArgs(driveRepoTestUser).WillReturnRows(sqlmock.NewRows([]string{"bytes", "objects"}).AddRow(1024, 8))
	mock.ExpectCommit()
	require.NoError(t, repo.WithUser(context.Background(), driveRepoTestUser, func(tx service.DesktopDriveStore) error {
		bytes, objects, err := tx.Usage(context.Background())
		require.NoError(t, err)
		require.Equal(t, int64(1024), bytes)
		require.Equal(t, int64(8), objects)
		return nil
	}))
}

func TestDesktopDriveRepositoryRequestReplayNeverExpiresOrOverwrites(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	fingerprint := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	result := json.RawMessage(`{"id":"old-deleted-drive"}`)
	// Anchor the query: a creation-time predicate would discard old replays.
	mock.ExpectQuery(`SELECT fingerprint, result FROM desktop_drive_requests WHERE user_id = \$1 AND request_id = \$2$`).
		WithArgs(driveRepoTestUser, driveRepoTestItem).
		WillReturnRows(sqlmock.NewRows([]string{"fingerprint", "result"}).AddRow(fingerprint, []byte(result)))
	mock.ExpectExec(`INSERT INTO desktop_drive_requests \(user_id, request_id, fingerprint, result\) VALUES \(\$1, \$2, \$3, \$4::jsonb\)$`).
		WithArgs(driveRepoTestUser, driveRepoTestDrive, fingerprint, string(result)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.WithUser(context.Background(), driveRepoTestUser, func(tx service.DesktopDriveStore) error {
		request, err := tx.Request(context.Background(), driveRepoTestItem)
		require.NoError(t, err)
		require.Equal(t, fingerprint, request.Fingerprint)
		require.JSONEq(t, string(result), string(request.Result))
		return tx.SaveRequest(context.Background(), driveRepoTestDrive, service.DesktopDriveRequest{Fingerprint: fingerprint, Result: result})
	}))
}

func TestDesktopDriveRepositoryRecursiveDeletionQueuesOwnedBytesFirst(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	const tree = `(?s)WITH RECURSIVE subtree.*WHERE user_id = \$1 AND drive_id = \$2 AND id = \$3.*child.user_id = parent.user_id AND child.drive_id = parent.drive_id.*WHERE child.user_id = \$1 AND child.drive_id = \$2`
	mock.ExpectExec(tree+`.*INSERT INTO desktop_drive_garbage.*SELECT user_id, id FROM subtree WHERE item_type = 'file'`).
		WithArgs(driveRepoTestUser, driveRepoTestDrive, driveRepoTestItem).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(tree+`.*DELETE FROM desktop_drive_items WHERE user_id = \$1 AND drive_id = \$2 AND id IN \(SELECT id FROM subtree\)`).
		WithArgs(driveRepoTestUser, driveRepoTestDrive, driveRepoTestItem).WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()
	require.NoError(t, repo.WithUser(context.Background(), driveRepoTestUser, func(tx service.DesktopDriveStore) error {
		return tx.Delete(context.Background(), driveRepoTestDrive, driveRepoTestItem)
	}))
}

func TestDesktopDriveRepositoryFailedCleanupIntentRollsBackDeletion(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	want := errors.New("cleanup queue unavailable")
	mock.ExpectExec(`(?s)INSERT INTO desktop_drive_garbage.*WHERE user_id = \$1 AND drive_id = \$2 AND item_type = 'file'`).
		WithArgs(driveRepoTestUser, driveRepoTestDrive).WillReturnError(want)
	mock.ExpectRollback()
	err := repo.WithUser(context.Background(), driveRepoTestUser, func(tx service.DesktopDriveStore) error {
		return tx.Delete(context.Background(), driveRepoTestDrive, "")
	})
	require.ErrorIs(t, err, want)
}

func TestDesktopDriveRepositoryGarbageCannotCrossOwner(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	mock.ExpectQuery(`SELECT file_id::text FROM desktop_drive_garbage WHERE user_id = \$1 ORDER BY created_at, file_id LIMIT 512`).
		WithArgs(driveRepoTestUser).WillReturnRows(sqlmock.NewRows([]string{"file_id"}).AddRow(driveRepoTestItem))
	mock.ExpectExec(`DELETE FROM desktop_drive_garbage WHERE user_id = \$1 AND file_id = \$2`).
		WithArgs(driveRepoTestUser, driveRepoTestItem).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.WithUser(context.Background(), driveRepoTestUser, func(tx service.DesktopDriveStore) error {
		files, err := tx.Garbage(context.Background())
		require.NoError(t, err)
		require.Equal(t, []string{driveRepoTestItem}, files)
		return tx.ForgetGarbage(context.Background(), files[0])
	}))
}

func TestDesktopDriveRepositoryFileExistenceBindsOwner(t *testing.T) {
	repo, mock := desktopDriveRepoTest(t)
	desktopDriveExpectUser(mock)
	const query = `SELECT EXISTS \(SELECT 1 FROM desktop_drive_items WHERE user_id = \$1 AND id = \$2 AND item_type = 'file'\)$`
	mock.ExpectQuery(query).WithArgs(driveRepoTestUser, driveRepoTestItem).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(query).WithArgs(driveRepoTestUser, driveRepoTestDrive).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectCommit()
	require.NoError(t, repo.WithUser(context.Background(), driveRepoTestUser, func(store service.DesktopDriveStore) error {
		exists, err := store.FileExists(context.Background(), driveRepoTestItem)
		require.NoError(t, err)
		require.True(t, exists)
		exists, err = store.FileExists(context.Background(), driveRepoTestDrive)
		require.NoError(t, err)
		require.False(t, exists)
		return nil
	}))
}

func TestDesktopDriveRepositoryRefusesUnboundUser(t *testing.T) {
	repo, _ := desktopDriveRepoTest(t)
	err := repo.WithUser(context.Background(), 0, func(service.DesktopDriveStore) error {
		t.Fatal("unbound callback must never execute")
		return nil
	})
	require.Error(t, err)
}
