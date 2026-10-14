package digitalteacher

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestLegacyPhotoTransport(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1024, 1024))
	var seed uint32 = 7
	for y := 0; y < 1024; y++ {
		for x := 0; x < 1024; x++ {
			seed = 1664525*seed + 1013904223
			img.SetRGBA(x, y, color.RGBA{byte(seed >> 24), byte(seed >> 16), byte(seed >> 8), 255})
		}
	}
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, img))
	require.Greater(t, source.Len(), 900<<10)
	encoded, err := legacyPhoto(source.Bytes())
	require.NoError(t, err)
	require.Less(t, len(encoded), 900<<10)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(encoded))
	require.NoError(t, err)
	require.Equal(t, "jpeg", format)
	require.Equal(t, 1024, cfg.Width)
	require.Equal(t, 1024, cfg.Height)
}

func testAvatarJobs(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	g, err := New(db, Options{SimliAPIKey: "test-simli", Provider: "simli", CryptoSecret: strings.Repeat("x", 32)})
	require.NoError(t, err)
	var o Owner
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash) VALUES($1,'test') RETURNING id`, uuid.NewString()+"@example.invalid").Scan(&o.UserID))
	require.NoError(t, db.QueryRow(`INSERT INTO groups(name) VALUES($1) RETURNING id`, uuid.NewString()).Scan(&o.GroupID))
	_, err = g.SetPlan(ctx, Plan{GroupID: o.GroupID, Enabled: true, MaxMinutes: 1})
	require.NoError(t, err)
	var photo bytes.Buffer
	require.NoError(t, png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 512, 512))))
	_, err = g.CreateAvatar(ctx, o, uuid.NewString(), "老师", true, photo.Bytes())
	require.Error(t, err, "an enabled group alone must not grant free creation")
	require.NoError(t, g.Grant(ctx, o.UserID, "avatar-test", 1, "test"))
	var calls atomic.Int64
	var outcome atomic.Int64
	face := uuid.NewString()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-simli", r.Header.Get("x-simli-api-key"))
		switch r.URL.Path {
		case "/faces/trinity", "/faces/legacy":
			calls.Add(1)
			require.Equal(t, "POST", r.Method)
			if r.URL.Path == "/faces/legacy" {
				require.Equal(t, "1.5", r.URL.Query().Get("characterVersion"))
			} else {
				require.Equal(t, "GSA_1.0", r.URL.Query().Get("gsVersion"))
			}
			require.True(t, strings.HasPrefix(r.URL.Query().Get("face_name"), "wxm-"))
			require.NoError(t, r.ParseMultipartForm(6<<20))
			defer r.MultipartForm.RemoveAll()
			file, header, e := r.FormFile("image")
			require.NoError(t, e)
			if r.URL.Path == "/faces/legacy" {
				require.Equal(t, "image/jpeg", header.Header.Get("Content-Type"))
				require.Less(t, header.Size, int64(900<<10))
				cfg, format, err := image.DecodeConfig(file)
				require.NoError(t, err)
				require.Equal(t, "jpeg", format)
				require.Equal(t, 512, cfg.Width)
			}
			file.Close()
			if outcome.Load() == 1 {
				w.WriteHeader(503)
				return
			}
			field := "face_id"
			if r.URL.Path == "/faces/legacy" {
				field = "character_uid"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{field: face})
		case "/faces/trinity/generation_status", "/faces/legacy/generation_status":
			require.Equal(t, face, r.URL.Query().Get("face_id"))
			// Provider's top-level latest job belongs to somebody else.
			_ = json.NewEncoder(w).Encode(map[string]any{"face_id": uuid.NewString(), "status": "failed", "generations": []any{map[string]any{"face_id": face, "status": "completed", "queue_position": 0}}})
		case "/faces":
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	g.simliURL = fake.URL
	request := uuid.NewString()
	_, err = g.CreateAvatar(ctx, o, request, "老师", false, photo.Bytes())
	require.Error(t, err)
	_, err = g.CreateAvatar(ctx, o, request, "老师", true, []byte("not an image"))
	require.Error(t, err)
	require.Zero(t, calls.Load())
	j, err := g.CreateAvatar(ctx, o, request, "我的老师", true, photo.Bytes())
	require.NoError(t, err)
	require.Equal(t, "pending", j.State)
	require.True(t, strings.HasPrefix(j.Preview, "data:image/jpeg;base64,"))
	require.NotContains(t, j.Preview, "test-simli")
	again, err := g.CreateAvatar(ctx, o, request, "我的老师", true, photo.Bytes())
	require.NoError(t, err)
	require.Equal(t, j.ID, again.ID)
	require.EqualValues(t, 1, calls.Load())
	_, err = g.CreateAvatar(ctx, o, request, "另一老师", true, photo.Bytes())
	require.Error(t, err)
	_, err = g.RefreshAvatar(ctx, Owner{UserID: o.UserID + 999}, j.ID)
	require.ErrorIs(t, err, ErrNotFound)
	j, err = g.RefreshAvatar(ctx, o, j.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", j.State)
	catalog, err := g.simliCatalog(ctx, o)
	require.NoError(t, err)
	require.Len(t, catalog.Avatars, 22)
	require.Len(t, catalog.Voices, 6)
	teacher := Teacher{Provider: "simli", ID: uuid.NewString(), Name: "我的老师", Subject: "数学", Instructions: "耐心讲解", AvatarID: face, VoiceID: "steady"}
	_, err = g.SaveTeacher(ctx, o, teacher)
	require.NoError(t, err)
	face = uuid.NewString()
	legacy, err := g.CreateAvatar(ctx, o, uuid.NewString(), "照片老师", true, photo.Bytes(), "legacy")
	require.NoError(t, err)
	require.Equal(t, "legacy", legacy.Generation)
	legacy, err = g.RefreshAvatar(ctx, o, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, "ready", legacy.State)
	outcome.Store(1)
	request = uuid.NewString()
	unknown, err := g.CreateAvatar(ctx, o, request, "结果待确认", true, photo.Bytes())
	require.NoError(t, err)
	require.Equal(t, "unknown", unknown.State)
	_, err = g.CreateAvatar(ctx, o, request, "结果待确认", true, photo.Bytes())
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load())
	_, err = g.CreateAvatar(ctx, o, uuid.NewString(), "重复制作", true, photo.Bytes())
	require.Error(t, err)
	require.EqualValues(t, 3, calls.Load())
	_, err = g.RefreshAvatar(ctx, o, unknown.ID)
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load(), "refresh must never repeat a mutating provider call")
}
