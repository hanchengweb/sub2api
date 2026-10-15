package digitalteacher

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const anamAvatar = "edf6fdcb-acab-44b8-b974-ded72665ee26"
const anamVoice = "d5194111-d7d7-41dd-9f72-dea1a98876c7" // Richard (zh), Cartesia
const anamModel = "b4f89001-9638-4879-a9c3-02cc9f9f2004" // GPT 4.1
const anamExternalModel = "CUSTOMER_CLIENT_V1"
const anamSeconds = 90
const anamSDK = "https://windhub.online/v1/digital-teachers/anam-sdk"

type anamAsset struct {
	ID         string   `json:"id"`
	Name       string   `json:"displayName"`
	Private    *string  `json:"createdByOrganizationId"`
	Versions   []string `json:"availableVersions"`
	Image      string   `json:"sourceImageUrl"`
	Preview    string   `json:"previewSampleUrl"`
	Sample     string   `json:"sampleUrl"`
	Country    string   `json:"country"`
	Provider   string   `json:"provider"`
	Model      string   `json:"providerModelId"`
	Deprecated bool     `json:"isDeprecated"`
	Global     bool     `json:"isGlobal"`
}
type anamCatalogCache struct {
	sync.Mutex
	until                   time.Time
	avatars, voices, models []anamAsset
}

func (g *Gateway) providerForOwner(o Owner) string {
	if g.anamUsers[o.UserID] {
		return "anam"
	}
	return g.provider
}
func (g *Gateway) configuredForOwner(o Owner) bool {
	if g.providerForOwner(o) == "anam" {
		return g.anamKey != ""
	}
	return g.Configured()
}
func (g *Gateway) anamCall(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var payload []byte
	if body != nil {
		var e error
		payload, e = json.Marshal(body)
		if e != nil {
			return nil, e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, g.anamURL+path, bytes.NewReader(payload))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+g.anamKey)
	req.Header.Set("Content-Type", "application/json")
	res, e := g.client.Do(req)
	if e != nil {
		return nil, errors.New("Anam response unknown")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &providerError{res.StatusCode}
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if e != nil || !json.Valid(raw) {
		return nil, errors.New("Anam result unknown")
	}
	return raw, nil
}
func (g *Gateway) anamList(ctx context.Context, kind string) ([]anamAsset, error) {
	all := []anamAsset{}
	for page := 1; page <= 30; page++ {
		raw, e := g.anamCall(ctx, "GET", fmt.Sprintf("/v1/%s?page=%d&perPage=100", kind, page), nil)
		if e != nil {
			return nil, e
		}
		var data struct {
			Data []anamAsset `json:"data"`
			Meta struct {
				Next  *int `json:"next"`
				Total int  `json:"total"`
			} `json:"meta"`
		}
		if json.Unmarshal(raw, &data) != nil || data.Data == nil {
			return nil, errors.New("Anam catalog invalid")
		}
		all = append(all, data.Data...)
		if data.Meta.Next == nil && len(all) >= data.Meta.Total {
			return all, nil
		}
	}
	return nil, errors.New("Anam catalog too large")
}
func anamMediaURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || len(raw) > 4096 {
		return ""
	}
	switch u.Host {
	case "lab.anam.ai", "newgxnc1uqs0jnqm.public.blob.vercel-storage.com", "rdy2zdlvcqjo0egm.public.blob.vercel-storage.com":
		return raw
	}
	return ""
}
func (g *Gateway) anamCatalog(ctx context.Context, o Owner) (Catalog, error) {
	out := Catalog{Avatars: []map[string]any{}, Voices: []map[string]any{}, ModelLabels: map[string]string{}, VoiceModels: []string{"cara-4"}}
	if _, e := g.requirePlan(ctx, o, false); e != nil {
		return out, e
	}
	c := &g.anamCache
	c.Lock()
	if !g.now().Before(c.until) {
		av, e := g.anamList(ctx, "avatars")
		if e != nil {
			c.Unlock()
			return out, internalError(e)
		}
		vo, e := g.anamList(ctx, "voices")
		if e != nil {
			c.Unlock()
			return out, internalError(e)
		}
		mo, e := g.anamList(ctx, "llms")
		if e != nil {
			c.Unlock()
			return out, internalError(e)
		}
		c.avatars, c.voices, c.models, c.until = av, vo, mo, g.now().Add(10*time.Minute)
	}
	av, vo, mo := c.avatars, c.voices, c.models
	c.Unlock()
	grants := map[string]bool{}
	rows, e := g.db.QueryContext(ctx, `SELECT kind,asset_id::text FROM digital_teacher_assets WHERE user_id=$1 AND provider='anam'`, o.UserID)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var kind, id string
		if e = rows.Scan(&kind, &id); e != nil {
			rows.Close()
			return out, e
		}
		grants[kind+":"+id] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, a := range av {
		if !validUUID(a.ID) || a.Private != nil && !grants["avatar:"+a.ID] {
			continue
		}
		out.Avatars = append(out.Avatars, map[string]any{"id": a.ID, "name": a.Name, "custom": a.Private != nil, "ready": contains(a.Versions, "cara-4"), "previewUrl": anamMediaURL(a.Image)})
	}
	sort.SliceStable(out.Avatars, func(i, j int) bool { return out.Avatars[i]["custom"] == true && out.Avatars[j]["custom"] != true })
	for _, v := range vo {
		if !validUUID(v.ID) || v.Private != nil && !grants["voice:"+v.ID] {
			continue
		}
		language := v.Country
		if language == "CN" {
			language = "中文 · Chinese"
		}
		preview := v.Preview
		if preview == "" {
			preview = v.Sample
		}
		out.Voices = append(out.Voices, map[string]any{"id": v.ID, "name": v.Name, "language": language, "description": language + " · " + v.Provider + " · " + v.Model, "previewUrl": anamMediaURL(preview)})
	}
	sort.SliceStable(out.Voices, func(i, j int) bool {
		return out.Voices[i]["language"] == "中文 · Chinese" && out.Voices[j]["language"] != "中文 · Chinese"
	})
	for _, m := range mo {
		if validUUID(m.ID) && m.Global && !m.Deprecated {
			out.Models = append(out.Models, m.ID)
			out.ModelLabels[m.ID] = m.Name
		}
	}
	sort.SliceStable(out.Models, func(i, j int) bool { return out.Models[i] == anamModel && out.Models[j] != anamModel })
	out.Models = append(out.Models, anamExternalModel)
	out.ModelLabels[anamExternalModel] = "微末智学智能体"
	out.Jobs, e = g.AvatarJobs(ctx, o)
	return out, e
}

