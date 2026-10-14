package digitalteacher

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type providerError struct{ status int }

func (e *providerError) Error() string {
	return fmt.Sprintf("LiveAvatar request failed (HTTP %d)", e.status)
}
func (g *Gateway) call(ctx context.Context, method, path, token string, body any) (json.RawMessage, error) {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, g.upstream+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.Header.Set("X-API-KEY", g.key)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, errors.New("LiveAvatar response unknown")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &providerError{resp.StatusCode}
	}
	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope)
	if err != nil || envelope.Code != 1000 || len(envelope.Data) == 0 {
		return nil, errors.New("LiveAvatar response not confirmed")
	}
	return envelope.Data, nil
}

type Asset struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Language     string `json:"language,omitempty"`
	Status       string `json:"status,omitempty"`
	Expired      bool   `json:"is_expired,omitempty"`
	Preview      string `json:"preview_url,omitempty"`
	DefaultVoice *Asset `json:"default_voice,omitempty"`
}
type Catalog struct {
	Models      []string         `json:"models,omitempty"`
	VoiceModels []string         `json:"voiceModels,omitempty"`
	Avatars     []map[string]any `json:"avatars"`
	Voices      []map[string]any `json:"voices"`
	Jobs        []AvatarJob      `json:"jobs,omitempty"`
}

func (g *Gateway) Teachers(ctx context.Context, o Owner) ([]Teacher, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT config FROM digital_teachers WHERE user_id=$1 ORDER BY updated_at DESC LIMIT 30`, o.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Teacher{}
	for rows.Next() {
		var data []byte
		var t Teacher
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		out = append(out, g.teacherForOwner(t, o))
	}
	return out, rows.Err()
}

func (g *Gateway) list(ctx context.Context, path string) ([]Asset, error) {
	var all []Asset
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for page := 1; page <= 20; page++ {
		data, err := g.call(ctx, "GET", fmt.Sprintf("%s%spage=%d&page_size=100", path, sep, page), "", nil)
		if err != nil {
			return nil, err
		}
		var result struct {
			Results []Asset         `json:"results"`
			Next    json.RawMessage `json:"next"`
		}
		if json.Unmarshal(data, &result) != nil || result.Results == nil {
			return nil, errors.New("invalid provider catalog")
		}
		all = append(all, result.Results...)
		if len(result.Next) == 0 || string(result.Next) == "null" || string(result.Next) == "false" || string(result.Next) == "\"\"" {
			return all, nil
		}
	}
	return nil, errors.New("provider catalog too large")
}
func (g *Gateway) Catalog(ctx context.Context, o Owner) (Catalog, error) {
	if g.provider == "simli" {
		return g.simliCatalog(ctx, o)
	}
	out := Catalog{Avatars: []map[string]any{}, Voices: []map[string]any{}}
	p, err := g.Plan(ctx, o.GroupID)
	if err != nil {
		return out, err
	}
	if !g.Configured() || !p.Enabled {
		return out, fail(403, "digital_teacher_not_enabled", "当前算力包尚未开通数字老师")
	}
	avatars, err := g.list(ctx, "/v1/avatars/public")
	if err != nil {
		return out, internalError(err)
	}
	voices, err := g.list(ctx, "/v1/voices?voice_type=public")
	if err != nil {
		return out, internalError(err)
	}
	custom := map[string]bool{}
	rows, err := g.db.QueryContext(ctx, `SELECT kind,asset_id::text FROM digital_teacher_assets WHERE user_id=$1 AND provider='liveavatar'`, o.UserID)
	if err != nil {
		return out, err
	}
	var grants [][2]string
	for rows.Next() {
		var kind, id string
		if err = rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return out, err
		}
		grants = append(grants, [2]string{kind, id})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, grant := range grants {
		data, e := g.call(ctx, "GET", "/v1/"+grant[0]+"s/"+grant[1], "", nil)
		if e != nil {
			return out, internalError(e)
		}
		var a Asset
		if json.Unmarshal(data, &a) != nil || a.ID != grant[1] {
			return out, fail(503, "digital_teacher_asset_unavailable", "已分配的老师素材暂不可用")
		}
		if grant[0] == "avatar" {
			avatars = append(avatars, a)
			custom[a.ID] = true
		} else {
			voices = append(voices, a)
		}
	}
	seen := map[string]bool{}
	for _, a := range avatars {
		if !validUUID(a.ID) || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		voice := ""
		if a.DefaultVoice != nil {
			voice = a.DefaultVoice.ID
		}
		preview := ""
		if strings.HasPrefix(a.Preview, "https://") {
			preview = a.Preview
		}
		out.Avatars = append(out.Avatars, map[string]any{"id": a.ID, "name": a.Name, "custom": custom[a.ID], "ready": a.Status == "ACTIVE" && !a.Expired, "status": a.Status, "previewUrl": preview, "voiceId": voice})
	}
	seen = map[string]bool{}
	for _, v := range voices {
		if !validUUID(v.ID) || seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		out.Voices = append(out.Voices, map[string]any{"id": v.ID, "name": v.Name, "language": v.Language})
	}
	return out, nil
}
func (g *Gateway) SaveTeacher(ctx context.Context, o Owner, t Teacher) (Teacher, error) {
	if t.Provider == "" {
		t.Provider = g.provider
	}
	if t.Provider != g.provider {
		return t, fail(409, "teacher_provider_changed", "数字老师服务已切换，请刷新后选择当前形象")
	}
	if err := t.Validate(); err != nil {
		return t, err
	}
	catalog, err := g.Catalog(ctx, o)
	if err != nil {
		return t, err
	}
	avatar, voice := false, false
	for _, a := range catalog.Avatars {
		if a["id"] == t.AvatarID && a["ready"] == true {
			avatar = true
		}
	}
	for _, v := range catalog.Voices {
		if v["id"] == t.VoiceID {
			voice = true
		}
	}
	if !avatar || !voice {
		return t, fail(403, "teacher_asset_not_assigned", "请选择当前账户可用的老师形象和声音")
	}
	data, _ := json.Marshal(t)
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	var uid int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, o.UserID).Scan(&uid); err != nil {
		return t, err
	}
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM digital_teacher_sessions WHERE user_id=$1 AND settled_at IS NULL) OR EXISTS(SELECT 1 FROM digital_teachers WHERE user_id=$1 AND client_id=$2 AND context_pending)`, o.UserID, t.ID).Scan(&busy); err != nil {
		return t, err
	}
	if busy {
		return t, ErrConflict
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM digital_teachers WHERE user_id=$1 AND client_id<>$2`, o.UserID, t.ID).Scan(&count); err != nil {
		return t, err
	}
	if count >= 30 {
		return t, fail(400, "too_many_teachers", "每个账户最多保存 30 位老师")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO digital_teachers(id,user_id,client_id,config) VALUES(gen_random_uuid(),$1,$2,$3) ON CONFLICT(user_id,client_id) DO UPDATE SET config=EXCLUDED.config,updated_at=NOW()`, o.UserID, t.ID, data)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}
