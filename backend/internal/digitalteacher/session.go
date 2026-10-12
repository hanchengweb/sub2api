package digitalteacher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"time"
)

// Digital teacher usage has its own minute plan but inherits group access.
const groupAccessSQL = `SELECT EXISTS(SELECT 1 FROM groups g WHERE g.id=$2 AND g.status='active' AND g.deleted_at IS NULL AND (CASE WHEN g.subscription_type='subscription' THEN EXISTS(SELECT 1 FROM user_subscriptions sub WHERE sub.user_id=$1 AND sub.group_id=g.id AND sub.status='active' AND sub.expires_at>NOW() AND sub.deleted_at IS NULL) ELSE NOT g.is_exclusive OR EXISTS(SELECT 1 FROM user_allowed_groups a WHERE a.user_id=$1 AND a.group_id=g.id) END))`

func (g *Gateway) Create(ctx context.Context, o Owner, req StartRequest) (Ticket, error) {
	if _, err := g.requirePlan(ctx, o, req.Sandbox); err != nil {
		return Ticket{}, err
	}
	var teacherID string
	var teacher Teacher
	var err error
	if !req.Sandbox {
		teacherID, teacher, err = g.teacher(ctx, o, req.TeacherID)
		if err != nil {
			return Ticket{}, err
		}
	}
	s, replay, err := g.reserve(ctx, o, req, teacherID, teacher)
	if err != nil {
		return Ticket{}, internalError(err)
	}
	if replay {
		if (s.Phase == "ready" || s.Phase == "running") && !s.StopRequested && g.now().Before(s.Deadline) {
			return g.Ticket(s), nil
		}
		return Ticket{}, ErrConflict
	}
	// Finish recording the dispatched attempt even if the caller disconnects.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 55*time.Second)
	defer cancel()
	body := map[string]any{"mode": "FULL", "avatar_id": SandboxAvatar, "is_sandbox": req.Sandbox, "max_session_duration": s.MaxSeconds, "video_settings": map[string]any{"quality": "medium", "encoding": "H264"}, "interactivity_type": "CONVERSATIONAL", "avatar_persona": map[string]any{"language": "zh"}}
	if !req.Sandbox {
		contextID, e := g.context(work, teacherID, teacher)
		if e != nil {
			if release := g.complete(work, s, 0, 0, nil, true); release != nil {
				return Ticket{}, internalError(release)
			}
			return Ticket{}, internalError(e)
		}
		body["avatar_id"] = teacher.AvatarID
		body["avatar_persona"] = map[string]any{"voice_id": teacher.VoiceID, "context_id": contextID, "language": "zh"}
	}
	var stopped bool
	if err = g.db.QueryRowContext(work, `SELECT stop_requested FROM digital_teacher_sessions WHERE id=$1`, s.ID).Scan(&stopped); err != nil {
		return Ticket{}, internalError(err)
	}
	if stopped {
		err = g.complete(work, s, 0, 0, nil, true)
		if err != nil {
			return Ticket{}, internalError(err)
		}
		return Ticket{}, ErrConflict
	}
	data, err := g.call(work, "POST", "/v1/sessions/token", "", body)
	if err != nil {
		var pe *providerError
		if errors.As(err, &pe) && pe.status >= 400 && pe.status < 500 {
			if e := g.complete(work, s, 0, 0, nil, true); e != nil {
				return Ticket{}, internalError(e)
			}
		} else {
			_ = g.markReview(work, s.ID, "token_result_unknown")
		}
		return Ticket{}, internalError(err)
	}
	var upstream struct {
		ID    string `json:"session_id"`
		Token string `json:"session_token"`
	}
	if json.Unmarshal(data, &upstream) != nil || !validUUID(upstream.ID) || len(upstream.Token) < 20 {
		_ = g.markReview(work, s.ID, "token_result_invalid")
		return Ticket{}, fail(502, "digital_teacher_session_unknown", "会话创建结果待核对，请勿重复开始")
	}
	encrypted, err := g.seal(upstream.Token)
	if err != nil {
		return Ticket{}, internalError(err)
	}
	_, err = g.db.ExecContext(work, `UPDATE digital_teacher_sessions SET upstream_id=$2,upstream_token=$3,phase=CASE WHEN stop_requested THEN 'stopping' ELSE 'ready' END,updated_at=NOW() WHERE id=$1`, s.ID, upstream.ID, encrypted)
	if err != nil {
		_, _ = g.call(work, "POST", "/v1/sessions/stop", "", map[string]any{"session_id": upstream.ID, "reason": "USER_CLOSED"})
		return Ticket{}, internalError(err)
	}
	s, err = g.session(work, s.ID)
	if err != nil {
		return Ticket{}, internalError(err)
	}
	if s.StopRequested {
		_, _ = g.Stop(work, o, s.ID)
		return Ticket{}, ErrConflict
	}
	return g.Ticket(s), nil
}