func anamBody(id string, t Teacher) map[string]any {
	persona := map[string]any{"name": t.Name, "avatarId": t.AvatarID, "avatarModel": "cara-4", "voiceId": t.VoiceID, "llmId": t.LLMModel, "languageCode": "zh", "maxSessionLengthSeconds": anamSeconds, "voiceDetectionOptions": map[string]any{"sttProvider": "anam"}}
	// External mode sends neither a teaching prompt nor a second automatic
	// greeting. The client speaks the opening once, then its own agent's answers.
	if t.LLMModel != anamExternalModel {
		persona["systemPrompt"] = "你是" + t.Name + "，教授" + t.Subject + "。面向成年学生，使用自然中文交流，每次回答简洁、适合语音播放。\n" + t.Instructions
		persona["initialMessage"] = t.Opening
	}
	return map[string]any{"clientLabel": "wxm-anam-" + id, "personaConfig": persona, "sessionOptions": map[string]any{"videoWidth": 1152, "videoHeight": 768, "videoQuality": "high"}}
}

func validAnamEngine(raw []byte) bool {
	var r struct {
		ID       string          `json:"sessionId"`
		Host     string          `json:"engineHost"`
		Protocol string          `json:"engineProtocol"`
		Signal   string          `json:"signallingEndpoint"`
		Config   json.RawMessage `json:"clientConfig"`
	}
	if json.Unmarshal(raw, &r) != nil || !validUUID(r.ID) || r.Protocol != "https" || len(r.Config) == 0 || !strings.HasPrefix(r.Signal, "/") || strings.HasPrefix(r.Signal, "//") {
		return false
	}
	u, e := url.Parse("https://" + r.Host)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(r.Host) > 2048 {
		return false
	}
	return (u.Host == "connect.anam.ai" || u.Host == "connect-eu.anam.ai" || u.Host == "connect-us.anam.ai") && (u.Path == "" || strings.HasPrefix(u.EscapedPath(), "/v1/webrtc/engines/"))
}
func (g *Gateway) startAnam(ctx context.Context, id string, t Teacher) (map[string]any, error) {
	raw, e := g.anamCall(ctx, "POST", "/v1/engine/session", anamBody(id, t))
	if e != nil {
		var pe *providerError
		if errors.As(e, &pe) && pe.status >= 400 && pe.status < 500 && pe.status != 408 {
			_, _ = g.db.ExecContext(ctx, `UPDATE digital_teacher_studio_sessions SET phase='failed',closed_at=NOW() WHERE id=$1`, id)
			if pe.status == 402 || pe.status == 429 {
				return nil, fail(429, "anam_limit", "Anam 当前额度、并发或服务容量不足，请稍后重试或查看账户额度；不会自动升级套餐")
			}
		}
		return nil, internalError(e)
	}
	var r struct {
		ID string `json:"sessionId"`
	}
	_ = json.Unmarshal(raw, &r)
	if !validUUID(r.ID) {
		return nil, fail(503, "anam_unknown", "会话创建结果待核对，请勿重复开始")
	}
	if !validAnamEngine(raw) {
		_, _ = g.anamCall(ctx, "POST", "/v1/sessions/"+r.ID+"/stop", map[string]any{})
		_, _ = g.db.ExecContext(ctx, `UPDATE digital_teacher_studio_sessions SET upstream_id=$2,phase='closing' WHERE id=$1`, id, r.ID)
		return nil, fail(503, "anam_engine_invalid", "服务方视频地址未获确认，正在结束会话")
	}
	sealed, e := g.seal(string(raw))
	if e != nil {
		return nil, e
	}
	s, e := scanStudio(g.db.QueryRowContext(ctx, `UPDATE digital_teacher_studio_sessions SET upstream_id=$2,room_sealed=CASE WHEN phase='creating' THEN $3 ELSE NULL END,phase=CASE WHEN phase='creating' THEN 'ready' ELSE phase END,deadline=NOW()+interval '135 seconds' WHERE id=$1 RETURNING `+studioColumns, id, r.ID, sealed))
	if e != nil {
		return nil, e
	}
	if s.Phase != "ready" {
		_, _ = g.anamCall(ctx, "POST", "/v1/sessions/"+r.ID+"/stop", map[string]any{})
		return nil, ErrConflict
	}
	return g.studioTicket(s, t.LLMModel == anamExternalModel)
}

