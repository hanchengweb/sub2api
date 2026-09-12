package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/lib/pq"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type playgroundConversationRepository struct {
	db *sql.DB
}

// NewPlaygroundConversationRepository 在线使用会话的仓储。
func NewPlaygroundConversationRepository(sqlDB *sql.DB) service.PlaygroundConversationRepository {
	return &playgroundConversationRepository{db: sqlDB}
}

// List 一次取回会话与消息。
//
// 分两条查询而不是 JOIN：JOIN 会把会话字段在每条消息上重复一遍，
// 200 条消息的会话要传 200 份标题和模型名。两条查询在内存里拼更省。
func (r *playgroundConversationRepository) List(ctx context.Context, userID int64, limit int) ([]service.PlaygroundConversation, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("playground conversation repository db is nil")
	}
	const convSQL = `
		SELECT id, title, mode, model, updated_at
		FROM playground_conversations
		WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT $2`
	rows, err := r.db.QueryContext(ctx, convSQL, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	convs := make([]service.PlaygroundConversation, 0, limit)
	index := map[string]int{}
	for rows.Next() {
		var c service.PlaygroundConversation
		var updated sql.NullTime
		if err := rows.Scan(&c.ID, &c.Title, &c.Mode, &c.Model, &updated); err != nil {
			return nil, err
		}
		if updated.Valid {
			c.UpdatedAt = updated.Time.UnixMilli()
		}
		c.Messages = []service.PlaygroundMessage{}
		index[c.ID] = len(convs)
		convs = append(convs, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(convs) == 0 {
		return convs, nil
	}

	ids := make([]string, 0, len(convs))
	for _, c := range convs {
		ids = append(ids, c.ID)
	}
	const msgSQL = `
		SELECT conversation_id, id, role, content, media_urls, media_kind,
		       task_id, error, needs_top_up, created_at
		FROM playground_messages
		WHERE user_id = $1 AND conversation_id = ANY($2)
		ORDER BY conversation_id, position`
	msgRows, err := r.db.QueryContext(ctx, msgSQL, userID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = msgRows.Close() }()

	for msgRows.Next() {
		var convID string
		var m service.PlaygroundMessage
		var mediaRaw []byte
		var kind, taskID, msgErr sql.NullString
		var created sql.NullTime
		if err := msgRows.Scan(&convID, &m.ID, &m.Role, &m.Content, &mediaRaw,
			&kind, &taskID, &msgErr, &m.NeedsTopUp, &created); err != nil {
			return nil, err
		}
		m.MediaURLs = []string{}
		if len(mediaRaw) > 0 {
			// 解析失败就当没有媒体：一条消息的地址坏了不该让整个列表拉不出来。
			_ = json.Unmarshal(mediaRaw, &m.MediaURLs)
		}
		m.MediaKind = kind.String
		m.TaskID = taskID.String
		m.Error = msgErr.String
		if created.Valid {
			m.CreatedAt = created.Time.UnixMilli()
		}
		if i, ok := index[convID]; ok {
			convs[i].Messages = append(convs[i].Messages, m)
		}
	}
	return convs, msgRows.Err()
}

// Upsert 整条覆盖写入。
//
// 必须在事务里：先删旧消息再插新消息，中途失败会让一条会话只剩半截消息，
// 而这恰好是用户最在意的那部分数据。
func (r *playgroundConversationRepository) Upsert(ctx context.Context, userID int64, conv *service.PlaygroundConversation) (err error) {
	if r == nil || r.db == nil {
		return errors.New("playground conversation repository db is nil")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	const convSQL = `
		INSERT INTO playground_conversations (id, user_id, title, mode, model, updated_at)
		VALUES ($1, $2, $3, $4, $5, to_timestamp($6::double precision / 1000))
		ON CONFLICT (user_id, id) DO UPDATE
		SET title = EXCLUDED.title, mode = EXCLUDED.mode,
		    model = EXCLUDED.model, updated_at = EXCLUDED.updated_at`
	if _, err = tx.ExecContext(ctx, convSQL, conv.ID, userID, conv.Title,
		conv.Mode, conv.Model, conv.UpdatedAt); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx,
		`DELETE FROM playground_messages WHERE user_id = $1 AND conversation_id = $2`,
		userID, conv.ID); err != nil {
		return err
	}

	const msgSQL = `
		INSERT INTO playground_messages
		(id, conversation_id, user_id, position, role, content, media_urls,
		 media_kind, task_id, error, needs_top_up, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11,
		        to_timestamp($12::double precision / 1000))`
	for i := range conv.Messages {
		m := &conv.Messages[i]
		media, marshalErr := json.Marshal(m.MediaURLs)
		if marshalErr != nil {
			media = []byte("[]")
		}
		if _, err = tx.ExecContext(ctx, msgSQL, m.ID, conv.ID, userID, m.Position,
			m.Role, m.Content, string(media), nullIfEmpty(m.MediaKind),
			nullIfEmpty(m.TaskID), nullIfEmpty(m.Error), m.NeedsTopUp, m.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *playgroundConversationRepository) Delete(ctx context.Context, userID int64, id string) error {
	if r == nil || r.db == nil {
		return errors.New("playground conversation repository db is nil")
	}
	// 消息先删：没有外键级联，留着就是一批永远查不到的孤儿行。
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM playground_messages WHERE user_id = $1 AND conversation_id = $2`,
		userID, id); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM playground_conversations WHERE user_id = $1 AND id = $2`, userID, id)
	return err
}

// TrimOldest 只保留最近 keep 条会话，返回删掉的条数。
func (r *playgroundConversationRepository) TrimOldest(ctx context.Context, userID int64, keep int) (int, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("playground conversation repository db is nil")
	}
	const pickSQL = `
		SELECT id FROM playground_conversations
		WHERE user_id = $1
		ORDER BY updated_at DESC
		OFFSET $2`
	rows, err := r.db.QueryContext(ctx, pickSQL, userID, keep)
	if err != nil {
		return 0, err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		stale = append(stale, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	if len(stale) == 0 {
		return 0, nil
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM playground_messages WHERE user_id = $1 AND conversation_id = ANY($2)`,
		userID, pq.Array(stale)); err != nil {
		return 0, err
	}
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM playground_conversations WHERE user_id = $1 AND id = ANY($2)`,
		userID, pq.Array(stale)); err != nil {
		return 0, err
	}
	return len(stale), nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
