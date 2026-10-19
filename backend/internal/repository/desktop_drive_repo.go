package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type desktopDriveRepository struct {
	db *sql.DB
}

type desktopDriveStore struct {
	tx     *sql.Tx
	userID int64
}

// NewDesktopDriveRepository keeps every operation inside a user-bound transaction.
func NewDesktopDriveRepository(db *sql.DB) service.DesktopDriveRepository {
	return &desktopDriveRepository{db: db}
}

func (r *desktopDriveRepository) WithUser(ctx context.Context, userID int64, fn func(service.DesktopDriveStore) error) error {
	if r == nil || r.db == nil || userID <= 0 || fn == nil {
		return errors.New("desktop drive repository requires a database, user and callback")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Separate namespace from all other advisory locks. A hash collision only
	// serializes two users; each query still independently binds its owner.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1464353874, hashint8($1::bigint))`, userID); err != nil {
		return err
	}
	if err = fn(&desktopDriveStore{tx: tx, userID: userID}); err != nil {
		return err
	}
	return tx.Commit()
}

const desktopDriveColumns = `d.id::text, d.name,
	(SELECT count(*) FROM desktop_drive_items i WHERE i.user_id = d.user_id AND i.drive_id = d.id),
	'ready', 'private'`

func scanDesktopDrive(row interface{ Scan(...any) error }) (*service.DesktopDrive, error) {
	var drive service.DesktopDrive
	if err := row.Scan(&drive.ID, &drive.Name, &drive.ItemCount, &drive.Status, &drive.Scope); err != nil {
		return nil, err
	}
	return &drive, nil
}

