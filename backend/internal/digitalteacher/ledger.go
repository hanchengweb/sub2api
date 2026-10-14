package digitalteacher

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

const sessionColumns = `id::text,user_id,api_key_id,group_id,request_id,digest,COALESCE(teacher_id::text,''),sandbox,max_seconds,plan_version,price_per_minute,held_amount,minute_hold,phase,COALESCE(upstream_id::text,''),COALESCE(upstream_token,''),COALESCE(start_response,''),start_requested_at,sdk_nonce,stop_requested,provider_credits,provider_seconds,charged_amount,receipt,receipt_seen_at,settled_at,COALESCE(error_code,''),created_at,deadline,provider`

type scanner interface{ Scan(...any) error }

func scanSession(row scanner) (*Session, error) {
	s := new(Session)
	var receipt []byte
	err := row.Scan(&s.ID, &s.UserID, &s.KeyID, &s.GroupID, &s.RequestID, &s.Digest, &s.TeacherID, &s.Sandbox, &s.MaxSeconds, &s.PlanVersion, &s.Price, &s.Held, &s.MinuteHold, &s.Phase, &s.UpstreamID, &s.UpstreamToken, &s.StartResponse, &s.StartRequested, &s.Nonce, &s.StopRequested, &s.ProviderCredits, &s.ProviderSeconds, &s.Charged, &receipt, &s.ReceiptSeen, &s.SettledAt, &s.ErrorCode, &s.CreatedAt, &s.Deadline, &s.Provider)
	s.Receipt = receipt
	return s, err
}
func (g *Gateway) session(ctx context.Context, id string) (*Session, error) {
	if !validUUID(id) {
		return nil, ErrNotFound
	}
	s, err := scanSession(g.db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM digital_teacher_sessions WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}
func (g *Gateway) List(ctx context.Context, o Owner) ([]*Session, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM digital_teacher_sessions WHERE user_id=$1 AND api_key_id=$2 ORDER BY created_at DESC LIMIT 50`, o.UserID, o.KeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []*Session{}
	for rows.Next() {
		s, e := scanSession(rows)
		if e != nil {
			return nil, e
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

type keyBudget struct {
	Quota, Used                                 float64
	Limit5, Limit1, Limit7, Used5, Used1, Used7 float64
	Start5, Start1, Start7                      sql.NullTime
}

func (b keyBudget) allows(amount float64, now time.Time) bool {
	if b.Quota > 0 && b.Used+amount > b.Quota+1e-8 {
		return false
	}
	for _, w := range []struct {
		limit, used float64
		start       sql.NullTime
		window      time.Duration
	}{{b.Limit5, b.Used5, b.Start5, 5 * time.Hour}, {b.Limit1, b.Used1, b.Start1, 24 * time.Hour}, {b.Limit7, b.Used7, b.Start7, 7 * 24 * time.Hour}} {
		used := w.used
		if !w.start.Valid || !w.start.Time.Add(w.window).After(now) {
			used = 0
		}
		if w.limit > 0 && used+amount > w.limit+1e-8 {
			return false
		}
	}
	return true
}
func (g *Gateway) reserve(ctx context.Context, o Owner, req StartRequest, teacherID string, t Teacher) (*Session, bool, error) {
	if len(req.RequestID) < 8 || len(req.RequestID) > 100 || strings.TrimSpace(req.RequestID) != req.RequestID {
		return nil, false, fail(400, "request_id_required", "请提供稳定的会话请求编号")
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var accountType string
	var balance float64
	err = tx.QueryRowContext(ctx, `SELECT account_type,balance FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, o.UserID).Scan(&accountType, &balance)
	if err != nil {
		return nil, false, ErrNotFound
	}
	hash := digest(struct {
		Request StartRequest
		Teacher Teacher
	}{req, t})
	old, e := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM digital_teacher_sessions WHERE user_id=$1 AND api_key_id=$2 AND request_id=$3`, o.UserID, o.KeyID, req.RequestID))
	if e == nil {
		if old.Digest != hash {
			return nil, false, fail(409, "request_id_conflict", "此请求编号已用于不同的老师或时长")
		}
		return old, true, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, false, e
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM digital_teacher_sessions WHERE user_id=$1 AND phase NOT IN ('completed','failed') UNION ALL SELECT 1 FROM digital_teacher_studio_sessions WHERE user_id=$1 AND closed_at IS NULL AND deadline>NOW())`, o.UserID).Scan(&active); err != nil {
		return nil, false, err
	}
	if active {
		return nil, false, ErrConflict
	}
	p := Plan{GroupID: o.GroupID}
	err = tx.QueryRowContext(ctx, `SELECT p.enabled,p.sandbox_enabled,p.price_per_minute,p.max_minutes,p.version FROM digital_teacher_plans p JOIN groups g ON g.id=p.group_id WHERE p.group_id=$1 AND g.status='active' AND g.deleted_at IS NULL FOR SHARE OF p`, o.GroupID).Scan(&p.Enabled, &p.SandboxEnabled, &p.PricePerMinute, &p.MaxMinutes, &p.Version)
	if err != nil || !p.Enabled || req.Sandbox && !p.SandboxEnabled {
		return nil, false, fail(403, "digital_teacher_not_enabled", "当前算力包尚未开通此数字老师服务")
	}
	minutes := req.Minutes
	if minutes == 0 {
		minutes = p.MaxMinutes
	}
	if req.Sandbox {
		minutes = 1
	}
	if minutes < 1 || minutes > p.MaxMinutes || minutes > 30 {
		return nil, false, fail(400, "invalid_session_duration", "会话时长超出当前算力包上限")
	}
	price := p.PricePerMinute
	if req.Sandbox {
		price = 0
	}
	held := math.Round(price*float64(minutes)*1e8) / 1e8
	var b keyBudget
	err = tx.QueryRowContext(ctx, `SELECT quota,quota_used,rate_limit_5h,rate_limit_1d,rate_limit_7d,usage_5h,usage_1d,usage_7d,window_5h_start,window_1d_start,window_7d_start FROM api_keys WHERE id=$1 AND user_id=$2 AND group_id=$3 AND status IN ('active','quota_exhausted') AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at>NOW()) FOR UPDATE`, o.KeyID, o.UserID, o.GroupID).Scan(&b.Quota, &b.Used, &b.Limit5, &b.Limit1, &b.Limit7, &b.Used5, &b.Used1, &b.Used7, &b.Start5, &b.Start1, &b.Start7)
	if err != nil {
		return nil, false, ErrNotFound
	}
	var groupAllowed bool
	err = tx.QueryRowContext(ctx, groupAccessSQL, o.UserID, o.GroupID).Scan(&groupAllowed)
	if err != nil {
		return nil, false, err
	}
	if !groupAllowed {
		return nil, false, fail(403, "group_not_allowed", "算力包授权已失效")
	}
	if held > 0 && (balance+1e-8 < held || !b.allows(held, g.now())) {
		return nil, false, fail(402, "digital_teacher_insufficient_balance", "账户余额或密钥额度不足以预留本次会话")
	}
	minuteHold := 0.0
	if req.Sandbox && p.PricePerMinute == 0 {
		var granted bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM digital_teacher_minutes WHERE user_id=$1 AND granted>0)`, o.UserID).Scan(&granted); err != nil {
			return nil, false, err
		}
		if !granted {
			return nil, false, fail(403, "digital_teacher_minutes_not_granted", "账户尚未开通数字老师试用，请联系管理员")
		}
	}
	if !req.Sandbox {
		var available float64
		e = tx.QueryRowContext(ctx, `SELECT granted-reserved-consumed FROM digital_teacher_minutes WHERE user_id=$1 FOR UPDATE`, o.UserID).Scan(&available)
		if errors.Is(e, sql.ErrNoRows) {
			if accountType == "organization_service" || price == 0 {
				return nil, false, fail(403, "digital_teacher_minutes_not_granted", "账户尚未获配数字人分钟额度，请联系管理员开通")
			}
		} else if e != nil {
			return nil, false, e
		} else {
			minuteHold = float64(minutes)
			if available+1e-8 < minuteHold {
				return nil, false, fail(402, "digital_teacher_minutes_exhausted", "数字人分钟额度不足")
			}
		}
	}
	id := uuid.NewString()
	nonce := strings.ReplaceAll(uuid.NewString(), "-", "") + strings.ReplaceAll(uuid.NewString(), "-", "")
	var teacher any
	if teacherID != "" {
		teacher = teacherID
	}
	s, err := scanSession(tx.QueryRowContext(ctx, `INSERT INTO digital_teacher_sessions(id,user_id,api_key_id,group_id,request_id,digest,teacher_id,sandbox,max_seconds,plan_version,price_per_minute,held_amount,minute_hold,sdk_nonce,deadline,provider) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NOW()+($9+45)*INTERVAL '1 second',$15) RETURNING `+sessionColumns, id, o.UserID, o.KeyID, o.GroupID, req.RequestID, hash, teacher, req.Sandbox, minutes*60, p.Version, price, held, minuteHold, nonce, g.provider))
	if err != nil {
		return nil, false, err
	}
	if held > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET balance=balance-$2,frozen_balance=COALESCE(frozen_balance,0)+$2,updated_at=NOW() WHERE id=$1`, o.UserID, held); err != nil {
			return nil, false, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET quota_used=quota_used+$2,usage_5h=(CASE WHEN window_5h_start IS NULL OR window_5h_start+INTERVAL '5 hours'<=NOW() THEN 0 ELSE usage_5h END)+$2,usage_1d=(CASE WHEN window_1d_start IS NULL OR window_1d_start+INTERVAL '1 day'<=NOW() THEN 0 ELSE usage_1d END)+$2,usage_7d=(CASE WHEN window_7d_start IS NULL OR window_7d_start+INTERVAL '7 days'<=NOW() THEN 0 ELSE usage_7d END)+$2,window_5h_start=CASE WHEN window_5h_start IS NULL OR window_5h_start+INTERVAL '5 hours'<=NOW() THEN NOW() ELSE window_5h_start END,window_1d_start=CASE WHEN window_1d_start IS NULL OR window_1d_start+INTERVAL '1 day'<=NOW() THEN NOW() ELSE window_1d_start END,window_7d_start=CASE WHEN window_7d_start IS NULL OR window_7d_start+INTERVAL '7 days'<=NOW() THEN NOW() ELSE window_7d_start END,updated_at=NOW() WHERE id=$1`, o.KeyID, held); err != nil {
			return nil, false, err
		}
	}
	if minuteHold > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE digital_teacher_minutes SET reserved=reserved+$2 WHERE user_id=$1`, o.UserID, minuteHold); err != nil {
			return nil, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	g.changed(ctx, s)
	return s, false, nil
}