func (g *Gateway) teacher(ctx context.Context, o Owner, id string) (string, Teacher, error) {
	var serverID string
	var data []byte
	var t Teacher
	err := g.db.QueryRowContext(ctx, `SELECT id::text,config FROM digital_teachers WHERE user_id=$1 AND client_id=$2`, o.UserID, id).Scan(&serverID, &data)
	if err != nil {
		return "", t, ErrNotFound
	}
	err = json.Unmarshal(data, &t)
	return serverID, g.teacherForOwner(t, o), err
}
func (g *Gateway) context(ctx context.Context, id string, t Teacher) (string, error) {
	body := map[string]any{"prompt": "你是" + t.Name + "，教授" + t.Subject + "。使用自然、简洁的中文交流，每次解释一个重点，适当提问确认学生理解。\n" + t.Instructions, "opening_text": t.Opening}
	hash := digest(body)
	body["name"] = "wh-teacher-" + id + "-" + hash[:16]
	var contextID, oldHash sql.NullString
	var pending bool
	if err := g.db.QueryRowContext(ctx, `SELECT context_id::text,context_hash,context_pending FROM digital_teachers WHERE id=$1`, id).Scan(&contextID, &oldHash, &pending); err != nil {
		return "", err
	}
	if oldHash.String == hash && contextID.Valid && !pending {
		return contextID.String, nil
	}
	// A context create can succeed despite a dropped response. Recover by its
	// stable server-generated name; never publish account-wide context contents.
	if pending {
		for page := 1; page <= 20; page++ {
			data, err := g.call(ctx, "GET", fmt.Sprintf("/v1/contexts?page=%d&page_size=100", page), "", nil)
			if err != nil {
				return "", err
			}
			var list struct {
				Results []struct{ ID, Name string } `json:"results"`
				Next    json.RawMessage             `json:"next"`
			}
			if json.Unmarshal(data, &list) != nil {
				return "", errors.New("context lookup failed")
			}
			for _, v := range list.Results {
				if v.Name == body["name"] && validUUID(v.ID) {
					_, err = g.db.ExecContext(ctx, `UPDATE digital_teachers SET context_id=$2,context_hash=$3,context_pending=FALSE WHERE id=$1`, id, v.ID, hash)
					return v.ID, err
				}
			}
			if len(list.Next) == 0 || string(list.Next) == "null" || string(list.Next) == "false" {
				break
			}
		}
		return "", fail(409, "teacher_context_pending", "老师设定的保存结果待核对，请稍后重试")
	}
	if _, err := g.db.ExecContext(ctx, `UPDATE digital_teachers SET context_pending=TRUE,context_hash=$2 WHERE id=$1`, id, hash); err != nil {
		return "", err
	}
	data, err := g.call(ctx, "POST", "/v1/contexts", "", body)
	if err != nil {
		var pe *providerError
		if errors.As(err, &pe) && pe.status >= 400 && pe.status < 500 {
			_, _ = g.db.ExecContext(ctx, `UPDATE digital_teachers SET context_pending=FALSE WHERE id=$1`, id)
		}
		return "", err
	}
	var result struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &result) != nil || !validUUID(result.ID) {
		return "", errors.New("context result unknown")
	}
	_, err = g.db.ExecContext(ctx, `UPDATE digital_teachers SET context_id=$2,context_hash=$3,context_pending=FALSE WHERE id=$1`, id, result.ID, hash)
	return result.ID, err
}