func (s *desktopDriveStore) Drives(ctx context.Context) ([]service.DesktopDrive, error) {
	rows, err := s.tx.QueryContext(ctx, `SELECT `+desktopDriveColumns+` FROM desktop_drives d
		WHERE d.user_id = $1 ORDER BY d.updated_at DESC, d.id`, s.userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	drives := []service.DesktopDrive{}
	for rows.Next() {
		drive, err := scanDesktopDrive(rows)
		if err != nil {
			return nil, err
		}
		drives = append(drives, *drive)
	}
	return drives, rows.Err()
}

func (s *desktopDriveStore) Drive(ctx context.Context, id string) (*service.DesktopDrive, error) {
	drive, err := scanDesktopDrive(s.tx.QueryRowContext(ctx, `SELECT `+desktopDriveColumns+`
		FROM desktop_drives d WHERE d.user_id = $1 AND d.id = $2`, s.userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return drive, err
}

const desktopDriveItemColumns = `id::text, name, item_type, file_name, media_type, size_bytes, COALESCE(parent_id::text, ''), sha256`

func scanDesktopDriveItem(row interface{ Scan(...any) error }) (*service.DesktopDriveItem, error) {
	var item service.DesktopDriveItem
	if err := row.Scan(&item.ID, &item.Name, &item.ItemType, &item.FileName, &item.MediaType, &item.SizeBytes, &item.ParentID, &item.SHA256); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *desktopDriveStore) Item(ctx context.Context, driveID, itemID string) (*service.DesktopDriveItem, error) {
	item, err := scanDesktopDriveItem(s.tx.QueryRowContext(ctx, `SELECT `+desktopDriveItemColumns+`
		FROM desktop_drive_items WHERE user_id = $1 AND drive_id = $2 AND id = $3`, s.userID, driveID, itemID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return item, err
}

func (s *desktopDriveStore) FileExists(ctx context.Context, itemID string) (bool, error) {
	var exists bool
	err := s.tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM desktop_drive_items
		WHERE user_id = $1 AND id = $2 AND item_type = 'file')`, s.userID, itemID).Scan(&exists)
	return exists, err
}

func (s *desktopDriveStore) Items(ctx context.Context, driveID, parentID string, offset, limit int) ([]service.DesktopDriveItem, int64, error) {
	if offset < 0 || limit <= 0 || limit > 200 {
		return nil, 0, errors.New("invalid desktop drive pagination")
	}
	const predicate = `user_id = $1 AND drive_id = $2 AND parent_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid`
	var total int64
	if err := s.tx.QueryRowContext(ctx, `SELECT count(*) FROM desktop_drive_items WHERE `+predicate,
		s.userID, driveID, parentID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.tx.QueryContext(ctx, `SELECT `+desktopDriveItemColumns+` FROM desktop_drive_items WHERE `+predicate+`
		ORDER BY (item_type = 'folder') DESC, name, id OFFSET $4 LIMIT $5`, s.userID, driveID, parentID, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := []service.DesktopDriveItem{}
	for rows.Next() {
		item, err := scanDesktopDriveItem(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	return items, total, rows.Err()
}

func (s *desktopDriveStore) Usage(ctx context.Context) (int64, int64, error) {
	var size, count int64
	err := s.tx.QueryRowContext(ctx, `SELECT COALESCE(sum(size_bytes), 0),
		count(*) + (SELECT count(*) FROM desktop_drives WHERE user_id = $1)
		FROM desktop_drive_items WHERE user_id = $1`, s.userID).Scan(&size, &count)
	return size, count, err
}

func (s *desktopDriveStore) CreateDrive(ctx context.Context, drive service.DesktopDrive) error {
	_, err := s.tx.ExecContext(ctx, `INSERT INTO desktop_drives (id, user_id, name) VALUES ($2, $1, $3)`,
		s.userID, drive.ID, drive.Name)
	return err
}

func desktopDriveAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *desktopDriveStore) CreateItem(ctx context.Context, driveID string, item service.DesktopDriveItem) error {
	result, err := s.tx.ExecContext(ctx, `INSERT INTO desktop_drive_items
		(id, user_id, drive_id, parent_id, name, item_type, file_name, media_type, size_bytes, sha256)
		SELECT $3::uuid, $1, $2::uuid, NULLIF($4, '')::uuid, $5, $6, $7, $8, $9, $10
		WHERE EXISTS (SELECT 1 FROM desktop_drives WHERE user_id = $1 AND id = $2)
		AND ($4 = '' OR EXISTS (SELECT 1 FROM desktop_drive_items
			WHERE user_id = $1 AND drive_id = $2 AND id = NULLIF($4, '')::uuid AND item_type = 'folder'))`,
		s.userID, driveID, item.ID, item.ParentID, item.Name, item.ItemType, item.FileName, item.MediaType, item.SizeBytes, item.SHA256)
	return desktopDriveAffected(result, err)
}

func (s *desktopDriveStore) Rename(ctx context.Context, driveID, itemID, name string) error {
	if itemID == "" {
		result, err := s.tx.ExecContext(ctx, `UPDATE desktop_drives SET name = $3, updated_at = NOW()
			WHERE user_id = $1 AND id = $2`, s.userID, driveID, name)
		return desktopDriveAffected(result, err)
	}
	result, err := s.tx.ExecContext(ctx, `UPDATE desktop_drive_items SET name = $4,
		file_name = CASE WHEN item_type = 'file' THEN $4 ELSE file_name END, updated_at = NOW()
		WHERE user_id = $1 AND drive_id = $2 AND id = $3`, s.userID, driveID, itemID, name)
	return desktopDriveAffected(result, err)
}

// Every recursive edge includes ownership and drive identity. Cleanup intents
// are durable before metadata is removed, in the same WithUser transaction.
const desktopDriveSubtree = `WITH RECURSIVE subtree AS (
	SELECT id, user_id, drive_id, item_type FROM desktop_drive_items
	WHERE user_id = $1 AND drive_id = $2 AND id = $3
	UNION ALL
	SELECT child.id, child.user_id, child.drive_id, child.item_type
	FROM desktop_drive_items child JOIN subtree parent
	ON child.parent_id = parent.id AND child.user_id = parent.user_id AND child.drive_id = parent.drive_id
	WHERE child.user_id = $1 AND child.drive_id = $2
) `

func (s *desktopDriveStore) Delete(ctx context.Context, driveID, itemID string) error {
	if itemID == "" {
		if _, err := s.tx.ExecContext(ctx, `INSERT INTO desktop_drive_garbage (user_id, file_id)
			SELECT user_id, id FROM desktop_drive_items
			WHERE user_id = $1 AND drive_id = $2 AND item_type = 'file'
			ON CONFLICT (user_id, file_id) DO NOTHING`, s.userID, driveID); err != nil {
			return err
		}
		_, err := s.tx.ExecContext(ctx, `DELETE FROM desktop_drives WHERE user_id = $1 AND id = $2`, s.userID, driveID)
		return err
	}
	if _, err := s.tx.ExecContext(ctx, desktopDriveSubtree+`INSERT INTO desktop_drive_garbage (user_id, file_id)
		SELECT user_id, id FROM subtree WHERE item_type = 'file'
		ON CONFLICT (user_id, file_id) DO NOTHING`, s.userID, driveID, itemID); err != nil {
		return err
	}
	_, err := s.tx.ExecContext(ctx, desktopDriveSubtree+`DELETE FROM desktop_drive_items
		WHERE user_id = $1 AND drive_id = $2 AND id IN (SELECT id FROM subtree)`, s.userID, driveID, itemID)
	return err
}

func (s *desktopDriveStore) Request(ctx context.Context, key string) (*service.DesktopDriveRequest, error) {
	var request service.DesktopDriveRequest
	err := s.tx.QueryRowContext(ctx, `SELECT fingerprint, result FROM desktop_drive_requests
		WHERE user_id = $1 AND request_id = $2`, s.userID, key).Scan(&request.Fingerprint, &request.Result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &request, err
}

func (s *desktopDriveStore) SaveRequest(ctx context.Context, key string, request service.DesktopDriveRequest) error {
	_, err := s.tx.ExecContext(ctx, `INSERT INTO desktop_drive_requests (user_id, request_id, fingerprint, result)
		VALUES ($1, $2, $3, $4::jsonb)`, s.userID, key, request.Fingerprint, string(request.Result))
	return err
}

func (s *desktopDriveStore) Garbage(ctx context.Context) ([]string, error) {
	rows, err := s.tx.QueryContext(ctx, `SELECT file_id::text FROM desktop_drive_garbage
		WHERE user_id = $1 ORDER BY created_at, file_id LIMIT 512`, s.userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	files := []string{}
	for rows.Next() {
		var fileID string
		if err := rows.Scan(&fileID); err != nil {
			return nil, err
		}
		files = append(files, fileID)
	}
	return files, rows.Err()
}

func (s *desktopDriveStore) ForgetGarbage(ctx context.Context, fileID string) error {
	_, err := s.tx.ExecContext(ctx, `DELETE FROM desktop_drive_garbage WHERE user_id = $1 AND file_id = $2`, s.userID, fileID)
	return err
}
