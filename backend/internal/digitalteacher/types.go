// Package digitalteacher owns the bounded digital teacher session lifecycle. It does
// not expose provider credentials, account-wide catalogs, or management tokens.
package digitalteacher

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/speechvoices"
	"github.com/google/uuid"
)

const Model = "liveavatar-full"
const SandboxAvatar = "dd73ea75-1218-4ef3-92ce-606d5f7fbc0a"

type Error struct {
	Status        int
	Code, Message string
	cause         error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }
func fail(status int, code, message string) error {
	return &Error{Status: status, Code: code, Message: message}
}

var ErrNotFound = &Error{Status: 404, Code: "digital_teacher_not_found", Message: "未找到当前账户的数字老师或会话"}
var ErrConflict = &Error{Status: 409, Code: "digital_teacher_pending", Message: "已有会话正在处理，请先查看或结束原会话"}

type Owner struct{ UserID, KeyID, GroupID int64 }
type Plan struct {
	GroupID        int64   `json:"group_id"`
	Enabled        bool    `json:"enabled"`
	SandboxEnabled bool    `json:"sandbox_enabled"`
	PricePerMinute float64 `json:"price_per_minute"`
	MaxMinutes     int     `json:"max_minutes"`
	Version        int64   `json:"version"`
}
type Teacher struct {
	Provider     string `json:"provider,omitempty"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Subject      string `json:"subject"`
	Instructions string `json:"instructions"`
	Opening      string `json:"opening"`
	AvatarID     string `json:"avatarId"`
	VoiceID      string `json:"voiceId"`
	LLMModel     string `json:"llmModel,omitempty"`
	VoiceModel   string `json:"voiceModel,omitempty"`
}

func (t Teacher) Validate() error {
	voiceValid := validUUID(t.VoiceID) || t.Provider == "simli" && (speechvoices.Valid(t.VoiceID) || elevenID.MatchString(t.VoiceID))
	if t.LLMModel != "" && !contains(studioModels, t.LLMModel) || t.VoiceModel != "" && !contains(studioVoiceModels, t.VoiceModel) {
		return fail(400, "invalid_studio_model", "请选择当前支持的对话与语音模型")
	}
	if (t.Provider != "" && t.Provider != "liveavatar" && t.Provider != "simli") || !validUUID(t.ID) || !validUUID(t.AvatarID) || !voiceValid || strings.TrimSpace(t.Name) == "" || len([]rune(t.Name)) > 40 || strings.TrimSpace(t.Subject) == "" || len([]rune(t.Subject)) > 80 || strings.TrimSpace(t.Instructions) == "" || len([]rune(t.Instructions)) > 12000 || len([]rune(t.Opening)) > 500 {
		return fail(400, "invalid_teacher", "请完整填写老师姓名、形象、声音与授课要求")
	}
	return nil
}

type Session struct {
	Provider        string          `json:"provider"`
	ID              string          `json:"id"`
	UserID          int64           `json:"-"`
	KeyID           int64           `json:"-"`
	GroupID         int64           `json:"-"`
	RequestID       string          `json:"request_id"`
	Digest          string          `json:"-"`
	TeacherID       string          `json:"-"`
	Sandbox         bool            `json:"sandbox"`
	MaxSeconds      int             `json:"max_seconds"`
	PlanVersion     int64           `json:"plan_version"`
	Price           float64         `json:"price_per_minute"`
	Held            float64         `json:"reserved_credits"`
	MinuteHold      float64         `json:"reserved_minutes"`
	Phase           string          `json:"state"`
	UpstreamID      string          `json:"-"`
	UpstreamToken   string          `json:"-"`
	StartResponse   string          `json:"-"`
	StartRequested  *time.Time      `json:"-"`
	Nonce           string          `json:"-"`
	StopRequested   bool            `json:"stop_requested"`
	ProviderCredits *float64        `json:"provider_credits"`
	ProviderSeconds *float64        `json:"duration_seconds"`
	Charged         *float64        `json:"charged_credits"`
	Receipt         json.RawMessage `json:"-"`
	ReceiptSeen     *time.Time      `json:"-"`
	SettledAt       *time.Time      `json:"settled_at"`
	ErrorCode       string          `json:"error_code,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	Deadline        time.Time       `json:"deadline"`
}
type StartRequest struct {
	RequestID string `json:"request_id"`
	TeacherID string `json:"teacher_id"`
	Sandbox   bool   `json:"sandbox"`
	Minutes   int    `json:"minutes"`
}
type Ticket struct {
	Provider     string  `json:"provider"`
	SessionToken string  `json:"sessionToken"`
	SessionID    string  `json:"sessionId"`
	Seconds      int     `json:"seconds"`
	APIURL       string  `json:"apiUrl"`
	Price        float64 `json:"price_per_minute"`
	Reserved     float64 `json:"reserved_credits"`
}
type Options struct {
	APIKey, CryptoSecret  string
	SimliAPIKey, Provider string
	StudioUsers           string
	Invalidate            func(context.Context, int64, int64)
}
type Gateway struct {
	db          *sql.DB
	key         string
	simliKey    string
	provider    string
	simliURL    string
	simliWSURL  string
	elevenURL   string
	studioUsers map[int64]bool
	voiceCache  studioVoiceCache
	secret      []byte
	box         cipher.AEAD
	client      *http.Client
	upstream    string
	invalidate  func(context.Context, int64, int64)
	now         func() time.Time
}

