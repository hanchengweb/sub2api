package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

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

// DeleteUnreferenced 删除该用户已无任何会话消息引用的转存记录。
//
// 「有没有被引用」只能从消息里的 media_urls 反查：转存时还不知道这张图
// 最终落在哪条会话上，所以表里没有 conversation_id。存进消息的地址形如
// /api/v1/playground/media/<id>，按整串后缀匹配——不能用 LIKE '%<id>%'，
// 那样 id=12 会把 id=123 的引用也算成自己的，删掉还在用的图。
//
// minAge 是必需的：转存发生在结果刚拿到那一刻，会话要等前端防抖之后才推上来，
// 这期间「没人引用」是正常状态。没有这道门槛就会删掉用户刚生成的图。
func (r *playgroundMediaRepository) DeleteUnreferenced(ctx context.Context, userID int64, minAge time.Duration) ([]string, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("playground media repository db is nil")
	}
	if userID <= 0 {
		return nil, nil
	}
	minutes := int(minAge / time.Minute)
	if minutes < 0 {
		minutes = 0
	}
	// 先把「还被引用的 id」一次性收拢成一个集合，再做反连接。
	// 不写成 NOT EXISTS(... LIKE ...) 的相关子查询：那样每条候选记录都要
	// 重扫一遍该用户的全部消息，会话满载时是百万次比较。
	//
	// 地址形如 /api/v1/playground/media/<id>，按结尾整段取数字。
	// 限 18 位是防 ::bigint 溢出报错——库里不会有这种值，但这条 SQL 读的是
	// 消息内容，而消息内容前端写什么都可能。
	// media_urls 理论上恒为数组，仍套一层 CASE：真出现一行标量，
	// jsonb_array_elements_text 会让整条清理查询直接报错。
	const q = `
		WITH referenced AS (
			SELECT DISTINCT
				substring(u.url from '/playground/media/([0-9]{1,18})$')::bigint AS id
			FROM playground_messages pm
			CROSS JOIN LATERAL jsonb_array_elements_text(
				CASE WHEN jsonb_typeof(pm.media_urls) = 'array'
					THEN pm.media_urls ELSE '[]'::jsonb END
			) AS u(url)
			WHERE pm.user_id = $1
			  AND u.url ~ '/playground/media/[0-9]{1,18}$'
		)
		DELETE FROM playground_media m
		WHERE m.user_id = $1
		  AND m.created_at < NOW() - make_interval(mins => $2)
		  AND NOT EXISTS (SELECT 1 FROM referenced r WHERE r.id = m.id)
		RETURNING stored_path`
	rows, err := r.db.QueryContext(ctx, q, userID, minutes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if strings.TrimSpace(p) != "" {
			paths = append(paths, p)
		}
	}
	return paths, rows.Err()
}
