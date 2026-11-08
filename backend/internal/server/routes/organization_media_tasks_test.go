package routes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelImageDecodeStore struct {
	mediatasks.Store
	finds int
}

func (s *modelImageDecodeStore) Find(context.Context, mediatasks.Owner, string, string) (*mediatasks.Task, error) {
	s.finds++
	return nil, mediatasks.ErrUnavailable
}

func TestModel3DImageBodyLimitAndClosedJSONContract(t *testing.T) {
	t.Setenv("WXM_OFFICIAL_MEDIA_ENABLED", "true")
	gin.SetMode(gin.TestMode)
	store := &modelImageDecodeStore{}
	h := handler.NewOrganizationMediaTaskHandler(store, nil, nil, nil, nil, nil)
	router := gin.New()
	registerOrganizationMediaTasks(router, h, middleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		group := int64(7)
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 2, UserID: 1, GroupID: &group,
			Status: service.StatusActive, User: &service.User{ID: 1, AccountType: "organization_service"}})
		c.Next()
	}))
	canvas := image.NewNRGBA(image.Rect(0, 0, 128, 128))
	_, err := rand.New(rand.NewSource(1)).Read(canvas.Pix)
	require.NoError(t, err)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, canvas))
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	body, err := json.Marshal(map[string]any{"model": mediatasks.TripoModel, "image": dataURL})
	require.NoError(t, err)
	require.Greater(t, len(body), 32<<10)

	for _, tc := range []struct {
		name, path, body string
		status           int
		reachedStore     bool
	}{
		{"image above old 32KiB", "/model3d/generations", string(body), 503, true},
		{"text preserved", "/model3d/generations", `{"model":"tripo-v3.1-standard","prompt":"a toy"}`, 503, true},
		{"supplier token forbidden", "/model3d/generations", `{"model":"tripo-v3.1-standard","prompt":"a toy","file_token":"file_private"}`, 400, false},
		{"remote image forbidden", "/model3d/generations", `{"model":"tripo-v3.1-standard","image":"https://example.invalid/a.png"}`, 422, false},
		{"image URL field forbidden", "/model3d/generations", `{"model":"tripo-v3.1-standard","prompt":"a toy","image_url":"https://example.invalid/a.png"}`, 400, false},
		{"file field forbidden", "/model3d/generations", `{"model":"tripo-v3.1-standard","prompt":"a toy","file":{}}`, 400, false},
		{"over request bound", "/model3d/generations", strings.Repeat(" ", mediatasks.MaxModel3DRequestBytes+1) + string(body), 400, false},
		{"audio bound unchanged", "/audio/generations", `{"model":"seed-tts-2.0","input":"` + strings.Repeat("a", 33<<10) + `"}`, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := store.finds
			req := httptest.NewRequest(http.MethodPost, "/v1"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-Client-Request-ID", "123e4567-e89b-42d3-a456-426614174000")
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.Equal(t, tc.reachedStore, store.finds > before)
			require.NotContains(t, response.Body.String(), dataURL)
		})
	}
}
