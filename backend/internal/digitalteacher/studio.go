package digitalteacher

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const studioVoice = "9BWtsMINqrJLrRacOk9x" // Aria, verified in Simli Studio and real Auto audio.
var elevenID = regexp.MustCompile(`^[A-Za-z0-9]{20}$`)
var studioModels = []string{"gpt-4o-mini", "gpt-4.1-mini", "gpt-4o", "gpt-4.1"}
var studioVoiceModels = []string{"eleven_multilingual_v2", "eleven_flash_v2_5"}

type studioVoiceCache struct {
	sync.Mutex
	until  time.Time
	voices []map[string]any
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
func (g *Gateway) StudioEnabled(o Owner) bool {
	return g.providerForOwner(o) == "anam" || g.provider == "simli" && g.studioUsers[o.UserID]
}
func (g *Gateway) teacherForOwner(t Teacher, o Owner) Teacher {
	if g.providerForOwner(o) == "anam" {
		if t.Provider != "anam" {
			t.Provider = "anam"
			t.AvatarID = anamAvatar
			t.VoiceID = anamVoice
			t.LLMModel = anamModel
			t.VoiceModel = "cara-4"
		}
		return t
	}
	t = g.currentTeacher(t)
	if g.StudioEnabled(o) {
		if !elevenID.MatchString(t.VoiceID) {
			t.VoiceID = studioVoice
		}
		if t.LLMModel == "" {
			t.LLMModel = studioModels[0]
		}
		if t.VoiceModel == "" {
			t.VoiceModel = studioVoiceModels[0]
		}
	}
	return t
}

// This is the public ElevenLabs catalog. No supplier key is sent to ElevenLabs.
func (g *Gateway) studioVoices(ctx context.Context) []map[string]any {
	g.voiceCache.Lock()
	defer g.voiceCache.Unlock()
	if g.now().Before(g.voiceCache.until) && len(g.voiceCache.voices) > 0 {
		return g.voiceCache.voices
	}
	out := []map[string]any{{"id": studioVoice, "name": "Aria · Studio 示例声音", "language": "中文 / 多语种", "description": "ElevenLabs · 已实测中文"}}
	fallback := out
	if len(g.voiceCache.voices) > 0 {
		fallback = g.voiceCache.voices
	}
	g.voiceCache.voices = fallback
	g.voiceCache.until = g.now().Add(30 * time.Second)
	req, _ := http.NewRequestWithContext(ctx, "GET", g.elevenURL+"/v1/voices", nil)
	res, err := g.client.Do(req)
	if err != nil {
		return fallback
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fallback
	}
	var data struct {
		Voices []struct {
			ID      string `json:"voice_id"`
			Name    string `json:"name"`
			Preview string `json:"preview_url"`
		} `json:"voices"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&data) != nil {
		return fallback
	}
	for _, v := range data.Voices {
		if !elevenID.MatchString(v.ID) || v.ID == studioVoice || len(v.Name) > 160 {
			continue
		}
		preview := ""
		u, e := url.Parse(v.Preview)
		if e == nil && u.Scheme == "https" && u.User == nil && (u.Host == "storage.googleapis.com" && strings.HasPrefix(u.Path, "/eleven-public-prod/") || (u.Host == "api.us.elevenlabs.io" || u.Host == "api.elevenlabs.io") && strings.HasPrefix(u.Path, "/v1/voices/")) {
			preview = v.Preview
		}
		out = append(out, map[string]any{"id": v.ID, "name": v.Name, "language": "中文 / 多语种", "description": "ElevenLabs · 官方样音为英语，中文效果请开始对话试听", "previewUrl": preview})
		if len(out) >= 80 {
			break
		}
	}
	g.voiceCache.voices = out
	g.voiceCache.until = g.now().Add(10 * time.Minute)
	return out
}

type StudioSession struct {
	ID                     string     `json:"id"`
	RequestID              string     `json:"request_id"`
	Phase                  string     `json:"state"`
	CreatedAt              time.Time  `json:"created_at"`
	Deadline               time.Time  `json:"deadline"`
	ClosedAt               *time.Time `json:"closed_at"`
	Engine                 string     `json:"engine"`
	Provider               string     `json:"provider"`
	Duration               *float64   `json:"duration_seconds,omitempty"`
	digest, room, upstream string
}

const studioColumns = `id::text,request_id,phase,created_at,deadline,closed_at,digest,COALESCE(room_sealed,''),COALESCE(upstream_id::text,''),provider,duration_seconds`

func scanStudio(row scanner) (StudioSession, error) {
	s := StudioSession{Engine: "auto"}
	err := row.Scan(&s.ID, &s.RequestID, &s.Phase, &s.CreatedAt, &s.Deadline, &s.ClosedAt, &s.digest, &s.room, &s.upstream, &s.Provider, &s.Duration)
	if s.Provider == "anam" {
		s.Engine = "anam"
	}
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}
func (g *Gateway) expireStudio(ctx context.Context, o Owner) error {
	// The supplier received maxSessionLength=60 before returning the room. Keep
	// admission closed for startup/timeout uncertainty (150s for an unknown
	// result, or 105s after a room is returned).
	// This closes our evaluation record; it does not invent supplier usage.
	_, err := g.db.ExecContext(ctx, `UPDATE digital_teacher_studio_sessions SET phase='closed',closed_at=NOW(),room_sealed=NULL WHERE user_id=$1 AND provider='simli' AND closed_at IS NULL AND deadline<NOW()`, o.UserID)
	return err
}
func (g *Gateway) StudioList(ctx context.Context, o Owner) ([]StudioSession, error) {
	if err := g.expireStudio(ctx, o); err != nil {
		return nil, err
	}
	rows, err := g.db.QueryContext(ctx, `SELECT `+studioColumns+` FROM digital_teacher_studio_sessions WHERE user_id=$1 AND api_key_id=$2 ORDER BY created_at DESC LIMIT 20`, o.UserID, o.KeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StudioSession{}
	for rows.Next() {
		s, e := scanStudio(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (g *Gateway) StudioStop(ctx context.Context, o Owner, id string) (map[string]any, error) {
	if !validUUID(id) {
		return nil, ErrNotFound
	}
	if err := g.expireStudio(ctx, o); err != nil {
		return nil, err
	}
	s, err := scanStudio(g.db.QueryRowContext(ctx, `SELECT `+studioColumns+` FROM digital_teacher_studio_sessions WHERE user_id=$1 AND api_key_id=$2 AND id=$3`, o.UserID, o.KeyID, id))
	if err != nil {
		return nil, err
	}
	if s.Provider == "anam" {
		if s.ClosedAt == nil {
			_, err = g.db.ExecContext(ctx, `UPDATE digital_teacher_studio_sessions SET phase='closing',room_sealed=NULL WHERE id=$1 AND closed_at IS NULL`, s.ID)
			if err != nil {
				return nil, err
			}
			s.Phase = "closing"
			_ = g.reconcileAnamOne(ctx, s)
			s, err = scanStudio(g.db.QueryRowContext(ctx, `SELECT `+studioColumns+` FROM digital_teacher_studio_sessions WHERE id=$1`, s.ID))
			if err != nil {
				return nil, err
			}
		}
		return map[string]any{"stopped": s.ClosedAt != nil, "settlement_pending": s.ClosedAt == nil, "evaluation": true, "duration_seconds": s.Duration, "message": "本次试用不扣 WindHub 积分或分钟；结束状态以 Anam 回执为准"}, nil
	}
	if s.ClosedAt == nil {
		_, err = g.db.ExecContext(ctx, `UPDATE digital_teacher_studio_sessions SET phase='closing',room_sealed=NULL WHERE id=$1 AND closed_at IS NULL`, s.ID)
	}
	return map[string]any{"stopped": s.ClosedAt != nil, "settlement_pending": s.ClosedAt == nil, "evaluation": true, "retryAfter": s.Deadline, "message": "试用不扣积分或分钟；云端会话有 60 秒上限，等待确认结束后可再次开始"}, err
}
func validStudioRoom(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && len(raw) < 2048 && u.Scheme == "https" && u.Host == "simli.daily.co" && u.User == nil && u.Fragment == "" && len(u.Path) > 1
}
func (g *Gateway) studioTicket(s StudioSession) (map[string]any, error) {
	if s.Phase != "ready" || s.ClosedAt != nil || !g.now().Before(s.Deadline) {
		return nil, ErrConflict
	}
	if s.Provider == "anam" {
		return map[string]any{"provider": "anam", "engine": "anam", "sessionId": s.ID, "sessionToken": g.anamCapability(s), "apiUrl": anamSDK, "seconds": anamSeconds, "evaluation": true, "price_per_minute": 0}, nil
	}
	room, err := g.unseal(s.room)
	if err != nil || !validStudioRoom(room) {
		return nil, fail(503, "studio_room_invalid", "完整会话地址未获确认")
	}
	return map[string]any{"provider": "simli", "engine": "auto", "sessionId": s.ID, "roomUrl": room, "seconds": 60, "evaluation": true, "price_per_minute": 0}, nil
}
func (g *Gateway) StudioCreate(ctx context.Context, o Owner, req StartRequest) (map[string]any, error) {
	if !g.StudioEnabled(o) {
		return nil, fail(403, "studio_not_enabled", "当前账户尚未开放完整效果试用")
	}
	plan, err := g.requirePlan(ctx, o, false)
	if err != nil {
		return nil, err
	}
	if req.Sandbox || req.Minutes > 1 || req.Minutes < 0 || !validUUID(req.RequestID) {
		return nil, fail(400, "studio_request_invalid", "完整效果试用每次最长 60 秒")
	}
	_, teacher, err := g.teacher(ctx, o, req.TeacherID)
	if err != nil {
		return nil, err
	}
	if err = teacher.Validate(); err != nil {
		return nil, err
	}
	catalog, err := g.Catalog(ctx, o)
	if err != nil {
		return nil, err
	}
	face, voice := false, false
	for _, a := range catalog.Avatars {
		if a["id"] == teacher.AvatarID && a["ready"] == true {
			face = true
		}
	}
	for _, v := range catalog.Voices {
		if v["id"] == teacher.VoiceID {
			voice = true
		}
	}
	if !face || !voice || teacher.Provider == "anam" && !contains(catalog.Models, teacher.LLMModel) {
		return nil, fail(403, "studio_asset_unavailable", "所选形象、声音或模型暂不可用，请刷新后重选")
	}
	if err = g.expireStudio(ctx, o); err != nil {
		return nil, err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var userID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, o.UserID).Scan(&userID); err != nil {
		return nil, ErrNotFound
	}
	var allowed, granted, active bool
	var count int
	if err = tx.QueryRowContext(ctx, groupAccessSQL, o.UserID, o.GroupID).Scan(&allowed); err != nil {
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys WHERE id=$1 AND user_id=$2 AND group_id=$3 AND status='active' AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()))`, o.KeyID, o.UserID, o.GroupID).Scan(&granted); err != nil {
		return nil, err
	}
	if !allowed || !granted || !plan.Enabled {
		return nil, ErrNotFound
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM digital_teacher_minutes WHERE user_id=$1 AND granted>0)`, o.UserID).Scan(&granted); err != nil {
		return nil, err
	}
	if !granted {
		return nil, fail(403, "studio_grant_required", "当前账户尚未开通数字老师")
	}
	dg := digest(teacher)
	old, e := scanStudio(tx.QueryRowContext(ctx, `SELECT `+studioColumns+` FROM digital_teacher_studio_sessions WHERE user_id=$1 AND api_key_id=$2 AND request_id=$3`, o.UserID, o.KeyID, req.RequestID))
	if e == nil {
		if old.digest != dg || old.Provider != g.providerForOwner(o) {
			return nil, ErrConflict
		}
		return g.studioTicket(old)
	}
	if !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM digital_teacher_sessions WHERE user_id=$1 AND settled_at IS NULL UNION ALL SELECT 1 FROM digital_teacher_studio_sessions WHERE user_id=$1 AND closed_at IS NULL)`, o.UserID).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM digital_teacher_studio_sessions WHERE user_id=$1 AND provider=$2 AND created_at>NOW()-interval '24 hours'`, o.UserID, g.providerForOwner(o)).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 5 {
		return nil, fail(429, "studio_trial_limit", "该服务近 24 小时已使用 5 次完整效果试用，请稍后再试")
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO digital_teacher_studio_sessions(id,user_id,api_key_id,request_id,digest,deadline,provider) VALUES($1,$2,$3,$4,$5,NOW()+interval '150 seconds',$6)`, id, o.UserID, o.KeyID, req.RequestID, dg, g.providerForOwner(o))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	if teacher.Provider == "anam" {
		return g.startAnam(work, id, teacher)
	}
	prompt := "你是" + teacher.Name + "，教授" + teacher.Subject + "。面向成年学生，使用自然中文交流，回答适合语音播放。\n" + teacher.Instructions
	body := map[string]any{"faceId": teacher.AvatarID, "voiceId": teacher.VoiceID, "ttsProvider": "ElevenLabs", "ttsModel": teacher.VoiceModel, "language": "zh", "model": "artalk", "systemPrompt": prompt, "firstMessage": teacher.Opening, "maxSessionLength": 60, "maxIdleTime": 60, "createTranscript": true, "llmConfig": map[string]string{"model": teacher.LLMModel, "provider": "OpenAI"}}
	data, err := g.simliCall(work, "POST", "/auto/start/configurable", body)
	if err != nil {
		var pe *providerError
		if errors.As(err, &pe) && pe.status >= 400 && pe.status < 500 {
			_, _ = g.db.ExecContext(work, `UPDATE digital_teacher_studio_sessions SET phase='failed',closed_at=NOW() WHERE id=$1`, id)
		}
		return nil, internalError(err)
	}
	var result struct {
		Room string `json:"roomUrl"`
		ID   string `json:"sessionId"`
	}
	if json.Unmarshal(data, &result) != nil || !validUUID(result.ID) || !validStudioRoom(result.Room) {
		return nil, fail(503, "studio_unknown", "完整会话结果待确认，请勿重复开始")
	}
	sealed, err := g.seal(result.Room)
	if err != nil {
		return nil, err
	}
	s, err := scanStudio(g.db.QueryRowContext(work, `UPDATE digital_teacher_studio_sessions SET upstream_id=$2,room_sealed=CASE WHEN phase='creating' THEN $3 ELSE NULL END,phase=CASE WHEN phase='creating' THEN 'ready' ELSE phase END,deadline=NOW()+interval '105 seconds' WHERE id=$1 RETURNING `+studioColumns, id, result.ID, sealed))
	if err != nil {
		return nil, err
	}
	return g.studioTicket(s)
}

func parseStudioUsers(value string) map[int64]bool {
	out := map[int64]bool{}
	for _, v := range strings.Split(value, ",") {
		id, e := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if e == nil && id > 0 {
			out[id] = true
		}
	}
	return out
}
