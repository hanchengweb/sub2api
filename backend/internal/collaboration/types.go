// Package collaboration implements personal cloud rooms. It never reads local
// workspaces, organization projects, private chats or private user memories.
package collaboration

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type Room struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerID     string `json:"ownerId"`
	ProjectID   string `json:"projectId,omitempty"`
	Role        string `json:"role"`
	MemberCount int    `json:"memberCount"`
}
type Member struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Role   string `json:"role"`
}
type Agent struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Model        string `json:"model"`
}
type Message struct {
	ID          string    `json:"id"`
	Seq         int64     `json:"seq"`
	Kind        string    `json:"kind"`
	AuthorID    string    `json:"authorId"`
	AuthorName  string    `json:"authorName"`
	Text        string    `json:"text"`
	CreatedAt   time.Time `json:"createdAt"`
	ReplyToID   string    `json:"replyToId,omitempty"`
	RunID       string    `json:"runId,omitempty"`
	Attachments []Asset   `json:"attachments,omitempty"`
}
type Run struct {
	ID           string `json:"id"`
	MessageID    string `json:"messageId"`
	AgentID      string `json:"agentId"`
	AgentName    string `json:"agentName"`
	Status       string `json:"status"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}
type Task struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	AssigneeID string    `json:"assigneeId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	Revision   int64     `json:"revision"`
}
type Input struct {
	ClientRequestID string   `json:"clientRequestId"`
	Name            string   `json:"name"`
	ProjectName     string   `json:"projectName"`
	Instructions    string   `json:"instructions"`
	Model           string   `json:"model"`
	Token           string   `json:"token"`
	Text            string   `json:"text"`
	MentionIDs      []string `json:"mentionIds"`
	AttachmentIDs   []string `json:"attachmentIds"`
	Title           *string  `json:"title"`
	Status          *string  `json:"status"`
	AssigneeID      *string  `json:"assigneeId"`
	Revision        int64    `json:"revision"`
}
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string     { return e.Message }
func invalid(message string) error { return &Error{400, "COLLABORATION_INVALID", message} }

var denied = &Error{403, "COLLABORATION_FORBIDDEN", "无权访问此群聊"}
var conflict = &Error{409, "COLLABORATION_CONFLICT", "内容已变化，请刷新后重试"}
var unavailable = &Error{503, "COLLABORATION_UNAVAILABLE", "个人协作服务尚未启用"}

type KeyReader interface {
	GetByID(context.Context, int64) (*service.APIKey, error)
}
type Store struct{ DB *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{DB: db} }
func validID(id string) bool {
	u, e := uuid.Parse(id)
	return e == nil && u != uuid.Nil && u.String() == id
}
func userID(id string) (int64, error) {
	n, e := strconv.ParseInt(id, 10, 64)
	if e != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
		return 0, invalid("成员无效")
	}
	return n, nil
}
func validateText(s string, max int) bool {
	return strings.TrimSpace(s) != "" && utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && !strings.ContainsRune(s, 0)
}
func displayName(key *service.APIKey) string {
	if name := strings.TrimSpace(key.User.Username); name != "" {
		return truncate(name, 160)
	}
	return "用户 " + strconv.FormatInt(key.UserID, 10)
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func ValidateKey(key *service.APIKey) error {
	if err := service.ValidateDesktopDriveKey(key); err != nil {
		return err
	}
	if key.Key == "" || key.ID <= 0 {
		return denied
	}
	return nil
}
func rollback(tx *sql.Tx) { _ = tx.Rollback() }
func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) (any, error)) (result any, err error) {
	if s == nil || s.DB == nil {
		return nil, unavailable
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer rollback(tx)
	result, e = fn(tx)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return result, nil
}
func roomLock(ctx context.Context, tx *sql.Tx, id string, uid int64, owner bool) (*Room, error) {
	if !validID(id) {
		return nil, invalid("群聊无效")
	}
	// Lock the room for reads as well, making membership revocation linear with
	// each authorized snapshot. No payload leaves the transaction unverified.
	var r Room
	err := tx.QueryRowContext(ctx, `SELECT r.id::text,r.name,r.owner_id::text,COALESCE(r.project_id::text,''),
        CASE WHEN r.owner_id=$2 THEN 'owner' ELSE 'member' END,
        (SELECT count(*) FROM desktop_collaboration_members WHERE room_id=r.id AND active)
        FROM desktop_collaboration_rooms r JOIN desktop_collaboration_members m ON m.room_id=r.id AND m.user_id=$2 AND m.active
        WHERE r.id=$1 FOR UPDATE OF r`, id, uid).Scan(&r.ID, &r.Name, &r.OwnerID, &r.ProjectID, &r.Role, &r.MemberCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, denied
	}
	if err != nil {
		return nil, err
	}
	if owner && r.Role != "owner" {
		return nil, denied
	}
	return &r, nil
}
