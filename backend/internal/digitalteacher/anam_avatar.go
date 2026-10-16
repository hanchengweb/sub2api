package digitalteacher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"time"
)

func (g *Gateway) createAnamAvatar(ctx context.Context, o Owner, id string, photo []byte) (AvatarJob, error) {
	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	_ = mp.WriteField("displayName", "wxm-"+id)
	_ = mp.WriteField("avatarModel", "cara-4")
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="imageFile"; filename="portrait.jpg"`)
	header.Set("Content-Type", http.DetectContentType(photo))
	part, e := mp.CreatePart(header)
	if e != nil {
		return AvatarJob{}, e
	}
	_, _ = part.Write(photo)
	_ = mp.Close()
	req, _ := http.NewRequestWithContext(ctx, "POST", g.anamURL+"/v1/avatars", &body)
	req.Header.Set("Authorization", "Bearer "+g.anamKey)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	client := *g.client
	client.Timeout = 110 * time.Second
	res, e := client.Do(req)
	state, face, message := "unknown", "", "制作结果待核对，请勿重复上传"
	if e == nil {
		defer res.Body.Close()
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if res.StatusCode >= 200 && res.StatusCode < 300 && readErr == nil {
			var a anamAsset
			if json.Unmarshal(raw, &a) == nil && validUUID(a.ID) {
				face = a.ID
				state = "processing"
				message = "Anam 已接收照片，正在准备形象"
			}
		} else if res.StatusCode >= 400 && res.StatusCode < 500 {
			state = "failed"
			switch res.StatusCode {
			case 402, 403:
				message = "Anam 当前账户没有可用的照片制作权限或额度，请查看 Anam 账户"
			case 429:
				message = "Anam 当前制作次数或容量达到上限，请稍后再试"
			default:
				message = "Anam 未接受照片，请换用清晰正脸、无遮挡的单人照"
			}
		}
	}
	_, e = g.db.ExecContext(ctx, `UPDATE digital_teacher_avatar_jobs SET state=$2,face_id=NULLIF($3,'')::uuid,message=$4,updated_at=NOW() WHERE id=$1`, id, state, face, message)
	if e != nil {
		return AvatarJob{}, e
	}
	j, e := g.avatar(ctx, o, id)
	if e != nil {
		return j, e
	}
	if face != "" {
		return g.refreshAnamAvatar(ctx, o, j)
	}
	return j, nil
}
func (g *Gateway) refreshAnamAvatar(ctx context.Context, o Owner, j AvatarJob) (AvatarJob, error) {
	if g.anamKey == "" {
		return j, nil
	}
	face := j.FaceID
	if face == "" {
		list, e := g.anamList(ctx, "avatars")
		if e != nil {
			return j, nil
		}
		for _, a := range list {
			if a.Name == "wxm-"+j.ID && validUUID(a.ID) {
				if face != "" {
					return j, errors.New("Anam avatar recovery ambiguous")
				}
				face = a.ID
			}
		}
		if face == "" {
			return j, nil
		}
	}
	raw, e := g.anamCall(ctx, "GET", "/v1/avatars/"+face, nil)
	if e != nil {
		return j, nil
	}
	var a anamAsset
	if json.Unmarshal(raw, &a) != nil || a.ID != face {
		return j, errors.New("Anam avatar response mismatch")
	}
	state, message := "processing", "正在准备形象，完成后可选用"
	if contains(a.Versions, "cara-4") {
		state, message = "ready", "制作完成，可以选择使用"
	}
	tx, e := g.db.BeginTx(ctx, nil)
	if e != nil {
		return j, e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `UPDATE digital_teacher_avatar_jobs SET state=$2,face_id=$3,message=$4,updated_at=NOW(),checked_at=NOW() WHERE id=$1`, j.ID, state, face, message)
	if e != nil {
		return j, e
	}
	if state == "ready" {
		_, e = tx.ExecContext(ctx, `INSERT INTO digital_teacher_assets(user_id,kind,asset_id,provider,caller_scope) VALUES($1,'avatar',$2,'anam',$3) ON CONFLICT(user_id,caller_scope,kind,asset_id) DO UPDATE SET provider='anam'`, o.UserID, face, o.Scope)
		if e != nil {
			return j, e
		}
	}
	if e = tx.Commit(); e != nil {
		return j, e
	}
	g.anamCache.Lock()
	g.anamCache.until = time.Time{}
	g.anamCache.Unlock()
	return g.avatar(ctx, o, j.ID)
}