func New(db *sql.DB, o Options) (*Gateway, error) {
	if len(o.CryptoSecret) < 32 {
		return nil, errors.New("digital teacher encryption requires a stable secret of at least 32 bytes")
	}
	sum := sha256.Sum256([]byte("windhub-digital-teacher-v1:" + o.CryptoSecret))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	box, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	provider := o.Provider
	if provider == "" {
		provider = "liveavatar"
	}
	if provider != "liveavatar" && provider != "simli" {
		return nil, errors.New("invalid digital teacher provider")
	}
	return &Gateway{db: db, key: strings.TrimSpace(o.APIKey), simliKey: strings.TrimSpace(o.SimliAPIKey), provider: provider, simliURL: "https://api.simli.ai", simliWSURL: "wss://api.simli.ai", elevenURL: "https://api.elevenlabs.io", studioUsers: parseStudioUsers(o.StudioUsers), secret: sum[:], box: box, upstream: "https://api.liveavatar.com", client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, invalidate: o.Invalidate, now: time.Now}, nil
}
func (g *Gateway) Configured() bool {
	return g != nil && (g.provider == "simli" && g.simliKey != "" || g.provider == "liveavatar" && g.key != "")
}
func validUUID(s string) bool { _, err := uuid.Parse(s); return err == nil && len(s) == 36 }
func digest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func (g *Gateway) seal(s string) (string, error) {
	nonce := make([]byte, g.box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(g.box.Seal(nonce, nonce, []byte(s), nil)), nil
}
func (g *Gateway) unseal(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) < g.box.NonceSize() {
		return "", errors.New("invalid protected session")
	}
	p, err := g.box.Open(nil, b[:g.box.NonceSize()], b[g.box.NonceSize():], nil)
	return string(p), err
}

type capabilityClaims struct {
	ID        string `json:"sub"`
	Nonce     string `json:"jti"`
	Expires   int64  `json:"exp"`
	Audience  string `json:"aud"`
	StartData struct {
		Mode      string `json:"mode"`
		AgentType string `json:"agent_type"`
	} `json:"start_session_data"`
}