func (g *Gateway) allowedToStart(ctx context.Context, s *Session) bool {
	var allowed bool
	err := g.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_keys k JOIN users u ON u.id=k.user_id JOIN groups g ON g.id=k.group_id JOIN digital_teacher_plans p ON p.group_id=g.id WHERE k.id=$1 AND k.user_id=$2 AND k.group_id=$3 AND k.deleted_at IS NULL AND k.status IN ('active','quota_exhausted') AND (k.expires_at IS NULL OR k.expires_at>NOW()) AND u.status='active' AND u.deleted_at IS NULL AND g.status='active' AND g.deleted_at IS NULL AND p.enabled AND (NOT $4 OR p.sandbox_enabled))`, s.KeyID, s.UserID, s.GroupID, s.Sandbox).Scan(&allowed)
	if err != nil || !allowed {
		return false
	}
	err = g.db.QueryRowContext(ctx, groupAccessSQL, s.UserID, s.GroupID).Scan(&allowed)
	return err == nil && allowed
}
func (g *Gateway) SDKStart(ctx context.Context, s *Session) (json.RawMessage, error) {
	if !g.allowedToStart(ctx, s) || s.StopRequested || !g.now().Before(s.Deadline) {
		return nil, fail(403, "session_cannot_start", "此会话已结束、超时或权限已失效")
	}
	if s.Phase == "running" && s.StartResponse != "" {
		plain, err := g.unseal(s.StartResponse)
		return json.RawMessage(plain), internalOrNil(err)
	}
	result, err := g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET phase='starting',start_requested_at=NOW(),updated_at=NOW() WHERE id=$1 AND phase='ready' AND NOT stop_requested AND deadline>NOW() AND created_at>NOW()-INTERVAL '45 seconds'`, s.ID)
	if err != nil {
		return nil, internalError(err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return nil, ErrConflict
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	// After dispatch begins, every uncertain response is stopped and reconciled.
	started := false
	defer func() {
		if !started {
			_, _ = g.db.ExecContext(work, `UPDATE digital_teacher_sessions SET stop_requested=TRUE,phase='stopping',updated_at=NOW() WHERE id=$1 AND settled_at IS NULL`, s.ID)
		}
	}()
	token, err := g.unseal(s.UpstreamToken)
	if err != nil {
		_ = g.markReview(work, s.ID, "session_token_unreadable")
		return nil, internalError(err)
	}
	data, err := g.call(work, "POST", "/v1/sessions/start", token, nil)
	if err != nil {
		_, _ = g.db.ExecContext(work, `UPDATE digital_teacher_sessions SET phase='stopping',stop_requested=TRUE,error_code='start_result_unknown',updated_at=NOW() WHERE id=$1`, s.ID)
		return nil, internalError(err)
	}
	var details struct {
		ID          string `json:"session_id"`
		URL         string `json:"livekit_url"`
		ClientToken string `json:"livekit_client_token"`
		Max         int    `json:"max_session_duration"`
	}
	if json.Unmarshal(data, &details) != nil || details.ID != s.UpstreamID || details.ClientToken == "" {
		_, _ = g.db.ExecContext(work, `UPDATE digital_teacher_sessions SET phase='stopping',stop_requested=TRUE,error_code='start_result_invalid' WHERE id=$1`, s.ID)
		return nil, fail(502, "invalid_session_response", "视频连接尚未确认，后台将结束此会话")
	}
	u, e := url.Parse(details.URL)
	if e != nil || u.Host == "" || (u.Scheme != "wss" && u.Scheme != "https") {
		return nil, fail(502, "invalid_video_address", "服务返回的视频连接地址无效")
	}
	// Return only the client room credential. The provider session JWT and agent
	// token never leave the gateway; replay cannot create a second upstream call.
	safe, _ := json.Marshal(map[string]any{"session_id": s.UpstreamID, "livekit_url": details.URL, "livekit_client_token": details.ClientToken, "max_session_duration": s.MaxSeconds})
	encrypted, err := g.seal(string(safe))
	if err != nil {
		return nil, internalError(err)
	}
	_, err = g.db.ExecContext(work, `UPDATE digital_teacher_sessions SET start_response=$2,phase=CASE WHEN stop_requested THEN 'stopping' ELSE 'running' END,deadline=LEAST(deadline,NOW()+max_seconds*INTERVAL '1 second'),updated_at=NOW() WHERE id=$1`, s.ID, encrypted)
	if err != nil {
		return nil, internalError(err)
	}
	fresh, err := g.session(work, s.ID)
	if err != nil {
		return nil, internalError(err)
	}
	if fresh.StopRequested {
		_, _ = g.Stop(work, Owner{s.UserID, s.KeyID, s.GroupID}, s.ID)
		return nil, ErrConflict
	}
	started = true
	return safe, nil
}
func internalOrNil(err error) error {
	if err == nil {
		return nil
	}
	return internalError(err)
}
func (g *Gateway) SDKKeepAlive(ctx context.Context, s *Session) error {
	if s.Phase != "running" || s.StopRequested || !g.now().Before(s.Deadline) || !g.allowedToStart(ctx, s) {
		return fail(409, "session_has_ended", "此会话已结束或到达时长上限")
	}
	token, err := g.unseal(s.UpstreamToken)
	if err != nil {
		return internalError(err)
	}
	_, err = g.call(ctx, "POST", "/v1/sessions/keep-alive", token, nil)
	return internalOrNil(err)
}
func (g *Gateway) Stop(ctx context.Context, o Owner, id string) (map[string]any, error) {
	s, err := g.owned(ctx, o, id)
	if err != nil {
		return nil, err
	}
	if s.SettledAt != nil {
		return map[string]any{"stopped": true, "settlement_pending": false}, nil
	}
	_, err = g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET stop_requested=TRUE,phase=CASE WHEN phase IN ('ready','running','review') THEN 'stopping' ELSE phase END,updated_at=NOW() WHERE id=$1 AND settled_at IS NULL`, id)
	if err != nil {
		return nil, internalError(err)
	}
	if s.UpstreamID == "" {
		return map[string]any{"stopped": false, "settlement_pending": true}, nil
	}
	if err = g.reconcileOne(ctx, s.ID, true); err != nil {
		return nil, internalError(err)
	}
	fresh, err := g.session(ctx, id)
	if err != nil {
		return nil, internalError(err)
	}
	ended := fresh.Phase == "settling" || fresh.SettledAt != nil
	return map[string]any{"stopped": ended, "settlement_pending": fresh.SettledAt == nil, "state": fresh.Phase}, nil
}

func number(raw json.RawMessage) (float64, error) {
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.Float64()
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strconv.ParseFloat(s, 64)
	}
	return 0, errors.New("usage is missing")
}
func receipt(data json.RawMessage) (float64, float64, time.Time, json.RawMessage, error) {
	var r struct {
		Credits json.RawMessage `json:"credits_consumed"`
		Seconds json.RawMessage `json:"duration"`
		End     string          `json:"end_at"`
		Mode    string          `json:"mode"`
		Sandbox bool            `json:"is_sandbox"`
	}
	if json.Unmarshal(data, &r) != nil || r.End == "" {
		return 0, 0, time.Time{}, nil, errors.New("session is not confirmed ended")
	}
	ended, err := time.Parse(time.RFC3339Nano, r.End)
	if err != nil {
		ended, err = time.ParseInLocation("2006-01-02T15:04:05.999999999", r.End, time.UTC)
	}
	if err != nil || ended.IsZero() {
		return 0, 0, time.Time{}, nil, errors.New("invalid provider end time")
	}
	credits, err := number(r.Credits)
	if err != nil {
		return 0, 0, time.Time{}, nil, err
	}
	seconds, err := number(r.Seconds)
	if err != nil {
		return 0, 0, time.Time{}, nil, err
	}
	if credits < 0 || seconds < 0 || math.IsNaN(credits) || math.IsInf(credits, 0) || math.IsNaN(seconds) || math.IsInf(seconds, 0) || r.Mode != "FULL" {
		return 0, 0, time.Time{}, nil, errors.New("invalid provider usage")
	}
	safe, _ := json.Marshal(map[string]any{"credits_consumed": credits, "duration": seconds, "end_at": ended, "mode": r.Mode, "is_sandbox": r.Sandbox})
	return credits, seconds, ended, safe, nil
}
func (g *Gateway) reconcileOne(ctx context.Context, id string, requestStop bool) error {
	s, err := g.session(ctx, id)
	if err != nil {
		return err
	}
	if s.SettledAt != nil {
		return nil
	}
	if s.UpstreamID == "" {
		if g.now().After(s.Deadline) {
			// A provider token is never sent to the browser. Without a recorded
			// upstream ID, no start request could have been dispatched.
			return g.complete(ctx, s, 0, 0, nil, true)
		}
		return nil
	}
	stop := requestStop || s.StopRequested || g.now().After(s.Deadline) || s.Phase == "ready" && g.now().Sub(s.CreatedAt) > 45*time.Second
	if stop {
		_, err = g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET stop_requested=TRUE,phase=CASE WHEN phase IN ('ready','running','review') THEN 'stopping' ELSE phase END WHERE id=$1 AND settled_at IS NULL`, id)
		if err != nil {
			return err
		}
		s, err = g.session(ctx, id)
		if err != nil {
			return err
		}
		_, err = g.call(ctx, "POST", "/v1/sessions/stop", "", map[string]any{"session_id": s.UpstreamID, "reason": "USER_CLOSED"})
		if s.StartRequested == nil {
			// No provider start was dispatched and the JWT never left the
			// gateway. Cancel locally even if stopping an unused token is 404.
			return g.complete(ctx, s, 0, 0, nil, true)
		}
		if err != nil {
			var pe *providerError
			if !errors.As(err, &pe) || pe.status != 404 {
				return err
			}
		}
	}
	data, err := g.call(ctx, "GET", "/v1/sessions/"+s.UpstreamID, "", nil)
	if err != nil {
		return err
	}
	var identity struct {
		ID      string `json:"id"`
		Sandbox bool   `json:"is_sandbox"`
	}
	if json.Unmarshal(data, &identity) != nil || identity.ID != s.UpstreamID || identity.Sandbox != s.Sandbox {
		return g.markReview(ctx, id, "provider_receipt_mismatch")
	}
	credits, seconds, ended, safe, err := receipt(data)
	if err != nil {
		return nil
	}
	// Two stable provider receipts separated in time form our settlement policy,
	// not a claim that the supplier promises financial finality. Keep the receipt
	// and the capped retail charge so late adjustments can be reconciled separately.
	if s.ReceiptSeen != nil && sameReceipt(s.Receipt, safe) && g.now().Sub(*s.ReceiptSeen) >= 5*time.Second && g.now().Sub(ended) >= 15*time.Second {
		return g.complete(ctx, s, credits, seconds, safe, false)
	}
	_, err = g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET phase='settling',receipt=$2,receipt_seen_at=CASE WHEN receipt=$2::jsonb THEN receipt_seen_at ELSE NOW() END,updated_at=NOW() WHERE id=$1 AND settled_at IS NULL`, id, safe)
	return err
}
func (g *Gateway) Reconcile(ctx context.Context) error {
	if !g.Configured() {
		return nil
	}
	rows, err := g.db.QueryContext(ctx, `SELECT id::text FROM digital_teacher_sessions WHERE settled_at IS NULL ORDER BY updated_at LIMIT 20`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Advance even failures so an unavailable supplier cannot starve newer
		// sessions whose hard stop deadlines are due.
		_, _ = g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET updated_at=NOW() WHERE id=$1`, id)
		_ = g.reconcileOne(ctx, id, false)
	}
	return nil
}

