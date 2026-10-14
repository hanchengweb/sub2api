package digitalteacher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Photos are sent only to the configured provider. We retain a small private
// thumbnail, consented request digest and job state, never the original photo.
type AvatarJob struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	State      string    `json:"state"`
	FaceID     string    `json:"faceId,omitempty"`
	Preview    string    `json:"previewUrl"`
	Message    string    `json:"message"`
	Queue      int       `json:"queuePosition"`
	CreatedAt  time.Time `json:"createdAt"`
	Generation string    `json:"generation"`
	digest     string
}

const avatarColumns = `id::text,name,state,COALESCE(face_id::text,''),preview,message,queue_position,created_at,digest,generation`

func scanAvatar(row interface{ Scan(...any) error }) (AvatarJob, error) {
	var j AvatarJob
	var preview []byte
	err := row.Scan(&j.ID, &j.Name, &j.State, &j.FaceID, &preview, &j.Message, &j.Queue, &j.CreatedAt, &j.digest, &j.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	j.Preview = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(preview)
	return j, err
}

func (g *Gateway) avatar(ctx context.Context, o Owner, id string) (AvatarJob, error) {
	if !validUUID(id) {
		return AvatarJob{}, ErrNotFound
	}
	return scanAvatar(g.db.QueryRowContext(ctx, `SELECT `+avatarColumns+` FROM digital_teacher_avatar_jobs WHERE user_id=$1 AND id=$2`, o.UserID, id))
}

func (g *Gateway) AvatarJobs(ctx context.Context, o Owner) ([]AvatarJob, error) {
	if _, err := g.requirePlan(ctx, o, false); err != nil {
		return nil, err
	}
	rows, err := g.db.QueryContext(ctx, `SELECT `+avatarColumns+` FROM digital_teacher_avatar_jobs WHERE user_id=$1 ORDER BY created_at DESC LIMIT 30`, o.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AvatarJob{}
	for rows.Next() {
		j, e := scanAvatar(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func avatarPreview(photo []byte) ([]byte, error) {
	invalid := fail(400, "avatar_photo_invalid", "请选择小于 5 MB、至少 512×512 的 JPG、PNG 或 WebP 正面单人照片")
	if len(photo) == 0 || len(photo) >= 5<<20 {
		return nil, invalid
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(photo))
	if err != nil || (format != "jpeg" && format != "png" && format != "webp") || cfg.Width < 512 || cfg.Height < 512 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 24000000 {
		return nil, invalid
	}
	img, _, err := image.Decode(bytes.NewReader(photo))
	if err != nil {
		return nil, invalid
	}
	w, h := 256, 256*cfg.Height/cfg.Width
	if h > 256 {
		h = 256
		w = 256 * cfg.Width / cfg.Height
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(thumb, thumb.Bounds(), img, img.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err = jpeg.Encode(&out, thumb, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Legacy rejected a valid 2 MB PNG at multipart parsing, but accepted the same
// pixel dimensions as a 304 KB JPEG. Stay below a conservative 900 KiB transport
// budget without reducing dimensions. This is not a documented provider limit.
func legacyPhoto(photo []byte) ([]byte, error) {
	if http.DetectContentType(photo) == "image/jpeg" && len(photo) < 900<<10 {
		return photo, nil
	}
	img, _, err := image.Decode(bytes.NewReader(photo))
	if err != nil {
		return nil, err
	}
	for _, quality := range []int{95, 85} {
		var out bytes.Buffer
		if err = jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		if out.Len() < 900<<10 {
			return out.Bytes(), nil
		}
	}
	return nil, fail(400, "avatar_legacy_photo_large", "这张照片压缩后仍偏大。请裁为清晰的正脸近景，或使用小于 900 KB 的 JPG 再制作 Legacy 形象")
}

func (g *Gateway) CreateAvatar(ctx context.Context, o Owner, requestID, name string, consent bool, photo []byte, generations ...string) (AvatarJob, error) {
	if g.provider != "simli" {
		return AvatarJob{}, fail(409, "avatar_provider_changed", "请刷新当前数字老师服务")
	}
	generation := "trinity"
	if len(generations) > 0 && generations[0] != "" {
		generation = generations[0]
	}
	if generation != "trinity" && generation != "legacy" {
		return AvatarJob{}, fail(400, "avatar_generation_invalid", "请选择照片制作方式")
	}
	plan, err := g.requirePlan(ctx, o, false)
	if err != nil {
		return AvatarJob{}, err
	}
	minutes, err := g.Minutes(ctx, o.UserID)
	if err != nil {
		return AvatarJob{}, err
	}
	if plan.PricePerMinute <= 0 && minutes["configured"] != true {
		return AvatarJob{}, fail(403, "avatar_not_enabled", "当前账户尚未开通数字老师，请先开通后制作形象")
	}
	name = strings.TrimSpace(name)
	if !validUUID(requestID) || !consent || name == "" || len([]rune(name)) > 60 {
		return AvatarJob{}, fail(400, "avatar_request_invalid", "请填写形象名称，并确认拥有照片使用授权")
	}
	preview, err := avatarPreview(photo)
	if err != nil {
		return AvatarJob{}, err
	}
	upload, filename := photo, "portrait.png"
	if generation == "legacy" {
		upload, err = legacyPhoto(photo)
		if err != nil {
			return AvatarJob{}, err
		}
		filename = "portrait.jpg"
	}
	sum := sha256.Sum256(photo)
	dg := digest([]string{name, hex.EncodeToString(sum[:]), generation})
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return AvatarJob{}, err
	}
	defer tx.Rollback()
	// Serialize admissions across replicas; do not hold this lock during upload.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(734152901)`); err != nil {
		return AvatarJob{}, err
	}
	existing, e := scanAvatar(tx.QueryRowContext(ctx, `SELECT `+avatarColumns+` FROM digital_teacher_avatar_jobs WHERE user_id=$1 AND request_id=$2`, o.UserID, requestID))
	if e == nil {
		if existing.digest != dg {
			return AvatarJob{}, fail(409, "avatar_request_conflict", "本次提交内容已改变，请刷新制作记录")
		}
		return existing, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return AvatarJob{}, e
	}
	var busy bool
	var count, today int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM digital_teacher_avatar_jobs WHERE state IN ('submitting','pending','processing') OR (user_id=$1 AND state='unknown'))`, o.UserID).Scan(&busy); err != nil {
		return AvatarJob{}, err
	}
	if busy {
		return AvatarJob{}, fail(409, "avatar_generation_busy", "已有形象正在制作或等待确认，请刷新制作记录后再提交")
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE created_at>NOW()-interval '24 hours') FROM digital_teacher_avatar_jobs WHERE user_id=$1`, o.UserID).Scan(&count, &today); err != nil {
		return AvatarJob{}, err
	}
	if count >= 30 || today >= 3 {
		return AvatarJob{}, fail(429, "avatar_generation_limit", "本账户的形象制作次数已达到限额，请稍后再试")
	}
	id := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO digital_teacher_avatar_jobs(id,user_id,request_id,name,digest,preview,state,message,generation) VALUES($1,$2,$3,$4,$5,$6,'submitting','照片已提交，正在确认制作任务',$7)`, id, o.UserID, requestID, name, dg, preview, generation)
	if err != nil {
		return AvatarJob{}, err
	}
	if err = tx.Commit(); err != nil {
		return AvatarJob{}, err
	}

	// Once submitted, a disconnect must not cause a second paid creation. Unknown
	// outcomes remain durable and may only be reconciled to this exact job name.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 120*time.Second)
	defer cancel()
	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="image"; filename="`+filename+`"`)
	header.Set("Content-Type", http.DetectContentType(upload))
	part, _ := mp.CreatePart(header)
	_, _ = part.Write(upload)
	_ = mp.Close()
	route := "/faces/trinity?gsVersion=GSA_1.0&face_name="
	if generation == "legacy" {
		route = "/faces/legacy?characterVersion=1.5&face_name="
	}
	req, _ := http.NewRequestWithContext(work, "POST", g.simliURL+route+url.QueryEscape("wxm-"+id), &body)
	req.Header.Set("x-simli-api-key", g.simliKey)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	client := *g.client
	client.Timeout = 120 * time.Second
	res, callErr := client.Do(req)
	phase, face, message := "unknown", "", "制作结果尚未确认，请刷新状态；请勿重复上传"
	if callErr == nil {
		defer res.Body.Close()
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 65537))
		if res.StatusCode >= 200 && res.StatusCode < 300 && readErr == nil && len(raw) <= 65536 {
			var result struct {
				FaceID       string `json:"face_id"`
				ID           string `json:"id"`
				CharacterUID string `json:"character_uid"`
			}
			if json.Unmarshal(raw, &result) == nil {
				face = result.FaceID
				if face == "" {
					face = result.ID
				}
				if face == "" && generation == "legacy" {
					face = result.CharacterUID
				}
				if validUUID(face) {
					phase, message = "pending", "照片已接收，正在排队制作"
				} else {
					face = ""
				}
			}
		} else if res.StatusCode >= 400 && res.StatusCode < 500 {
			phase = "failed"
			switch res.StatusCode {
			case 402, 403:
				message = "当前服务没有可用的形象制作名额，请联系服务管理员开通名额后重试"
			case 429:
				message = "服务方制作队列已满，请稍后重新提交"
			case 400, 422:
				message = "服务方未接受这张照片，请换用清晰正脸、无遮挡的单人照片；若仍失败，请核对制作名额"
				if bytes.Contains(raw, []byte("error parsing the body")) {
					message = "服务方未能解析照片上传，请稍后重试或联系管理员；这不代表照片不合格或名额不足"
				}
			default:
				message = "形象制作服务未接受请求，请联系服务管理员检查配置"
			}
		}
	}
	update, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer done()
	_, err = g.db.ExecContext(update, `UPDATE digital_teacher_avatar_jobs SET state=$2,face_id=NULLIF($3,'')::uuid,message=$4,updated_at=NOW() WHERE id=$1`, id, phase, face, message)
	if err != nil {
		return AvatarJob{}, err
	}
	return g.avatar(update, o, id)
}

type trinityGeneration struct {
	FaceID string `json:"face_id"`
	Status string `json:"status"`
	Queue  int    `json:"queue_position"`
}

func (g *Gateway) reconcileAvatars(ctx context.Context) {
	if g.provider != "simli" {
		return
	}
	rows, err := g.db.QueryContext(ctx, `SELECT id::text,user_id FROM digital_teacher_avatar_jobs WHERE state IN ('submitting','pending','processing','unknown') AND (checked_at IS NULL OR checked_at<NOW()-interval '30 seconds') ORDER BY checked_at NULLS FIRST LIMIT 3`)
	if err != nil {
		return
	}
	type pending struct {
		id     string
		userID int64
	}
	jobs := []pending{}
	for rows.Next() {
		var j pending
		if rows.Scan(&j.id, &j.userID) == nil {
			jobs = append(jobs, j)
		}
	}
	rows.Close()
	for _, j := range jobs {
		_, _ = g.RefreshAvatar(ctx, Owner{UserID: j.userID}, j.id)
	}
}

func (g *Gateway) RefreshAvatar(ctx context.Context, o Owner, id string) (AvatarJob, error) {
	j, err := g.avatar(ctx, o, id)
	if err != nil {
		return j, err
	}
	if j.State == "ready" || j.State == "failed" {
		return j, nil
	}
	if j.State == "submitting" && time.Since(j.CreatedAt) < 130*time.Second {
		return j, nil
	}
	result, err := g.db.ExecContext(ctx, `UPDATE digital_teacher_avatar_jobs SET checked_at=NOW() WHERE id=$1 AND (checked_at IS NULL OR checked_at<NOW()-interval '10 seconds')`, j.ID)
	if err != nil {
		return j, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return j, nil
	}
	if j.FaceID == "" {
		faces, e := g.simliFaces(ctx)
		if e != nil {
			return j, nil
		}
		for _, f := range faces {
			if f.Name == "wxm-"+j.ID && validUUID(f.ID) {
				if j.FaceID != "" {
					return j, nil
				}
				j.FaceID = f.ID
			}
		}
		if j.FaceID == "" {
			_, err = g.db.ExecContext(ctx, `UPDATE digital_teacher_avatar_jobs SET state='unknown',message='制作结果尚未确认，请联系服务管理员核对；请勿重复提交',updated_at=NOW() WHERE id=$1`, j.ID)
			if err != nil {
				return j, err
			}
			return g.avatar(ctx, o, j.ID)
		}
	}
	raw, err := g.simliCall(ctx, "GET", "/faces/"+j.Generation+"/generation_status?face_id="+url.QueryEscape(j.FaceID), nil)
	if err != nil {
		return j, nil
	}
	var status struct {
		trinityGeneration
		Generations []trinityGeneration `json:"generations"`
	}
	if json.Unmarshal(raw, &status) != nil {
		return j, nil
	}
	match := status.trinityGeneration
	for _, s := range status.Generations {
		if s.FaceID == j.FaceID {
			match = s
			break
		}
	}
	if match.FaceID != j.FaceID {
		return j, nil
	}
	phase, message := "unknown", "制作状态暂未确认，请稍后刷新"
	switch match.Status {
	case "pending":
		phase, message = "pending", "正在排队制作"
	case "processing":
		phase, message = "processing", "正在制作，完成后即可选为老师"
	case "completed":
		phase, message = "ready", "制作完成，可以选择使用"
	case "failed":
		phase, message = "failed", "制作未成功，请检查照片正脸与清晰度，再重新提交"
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return j, err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE digital_teacher_avatar_jobs SET state=$2,face_id=$3,message=$4,queue_position=$5,updated_at=NOW() WHERE id=$1 AND state NOT IN ('ready','failed')`, j.ID, phase, j.FaceID, message, match.Queue)
	if err != nil {
		return j, err
	}
	if phase == "ready" {
		_, err = tx.ExecContext(ctx, `INSERT INTO digital_teacher_assets(user_id,kind,asset_id,provider) VALUES($1,'avatar',$2,'simli') ON CONFLICT(user_id,kind,asset_id) DO UPDATE SET provider='simli'`, o.UserID, j.FaceID)
		if err != nil {
			return j, err
		}
	}
	if err = tx.Commit(); err != nil {
		return j, err
	}
	return g.avatar(ctx, o, j.ID)
}
