package digitalteacher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/speechvoices"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

const SimliAvatar = "cace3ef7-a4c4-425d-a8cf-a5358eb0c427"
const SimliVoice = "warm"

// Keep existing lesson settings when switching the supplier. Supplier-specific
// likenesses are never silently presented as an equivalent cloned person.
func (g *Gateway) currentTeacher(t Teacher) Teacher {
	if g.provider == "simli" && t.Provider != "simli" {
		t.Provider, t.AvatarID, t.VoiceID = "simli", SimliAvatar, SimliVoice
	}
	if t.Provider == "simli" && t.VoiceID == "windows-zh-cn" {
		t.VoiceID = SimliVoice
	}
	return t
}

func (g *Gateway) simliCall(ctx context.Context, method, route string, body any) (json.RawMessage, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, g.simliURL+route, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("invalid Simli request")
	}
	req.Header.Set("x-simli-api-key", g.simliKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := g.client.Do(req)
	if err != nil {
		return nil, errors.New("Simli response unknown")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &providerError{res.StatusCode}
	}
	data, err = io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil || !json.Valid(data) {
		return nil, errors.New("invalid Simli response")
	}
	return data, nil
}

type simliFace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (g *Gateway) simliFaces(ctx context.Context) ([]simliFace, error) {
	data, err := g.simliCall(ctx, "GET", "/faces", nil)
	if err != nil {
		return nil, err
	}
	var faces []simliFace
	if json.Unmarshal(data, &faces) != nil {
		return nil, errors.New("invalid Simli face catalog")
	}
	return faces, nil
}
func (g *Gateway) simliCatalog(ctx context.Context, o Owner) (Catalog, error) {
	out := Catalog{Avatars: []map[string]any{}, Voices: []map[string]any{}}
	if _, err := g.requirePlan(ctx, o, false); err != nil {
		return out, err
	}
	for _, a := range simliPresets {
		out.Avatars = append(out.Avatars, map[string]any{"id": a.id, "name": a.name, "ready": true, "custom": false, "voiceId": a.voice, "previewUrl": "https://mintcdn.com/simli/NELbEX-teJCHwcnx/images/" + a.image})
	}
	for _, v := range speechvoices.Catalog {
		out.Voices = append(out.Voices, map[string]any{"id": v.ID, "name": v.Name, "language": v.Language, "description": v.Description})
	}
	if g.StudioEnabled(o) {
		out.Voices = g.studioVoices(ctx)
		if len(out.Voices) == 1 {
			out.VoiceNotice = "ElevenLabs 的完整声音列表暂时未加载成功，可以先用 Aria；稍后点击刷新恢复更多声音。"
		}
		out.Models = studioModels
		out.VoiceModels = studioVoiceModels
		for _, a := range out.Avatars {
			a["voiceId"] = studioVoice
		}
	}
	jobs, err := g.AvatarJobs(ctx, o)
	if err != nil {
		return out, err
	}
	out.Jobs = jobs
	ready := map[string]bool{}
	for _, j := range jobs {
		if j.State == "ready" {
			ready[j.FaceID] = true
			out.Avatars = append(out.Avatars, map[string]any{"id": j.FaceID, "name": j.Name, "ready": true, "custom": true, "voiceId": SimliVoice, "previewUrl": j.Preview})
		}
	}
	rows, err := g.db.QueryContext(ctx, `SELECT asset_id::text FROM digital_teacher_assets WHERE user_id=$1 AND caller_scope=$2 AND provider='simli' AND kind='avatar'`, o.UserID, o.Scope)
	if err != nil {
		return out, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		if !ready[id] {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	faces, err := g.simliFaces(ctx)
	if err != nil {
		return out, internalError(err)
	}
	for _, id := range ids {
		for _, face := range faces {
			if face.ID == id && id != SimliAvatar {
				name := face.Name
				if name == "" {
					name = "我的定制老师"
				}
				out.Avatars = append(out.Avatars, map[string]any{"id": id, "name": name, "ready": true, "custom": true, "voiceId": SimliVoice, "previewUrl": ""})
			}
		}
	}
	return out, nil
}
func (g *Gateway) bindSimliAsset(ctx context.Context, userID int64, kind, id string) error {
	if kind != "avatar" {
		return fail(400, "simli_voice_catalog", "请在应用中选择自然中文声音，只需绑定老师形象")
	}
	faces, err := g.simliFaces(ctx)
	if err != nil {
		return internalError(err)
	}
	found := false
	for _, face := range faces {
		if face.ID == id {
			found = true
		}
	}
	if !found {
		return fail(404, "simli_face_missing", "尚未在 Simli 账号中找到此形象，请先完成制作")
	}
	_, err = g.db.ExecContext(ctx, `INSERT INTO digital_teacher_assets(user_id,kind,asset_id,provider) VALUES($1,'avatar',$2,'simli') ON CONFLICT(user_id,caller_scope,kind,asset_id) DO UPDATE SET provider='simli'`, userID, id)
	return err
}

func (g *Gateway) createSimli(ctx context.Context, s *Session, t Teacher) (Ticket, error) {
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	// Recheck the assigned face before consuming supplier resources.
	catalog, err := g.simliCatalog(work, Owner{UserID: s.UserID, KeyID: s.KeyID, GroupID: s.GroupID})
	found := false
	for _, a := range catalog.Avatars {
		if a["id"] == t.AvatarID && a["ready"] == true {
			found = true
		}
	}
	if err != nil || !found {
		_ = g.complete(work, s, 0, 0, nil, true)
		return Ticket{}, fail(403, "simli_face_unavailable", "老师形象暂不可用，请重新选择当前账户的形象")
	}
	body := map[string]any{"faceId": t.AvatarID, "apiVersion": "v2", "sessionAggregator": s.ID, "handleSilence": true, "maxSessionLength": s.MaxSeconds, "maxIdleTime": 30, "audioInputFormat": "pcm16"}
	data, err := g.simliCall(work, "POST", "/compose/token", body)
	if err != nil {
		_ = g.complete(work, s, 0, 0, nil, true)
		return Ticket{}, internalError(err)
	}
	var result struct {
		Token string `json:"session_token"`
	}
	if json.Unmarshal(data, &result) != nil || len(result.Token) < 20 || len(result.Token) > 20000 || result.Token == "FAIL" {
		_ = g.complete(work, s, 0, 0, nil, true)
		return Ticket{}, fail(502, "simli_token_invalid", "Simli 未能创建会话，请稍后重试")
	}
	sealed, err := g.seal(result.Token)
	if err != nil {
		return Ticket{}, internalError(err)
	}
	// The provider token remains encrypted on the gateway. Only its single-use
	// WindHub capability is sent to the desktop; the gateway owns the websocket.
	_, err = g.db.ExecContext(work, `UPDATE digital_teacher_sessions SET upstream_id=id,upstream_token=$2,phase=CASE WHEN stop_requested THEN 'stopping' ELSE 'ready' END,updated_at=NOW() WHERE id=$1`, s.ID, sealed)
	if err != nil {
		return Ticket{}, internalError(err)
	}
	fresh, err := g.session(work, s.ID)
	if err != nil {
		return Ticket{}, internalError(err)
	}
	if fresh.StopRequested {
		_ = g.complete(work, fresh, 0, 0, nil, true)
		return Ticket{}, ErrConflict
	}
	return g.Ticket(fresh), nil
}

type simliHistory struct {
	ID         string   `json:"id"`
	Aggregator string   `json:"sessionAggregator"`
	Seconds    *float64 `json:"sessionTotalTime"`
	Start      *float64 `json:"startTime"`
	End        *float64 `json:"endTime"`
}

func simliReceipt(data json.RawMessage, s *Session) (float64, time.Time, json.RawMessage, error) {
	var history struct {
		Sessions []simliHistory `json:"sessions"`
	}
	if json.Unmarshal(data, &history) != nil {
		return 0, time.Time{}, nil, errors.New("invalid Simli history")
	}
	var matches []simliHistory
	for _, r := range history.Sessions {
		if r.Aggregator == s.ID {
			matches = append(matches, r)
		}
	}
	if len(matches) != 1 {
		return 0, time.Time{}, nil, errors.New("Simli session receipt is not unique or not ready")
	}
	r := matches[0]
	finite := func(n *float64) bool { return n != nil && !math.IsNaN(*n) && !math.IsInf(*n, 0) }
	if !validUUID(r.ID) || !finite(r.Seconds) || !finite(r.Start) || !finite(r.End) {
		return 0, time.Time{}, nil, errors.New("incomplete Simli usage")
	}
	seconds, start, end := *r.Seconds, *r.Start, *r.End
	if seconds < 0 || end <= 0 || end < start || end > 1e11 || start < float64(s.CreatedAt.Unix()-30) || seconds > end-start+5 {
		return 0, time.Time{}, nil, errors.New("invalid Simli usage")
	}
	ended := time.UnixMilli(int64(end * 1000)).UTC()
	safe, _ := json.Marshal(map[string]any{"provider": "simli", "session_id": r.ID, "session_aggregator": r.Aggregator, "duration": seconds, "end_at": ended, "supplier_financial_receipt": false})
	return seconds, ended, safe, nil
}
func (g *Gateway) reconcileSimli(ctx context.Context, s *Session) error {
	if s.StartRequested == nil {
		if s.StopRequested || g.now().After(s.Deadline) || g.now().Sub(s.CreatedAt) > 45*time.Second {
			return g.complete(ctx, s, 0, 0, nil, true)
		}
		return nil
	}
	data, err := g.simliCall(ctx, "GET", "/history/sessions?startTime="+url.QueryEscape(fmt.Sprint(s.CreatedAt.Add(-30*time.Second).Unix())), nil)
	if err != nil {
		return err
	}
	seconds, ended, safe, err := simliReceipt(data, s)
	if err != nil {
		return nil
	} // Unknown provider history never creates a refund.
	if ended.After(g.now()) {
		return nil
	}
	if s.ReceiptSeen != nil && sameReceipt(s.Receipt, safe) && g.now().Sub(*s.ReceiptSeen) >= 5*time.Second && g.now().Sub(ended) >= 15*time.Second {
		return g.complete(ctx, s, 0, seconds, safe, false)
	}
	_, err = g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET phase='settling',receipt=$2,receipt_seen_at=CASE WHEN receipt=$2::jsonb THEN receipt_seen_at ELSE NOW() END,updated_at=NOW() WHERE id=$1 AND settled_at IS NULL`, s.ID, safe)
	return err
}
