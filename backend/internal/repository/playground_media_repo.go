package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type playgroundMediaRepository struct {
	db *sql.DB
}

// NewPlaygroundMediaRepository 在线使用生成结果的转存记录仓储。
//
// 只要 *sql.DB，不收 ent client：这张表全是手写 SQL，多带一个用不上的
// 参数只会把 ent 拖进依赖图。
func NewPlaygroundMediaRepository(sqlDB *sql.DB) service.PlaygroundMediaRepository {
	return &playgroundMediaRepository{db: sqlDB}
}

const playgroundMediaColumns = `id, user_id, task_id, kind, source_url, stored_path, mime_type, size_bytes, created_at`

func scanPlaygroundMedia(row interface{ Scan(...any) error }) (*service.PlaygroundMedia, error) {
	var m service.PlaygroundMedia
	if err := row.Scan(&m.ID, &m.UserID, &m.TaskID, &m.Kind, &m.SourceURL,
		&m.StoredPath, &m.MimeType, &m.SizeBytes, &m.CreatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// FindBySource 按 (用户, 源地址) 查。
//
// 条件用 md5(source_url) 与唯一索引保持一致：source_url 是 TEXT，
// 直接等值比较用不上那个函数索引，量大了会退化成全表扫。
func (r *playgroundMediaRepository) FindBySource(ctx context.Context, userID int64, sourceURL string) (*service.PlaygroundMedia, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("playground media repository db is nil")
	}
	sourceURL = strings.TrimSpace(sourceURL)
	if userID <= 0 || sourceURL == "" {
		return nil, nil
	}
	const q = `SELECT ` + playgroundMediaColumns + `
		FROM playground_media
		WHERE user_id = $1 AND md5(source_url) = md5($2) AND source_url = $2`
	media, err := scanPlaygroundMedia(r.db.QueryRowContext(ctx, q, userID, sourceURL))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return media, err
}

// Insert 落库。
//
// ON CONFLICT DO NOTHING + 回查：两条请求同时转存同一张图时，后到的那条
// 不该报错失败——它要的结果（一条可用记录）已经有了，直接返回既有的即可。
func (r *playgroundMediaRepository) Insert(ctx context.Context, media *service.PlaygroundMedia) (*service.PlaygroundMedia, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("playground media repository db is nil")
	}
	if media == nil {
		return nil, errors.New("media is nil")
	}
	const q = `INSERT INTO playground_media
		(user_id, task_id, kind, source_url, stored_path, mime_type, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT DO NOTHING
		RETURNING ` + playgroundMediaColumns
	stored, err := scanPlaygroundMedia(r.db.QueryRowContext(ctx, q,
		media.UserID, media.TaskID, media.Kind, media.SourceURL,
		media.StoredPath, media.MimeType, media.SizeBytes))
	if errors.Is(err, sql.ErrNoRows) {
		return r.FindBySource(ctx, media.UserID, media.SourceURL)
	}
	return stored, err
}

// GetOwned 取该用户自己的记录。
//
// user_id 写进 WHERE 而不是查完再比：越权访问这种事不能靠调用方记得校验。
func (r *playgroundMediaRepository) GetOwned(ctx context.Context, userID, id int64) (*service.PlaygroundMedia, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("playground media repository db is nil")
	}
	if userID <= 0 || id <= 0 {
		return nil, nil
	}
	const q = `SELECT ` + playgroundMediaColumns + ` FROM playground_media WHERE id = $1 AND user_id = $2`
	media, err := scanPlaygroundMedia(r.db.QueryRowContext(ctx, q, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return media, err
}