// Complete is one transaction for both money and minute limits. Only the
// service's verified terminal receipt, or a confirmed pre-dispatch failure,
// may call it. Browser events and timeouts never manufacture a refund.
func (g *Gateway) complete(ctx context.Context, s *Session, credits, seconds float64, receipt json.RawMessage, failed bool) error {
	if credits < 0 || seconds < 0 || math.IsNaN(credits) || math.IsInf(credits, 0) || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return errors.New("invalid usage receipt")
	}
	minutes := credits / 2
	if s.Provider == "simli" {
		minutes = seconds / 60
	}
	minutes = math.Round(minutes*1e6) / 1e6
	amount := math.Round(minutes*s.Price*1e8) / 1e8
	if s.Sandbox {
		minutes = 0
		amount = 0
		if credits != 0 {
			return g.markReview(ctx, s.ID, "sandbox_charged")
		}
	}
	// The quoted maximum is a customer charge ceiling. Any upstream overrun
	// remains visible on the receipt and is absorbed by the service.
	overrun := amount > s.Held+1e-8 || s.MinuteHold > 0 && minutes > s.MinuteHold+1e-6
	amount = math.Min(amount, s.Held)
	if s.MinuteHold > 0 {
		minutes = math.Min(minutes, s.MinuteHold)
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var userID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, s.UserID).Scan(&userID); err != nil {
		return err
	}
	current, err := scanSession(tx.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM digital_teacher_sessions WHERE id=$1 FOR UPDATE`, s.ID))
	if err != nil {
		return err
	}
	if current.SettledAt != nil {
		return nil
	}
	refund := math.Round((s.Held-amount)*1e8) / 1e8
	if s.Held > 0 {
		result, e := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2,frozen_balance=frozen_balance-$3,updated_at=NOW() WHERE id=$1 AND frozen_balance>=$3`, s.UserID, refund, s.Held)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return errors.New("session hold missing")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET quota_used=GREATEST(0,quota_used-$2),usage_5h=CASE WHEN window_5h_start<=$3 THEN GREATEST(0,usage_5h-$2) ELSE usage_5h END,usage_1d=CASE WHEN window_1d_start<=$3 THEN GREATEST(0,usage_1d-$2) ELSE usage_1d END,usage_7d=CASE WHEN window_7d_start<=$3 THEN GREATEST(0,usage_7d-$2) ELSE usage_7d END,status=CASE WHEN status='quota_exhausted' AND (quota<=0 OR quota_used-$2<quota) THEN 'active' ELSE status END,updated_at=NOW() WHERE id=$1`, s.KeyID, refund, s.CreatedAt); err != nil {
			return err
		}
	}
	if s.MinuteHold > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE digital_teacher_minutes SET reserved=reserved-$2,consumed=consumed+$3 WHERE user_id=$1`, s.UserID, s.MinuteHold, minutes); err != nil {
			return err
		}
	}
	phase := "completed"
	if failed {
		phase = "failed"
	}
	if len(receipt) == 0 {
		receipt = json.RawMessage(`{}`)
	}
	var supplierCredits any = credits
	if s.Provider == "simli" {
		supplierCredits = nil
	} // Session duration is not a supplier financial receipt.
	if _, err = tx.ExecContext(ctx, `UPDATE digital_teacher_sessions SET phase=$2,charged_amount=$3,provider_credits=$4,provider_seconds=$5,receipt=$6,settled_at=NOW(),updated_at=NOW(),upstream_token=NULL,start_response=NULL,error_code=CASE WHEN $7 THEN 'provider_usage_exceeds_limit' ELSE error_code END WHERE id=$1`, s.ID, phase, amount, supplierCredits, seconds, receipt, overrun); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	g.changed(ctx, s)
	return nil
}
func (g *Gateway) markReview(ctx context.Context, id, reason string) error {
	_, err := g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET phase='review',error_code=$2,updated_at=NOW() WHERE id=$1 AND settled_at IS NULL`, id, reason)
	if err != nil {
		return err
	}
	return fail(409, "digital_teacher_reconciliation_required", "会话用量需要核对，预留额度暂未释放")
}
func (g *Gateway) Minutes(ctx context.Context, userID int64) (map[string]any, error) {
	var granted, reserved, consumed float64
	err := g.db.QueryRowContext(ctx, `SELECT granted,reserved,consumed FROM digital_teacher_minutes WHERE user_id=$1`, userID).Scan(&granted, &reserved, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"configured": false}, nil
	}
	return map[string]any{"configured": true, "granted": granted, "reserved": reserved, "consumed": consumed, "available": granted - reserved - consumed}, err
}

func (g *Gateway) SetPlan(ctx context.Context, p Plan) (Plan, error) {
	if p.GroupID <= 0 || p.MaxMinutes < 1 || p.MaxMinutes > 30 || p.PricePerMinute < 0 || math.IsNaN(p.PricePerMinute) || math.IsInf(p.PricePerMinute, 0) || p.PricePerMinute > 1e9 || p.PricePerMinute != math.Round(p.PricePerMinute*1e8)/1e8 || p.Version < 0 {
		return p, fail(400, "invalid_digital_teacher_plan", "数字人算力包配置无效")
	}
	var next int64
	err := g.db.QueryRowContext(ctx, `INSERT INTO digital_teacher_plans(group_id,enabled,sandbox_enabled,price_per_minute,max_minutes,version) SELECT $1,$2,$3,$4,$5,1 WHERE $6=0 ON CONFLICT(group_id) DO NOTHING RETURNING version`, p.GroupID, p.Enabled, p.SandboxEnabled, p.PricePerMinute, p.MaxMinutes, p.Version).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		err = g.db.QueryRowContext(ctx, `UPDATE digital_teacher_plans SET enabled=$2,sandbox_enabled=$3,price_per_minute=$4,max_minutes=$5,version=version+1,updated_at=NOW() WHERE group_id=$1 AND version=$6 RETURNING version`, p.GroupID, p.Enabled, p.SandboxEnabled, p.PricePerMinute, p.MaxMinutes, p.Version).Scan(&next)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return p, fail(409, "digital_teacher_plan_conflict", "算力包配置已变化，请刷新后重试")
	}
	p.Version = next
	return p, err
}
func (g *Gateway) Grant(ctx context.Context, userID int64, requestID string, minutes float64, reason string) error {
	if userID <= 0 || len(requestID) < 8 || len(requestID) > 100 || !positive(minutes) || minutes > 1e9 || minutes != math.Round(minutes*1e6)/1e6 || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 500 {
		return fail(400, "invalid_minute_grant", "请填写有效的分钟额度、原因和请求编号")
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&id); err != nil {
		return ErrNotFound
	}
	var old float64
	var oldReason string
	err = tx.QueryRowContext(ctx, `SELECT minutes,reason FROM digital_teacher_grants WHERE user_id=$1 AND request_id=$2`, userID, requestID).Scan(&old, &oldReason)
	if err == nil {
		if old != minutes || oldReason != reason {
			return fail(409, "minute_grant_conflict", "请求编号已用于另一笔额度发放")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO digital_teacher_grants(user_id,request_id,minutes,reason) VALUES($1,$2,$3,$4)`, userID, requestID, minutes, reason); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO digital_teacher_minutes(user_id,granted) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET granted=digital_teacher_minutes.granted+EXCLUDED.granted`, userID, minutes); err != nil {
		return err
	}
	return tx.Commit()
}
func (g *Gateway) BindAsset(ctx context.Context, userID int64, kind, id string) error {
	if userID <= 0 || !validUUID(id) || (kind != "avatar" && kind != "voice") {
		return fail(400, "invalid_teacher_asset", "素材分配参数无效")
	}
	if g.provider == "simli" {
		return g.bindSimliAsset(ctx, userID, kind, id)
	}
	if _, err := g.call(ctx, "GET", "/v1/"+kind+"s/"+id, "", nil); err != nil {
		return internalError(err)
	}
	_, err := g.db.ExecContext(ctx, `INSERT INTO digital_teacher_assets(user_id,kind,asset_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, userID, kind, id)
	return err
}