func (g *Gateway) capability(s *Session) string {
	claims := capabilityClaims{ID: s.ID, Nonce: s.Nonce, Expires: s.Deadline.Add(10 * time.Minute).Unix(), Audience: "windhub-digital-teacher"}
	claims.StartData.Mode = "FULL"
	claims.StartData.AgentType = "FULL"
	payload, _ := json.Marshal(claims)
	// The SDK reads these public mode claims. This is still a WindHub-only
	// credential and contains no supplier session token or teacher prompt.
	body := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + base64.RawURLEncoding.EncodeToString(payload)
	m := hmac.New(sha256.New, g.secret)
	m.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (g *Gateway) ResolveCapability(ctx context.Context, token string) (*Session, error) {
	p := strings.Split(token, ".")
	if len(token) > 1024 || len(p) != 3 || p[0] != "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" {
		return nil, ErrNotFound
	}
	body := strings.Join(p[:2], ".")
	m := hmac.New(sha256.New, g.secret)
	m.Write([]byte(body))
	signature, err := base64.RawURLEncoding.DecodeString(p[2])
	if err != nil || !hmac.Equal(signature, m.Sum(nil)) {
		return nil, ErrNotFound
	}
	payload, err := base64.RawURLEncoding.DecodeString(p[1])
	var claims capabilityClaims
	if err != nil || json.Unmarshal(payload, &claims) != nil || !validUUID(claims.ID) || claims.Audience != "windhub-digital-teacher" || claims.StartData.Mode != "FULL" || claims.StartData.AgentType != "FULL" || g.now().Unix() > claims.Expires {
		return nil, ErrNotFound
	}
	s, err := g.session(ctx, claims.ID)
	if err != nil || !hmac.Equal([]byte(s.Nonce), []byte(claims.Nonce)) {
		return nil, ErrNotFound
	}
	return s, nil
}
func (g *Gateway) changed(ctx context.Context, s *Session) {
	if g.invalidate != nil {
		g.invalidate(ctx, s.UserID, s.KeyID)
	}
}
func (g *Gateway) Plan(ctx context.Context, id int64) (Plan, error) {
	p := Plan{GroupID: id, MaxMinutes: 2}
	err := g.db.QueryRowContext(ctx, `SELECT enabled,sandbox_enabled,price_per_minute,max_minutes,version FROM digital_teacher_plans WHERE group_id=$1`, id).Scan(&p.Enabled, &p.SandboxEnabled, &p.PricePerMinute, &p.MaxMinutes, &p.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	return p, err
}
func (g *Gateway) requirePlan(ctx context.Context, o Owner, sandbox bool) (Plan, error) {
	p, err := g.Plan(ctx, o.GroupID)
	if err != nil {
		return p, err
	}
	if !g.Configured() {
		return p, fail(503, "digital_teacher_unconfigured", "数字老师服务尚未配置")
	}
	if !p.Enabled || sandbox && !p.SandboxEnabled {
		return p, fail(403, "digital_teacher_not_enabled", "当前算力包尚未开通此数字老师服务")
	}
	return p, nil
}
func (g *Gateway) Ticket(s *Session) Ticket {
	api := "https://windhub.online/v1/digital-teachers/sdk"
	if s.Provider == "simli" {
		api = "wss://windhub.online/v1/digital-teachers/sdk"
	}
	return Ticket{Provider: s.Provider, SessionToken: g.capability(s), SessionID: s.ID, Seconds: s.MaxSeconds, APIURL: api, Price: s.Price, Reserved: s.Held}
}
func internalError(err error) error {
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return &Error{Status: 503, Code: "digital_teacher_unavailable", Message: "数字老师服务暂不可用，请稍后查看原会话", cause: err}
}
func (g *Gateway) Health() map[string]any {
	model := Model
	if g.provider == "simli" {
		model = "simli-compose"
	}
	return map[string]any{"provider_configured": g.Configured(), "provider": g.provider, "model": model, "billing_unit": "minute"}
}
func positive(v float64) bool { return v > 0 && v < 1e12 }
func (g *Gateway) owned(ctx context.Context, o Owner, id string) (*Session, error) {
	s, err := g.session(ctx, id)
	if err != nil || s.UserID != o.UserID || s.KeyID != o.KeyID {
		return nil, ErrNotFound
	}
	return s, nil
}
func (g *Gateway) Get(ctx context.Context, o Owner, id string) (*Session, error) {
	return g.owned(ctx, o, id)
}
func (g *Gateway) String() string {
	return fmt.Sprintf("digitalteacher(configured=%t)", g.Configured())
}