// This capability can only read one already-created engine session. The one-hour
// Anam token and API key never leave WindHub; SDK retries cannot create sessions.
func (g *Gateway) anamCapability(s StudioSession) string {
	payload, _ := json.Marshal(map[string]any{"sub": s.ID, "exp": s.Deadline.Unix(), "aud": "windhub-anam", "type": "ephemeral"})
	body := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." + base64.RawURLEncoding.EncodeToString(payload)
	m := hmac.New(sha256.New, g.secret)
	m.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func (g *Gateway) AnamSDK(ctx context.Context, token string) (json.RawMessage, error) {
	p := strings.Split(token, ".")
	if len(token) > 1024 || len(p) != 3 {
		return nil, ErrNotFound
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[2])
	m := hmac.New(sha256.New, g.secret)
	m.Write([]byte(p[0] + "." + p[1]))
	if e != nil || !hmac.Equal(sig, m.Sum(nil)) {
		return nil, ErrNotFound
	}
	payload, e := base64.RawURLEncoding.DecodeString(p[1])
	var c struct {
		ID       string `json:"sub"`
		Audience string `json:"aud"`
		Expires  int64  `json:"exp"`
	}
	if e != nil || json.Unmarshal(payload, &c) != nil || !validUUID(c.ID) || c.Audience != "windhub-anam" || g.now().Unix() >= c.Expires {
		return nil, ErrNotFound
	}
	var o Owner
	e = g.db.QueryRowContext(ctx, `SELECT s.user_id,s.api_key_id,k.group_id FROM digital_teacher_studio_sessions s JOIN api_keys k ON k.id=s.api_key_id WHERE s.id=$1`, c.ID).Scan(&o.UserID, &o.KeyID, &o.GroupID)
	if e != nil || g.providerForOwner(o) != "anam" || !g.configuredForOwner(o) {
		return nil, ErrNotFound
	}
	var allowed bool
	if e = g.db.QueryRowContext(ctx, groupAccessSQL, o.UserID, o.GroupID).Scan(&allowed); e != nil || !allowed {
		return nil, ErrNotFound
	}
	// Re-check the owning account/key/grant after issuance. No caller-supplied config.
	s, e := scanStudio(g.db.QueryRowContext(ctx, `SELECT `+studioColumns+` FROM digital_teacher_studio_sessions WHERE id=$1 AND provider='anam' AND EXISTS(SELECT 1 FROM api_keys k JOIN users u ON u.id=k.user_id JOIN digital_teacher_plans p ON p.group_id=k.group_id WHERE k.id=api_key_id AND k.user_id=digital_teacher_studio_sessions.user_id AND k.status='active' AND k.deleted_at IS NULL AND (k.expires_at IS NULL OR k.expires_at>NOW()) AND u.status='active' AND u.deleted_at IS NULL AND p.enabled)`, c.ID))
	if e != nil || s.Phase != "ready" || s.ClosedAt != nil || !g.now().Before(s.Deadline) {
		return nil, ErrNotFound
	}
	raw, e := g.unseal(s.room)
	if e != nil || !validAnamEngine([]byte(raw)) {
		return nil, ErrNotFound
	}
	return json.RawMessage(raw), nil
}

type anamReceipt struct {
	ID     string     `json:"id"`
	Label  string     `json:"clientLabel"`
	End    *time.Time `json:"endTime"`
	Millis *float64   `json:"sessionLengthMs"`
}

func (g *Gateway) reconcileAnamOne(ctx context.Context, s StudioSession) error {
	if s.ClosedAt != nil {
		return nil
	}
	// Exact clientLabel recovers a dropped create response; never match by time.
	if s.upstream == "" {
		if g.now().Sub(s.CreatedAt) < 65*time.Second {
			return nil
		}
		raw, e := g.anamCall(ctx, "GET", "/v1/sessions?perPage=100&search="+url.QueryEscape("wxm-anam-"+s.ID), nil)
		if e != nil {
			return e
		}
		var list struct {
			Data []anamReceipt `json:"data"`
		}
		if json.Unmarshal(raw, &list) != nil {
			return errors.New("Anam recovery invalid")
		}
		for _, r := range list.Data {
			if r.Label == "wxm-anam-"+s.ID && validUUID(r.ID) {
				if s.upstream != "" {
					return errors.New("Anam recovery ambiguous")
				}
				s.upstream = r.ID
			}
		}
		if s.upstream == "" {
			return nil
		}
		_, e = g.db.ExecContext(ctx, `UPDATE digital_teacher_studio_sessions SET upstream_id=$2,phase='closing',room_sealed=NULL WHERE id=$1`, s.ID, s.upstream)
		if e != nil {
			return e
		}
		s.Phase = "closing"
	}
	if s.Phase == "closing" || !g.now().Before(s.Deadline) {
		_, _ = g.anamCall(ctx, "POST", "/v1/sessions/"+s.upstream+"/stop", map[string]any{})
	}
	raw, e := g.anamCall(ctx, "GET", "/v1/sessions/"+s.upstream, nil)
	if e != nil {
		return e
	}
	var r anamReceipt
	if json.Unmarshal(raw, &r) != nil || r.ID != s.upstream || r.Label != "wxm-anam-"+s.ID {
		return errors.New("Anam receipt mismatch")
	}
	if r.End == nil || r.Millis == nil || *r.Millis < 0 || *r.Millis > 86400000 {
		return nil
	}
	_, e = g.db.ExecContext(ctx, `UPDATE digital_teacher_studio_sessions SET phase='closed',closed_at=$2,duration_seconds=$3,room_sealed=NULL WHERE id=$1 AND closed_at IS NULL`, s.ID, *r.End, *r.Millis/1000)
	return e
}
func (g *Gateway) reconcileAnam(ctx context.Context) {
	if g.anamKey == "" {
		return
	}
	rows, e := g.db.QueryContext(ctx, `UPDATE digital_teacher_studio_sessions SET checked_at=NOW() WHERE id IN (SELECT id FROM digital_teacher_studio_sessions WHERE provider='anam' AND closed_at IS NULL AND (checked_at IS NULL OR checked_at<NOW()-interval '8 seconds') ORDER BY checked_at NULLS FIRST LIMIT 5 FOR UPDATE SKIP LOCKED) RETURNING `+studioColumns)
	if e != nil {
		return
	}
	all := []StudioSession{}
	for rows.Next() {
		s, e := scanStudio(rows)
		if e == nil {
			all = append(all, s)
		}
	}
	rows.Close()
	for _, s := range all {
		_ = g.reconcileAnamOne(ctx, s)
	}
}
