package digitalteacher

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ServeSimli is a one-shot signaling/audio relay. WebRTC media travels directly
// between the browser and the supplier room; long-lived secrets stay here.
func (g *Gateway) ServeSimli(w http.ResponseWriter, r *http.Request, s *Session) error {
	if s.Provider != "simli" || g.simliKey == "" || s.StopRequested || !g.allowedToStart(r.Context(), s) || !g.now().Before(s.Deadline) {
		return ErrNotFound
	}
	if !websocket.IsWebSocketUpgrade(r) {
		return fail(400, "websocket_required", "请从数字老师页面连接视频")
	}
	origin := r.Header.Get("Origin")
	// Chromium serializes a top-level Electron file page as file:// for WS,
	// while opaque iframe and fetch callers use null. Both still require the
	// single-use WindHub capability; no owner/admin endpoint accepts file://.
	if origin != "null" && origin != "file://" && origin != "https://windhub.online" {
		return fail(403, "invalid_video_origin", "视频页面来源无效")
	}
	providerToken, err := g.unseal(s.UpstreamToken)
	if err != nil {
		return internalError(err)
	}
	result, err := g.db.ExecContext(r.Context(), `UPDATE digital_teacher_sessions SET phase='starting',start_requested_at=NOW(),deadline=LEAST(deadline,NOW()+(max_seconds+15)*INTERVAL '1 second'),updated_at=NOW() WHERE id=$1 AND phase='ready' AND start_requested_at IS NULL AND NOT stop_requested AND created_at>NOW()-INTERVAL '45 seconds' AND deadline>NOW()`, s.ID)
	if err != nil {
		return internalError(err)
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	s, err = g.session(r.Context(), s.ID)
	if err != nil {
		return internalError(err)
	}
	ctx, cancel := context.WithDeadline(r.Context(), s.Deadline)
	defer cancel()
	defer func() {
		work, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, _ = g.db.ExecContext(work, `UPDATE digital_teacher_sessions SET stop_requested=TRUE,phase=CASE WHEN settled_at IS NULL AND phase<>'settling' THEN 'stopping' ELSE phase END,updated_at=NOW() WHERE id=$1`, s.ID)
	}()
	dialer := websocket.Dialer{HandshakeTimeout: 20 * time.Second}
	upstream, res, err := dialer.DialContext(ctx, g.simliWSURL+"/compose/webrtc/livekit?session_token="+url.QueryEscape(providerToken), nil)
	if err != nil {
		if res != nil {
			res.Body.Close()
			if res.StatusCode >= 400 && res.StatusCode < 500 {
				_ = g.complete(context.WithoutCancel(ctx), s, 0, 0, nil, true)
			}
		}
		return errors.New("Simli stream was not confirmed")
	}
	var upstreamWrite sync.Mutex
	defer func() {
		upstreamWrite.Lock()
		_ = upstream.SetWriteDeadline(time.Now().Add(time.Second))
		_ = upstream.WriteMessage(websocket.TextMessage, []byte("DONE"))
		upstreamWrite.Unlock()
		upstream.Close()
	}()
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(*http.Request) bool { return true }}
	downstream, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil
	}
	defer downstream.Close()
	downstream.SetReadLimit(1 << 20)
	upstream.SetReadLimit(1 << 20)
	_, _ = g.db.ExecContext(ctx, `UPDATE digital_teacher_sessions SET phase=CASE WHEN stop_requested THEN 'stopping' ELSE 'running' END,updated_at=NOW() WHERE id=$1 AND settled_at IS NULL`, s.ID)
	var done sync.WaitGroup
	done.Add(2)
	go func() {
		defer done.Done()
		defer cancel()
		for {
			kind, data, e := downstream.ReadMessage()
			if e != nil {
				return
			}
			if kind != websocket.BinaryMessage && (kind != websocket.TextMessage || (string(data) != "SKIP" && string(data) != "DONE")) {
				return
			}
			upstreamWrite.Lock()
			_ = upstream.SetWriteDeadline(time.Now().Add(5 * time.Second))
			e = upstream.WriteMessage(kind, data)
			upstreamWrite.Unlock()
			if e != nil || string(data) == "DONE" {
				return
			}
		}
	}()
	go func() {
		defer done.Done()
		defer cancel()
		for {
			kind, data, e := upstream.ReadMessage()
			if e != nil {
				return
			}
			_ = downstream.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if downstream.WriteMessage(kind, data) != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-tick.C:
			fresh, e := g.session(ctx, s.ID)
			if e != nil || fresh.StopRequested || fresh.SettledAt != nil || !g.allowedToStart(ctx, fresh) {
				cancel()
				break loop
			}
		}
	}
	downstream.Close()
	upstreamWrite.Lock()
	_ = upstream.SetWriteDeadline(time.Now().Add(time.Second))
	_ = upstream.WriteMessage(websocket.TextMessage, []byte("DONE"))
	upstreamWrite.Unlock()
	upstream.Close()
	done.Wait()
	return nil
}