func sameReceipt(a, b json.RawMessage) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && digest(left) == digest(right)
}

func (g *Gateway) AdminSessions(ctx context.Context) ([]*Session, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM digital_teacher_sessions ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Session{}
	for rows.Next() {
		s, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (g *Gateway) AdminReconcile(ctx context.Context, id string) (*Session, error) {
	if err := g.reconcileOne(ctx, id, true); err != nil {
		return nil, internalError(err)
	}
	return g.session(ctx, id)
}
func (g *Gateway) Run(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			work, cancel := context.WithTimeout(ctx, 50*time.Second)
			_ = g.Reconcile(work)
			cancel()
		}
	}
}
func (g *Gateway) Settings(ctx context.Context, o Owner) (map[string]any, error) {
	p, err := g.Plan(ctx, o.GroupID)
	if err != nil {
		return nil, err
	}
	minutes, err := g.Minutes(ctx, o.UserID)
	if err != nil {
		return nil, err
	}
	teachers, err := g.Teachers(ctx, o)
	if err != nil {
		return nil, err
	}
	return map[string]any{"configured": g.Configured() && p.Enabled && (p.PricePerMinute > 0 || minutes["configured"] == true), "teachers": teachers, "model": Model, "plan": p, "minutes": minutes, "billing_unit": "minute", "billing_policy": "开始前预留所选时长的额度，结束后按实际用量结算并释放余量；收费不超过开始前展示的上限"}, nil
}
func (g *Gateway) Plans(ctx context.Context) ([]Plan, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT group_id,enabled,sandbox_enabled,price_per_minute,max_minutes,version FROM digital_teacher_plans ORDER BY group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		var p Plan
		if err = rows.Scan(&p.GroupID, &p.Enabled, &p.SandboxEnabled, &p.PricePerMinute, &p.MaxMinutes, &p.Version); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (g *Gateway) AdminStatus(ctx context.Context) (map[string]any, error) {
	plans, err := g.Plans(ctx)
	if err != nil {
		return nil, err
	}
	v := g.Health()
	v["plans"] = plans
	return v, nil
}
func (g *Gateway) RecordError(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return fmt.Sprintf("digital_teacher_operation_failed")
}
