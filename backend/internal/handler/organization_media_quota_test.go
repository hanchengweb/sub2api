package handler

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mediaQuotaStub struct {
	enabled                 bool
	reserveErr, dispatchErr error
	replay                  *service.MediaQuotaReservation
	claims                  []service.MediaQuotaClaim
	settlements             []int64
	measurements            []string
	bindings                []string
}

func (s *mediaQuotaStub) Enabled(context.Context, int64) (bool, error) { return s.enabled, nil }
func (s *mediaQuotaStub) Grant(context.Context, int64, service.MediaQuotaGrant) (int64, error) {
	return 1, nil
}
func (s *mediaQuotaStub) Summary(context.Context, int64, int) (*service.MediaQuotaSummary, error) {
	return &service.MediaQuotaSummary{}, nil
}
func (s *mediaQuotaStub) Reserve(_ context.Context, c service.MediaQuotaClaim) (*service.MediaQuotaReservation, error) {
	s.claims = append(s.claims, c)
	if s.reserveErr != nil {
		return nil, s.reserveErr
	}
	if s.replay != nil {
		return s.replay, nil
	}
	return &service.MediaQuotaReservation{RequestID: c.RequestID}, nil
}
func (s *mediaQuotaStub) Bind(_ context.Context, _ int64, _, task string) error {
	s.bindings = append(s.bindings, task)
	return nil
}
func (s *mediaQuotaStub) Settle(_ context.Context, _ int64, _, _ string, n int64, m string) error {
	s.settlements = append(s.settlements, n)
	s.measurements = append(s.measurements, m)
	return nil
}
func (s *mediaQuotaStub) ValidateDispatch(context.Context, int64, string) error { return s.dispatchErr }
func (s *mediaQuotaStub) FailVerifiedTask(context.Context, string) error        { return nil }
func quotaRouter(stub *mediaQuotaStub, account string, run func(*OpenAIGatewayHandler, *gin.Context)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &OpenAIGatewayHandler{mediaQuota: stub}
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{ID: 7, UserID: 1, User: &service.User{ID: 1, AccountType: account}})
	})
	r.Use(h.OrganizationMediaQuotaMiddleware())
	r.POST("/v1/images/generations", func(c *gin.Context) { run(h, c) })
	r.POST("/v1/videos/generations", func(c *gin.Context) { run(h, c) })
	r.POST("/v1/responses", func(c *gin.Context) { run(h, c) })
	return r
}
func quotaRequest(r http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client-Request-ID", "fixture-generation-request")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func TestOrganizationMediaQuotaStopsBeforeSupplier(t *testing.T) {
	s := &mediaQuotaStub{enabled: true, reserveErr: service.ErrMediaQuotaExceeded}
	called := 0
	r := quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) { called++ })
	w := quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1","prompt":"fixture","n":1}`)
	require.Equal(t, 429, w.Code)
	require.Zero(t, called)
	require.Empty(t, s.settlements)
	s.reserveErr = nil
	s.dispatchErr = service.ErrMediaQuotaExceeded
	r = quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) {
		if h.beginMediaQuotaDispatch(c) {
			called++
		}
	})
	w = quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1","prompt":"fixture"}`)
	require.Equal(t, 429, w.Code)
	require.Zero(t, called)
	require.Equal(t, []int64{0}, s.settlements)
}
func TestOrganizationMediaQuotaUnknownDoesNotRetryOrRelease(t *testing.T) {
	s := &mediaQuotaStub{enabled: true}
	called := 0
	r := quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) {
		if h.beginMediaQuotaDispatch(c) {
			called++
		}
		require.False(t, h.beginMediaQuotaDispatch(c))
	})
	w := quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1","prompt":"fixture"}`)
	require.Equal(t, 409, w.Code)
	require.Equal(t, 1, called)
	require.Empty(t, s.settlements)
	r = quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) {
		require.True(t, h.beginMediaQuotaDispatch(c))
		c.JSON(502, gin.H{"error": "timeout"})
	})
	require.Equal(t, 502, quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1"}`).Code)
	require.Empty(t, s.settlements)
}
func TestOrganizationMediaQuotaOutputFailureAndReplay(t *testing.T) {
	s := &mediaQuotaStub{enabled: true}
	r := quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) {
		require.True(t, h.beginMediaQuotaDispatch(c))
		c.JSON(200, gin.H{"status": "failed", "data": []gin.H{{"url": "https://fixture/1"}, {"url": "https://fixture/2"}}})
	})
	require.Equal(t, 200, quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1","n":3}`).Code)
	require.Equal(t, []int64{2}, s.settlements)
	s.replay = &service.MediaQuotaReservation{Replay: true, TaskID: "original-task", Status: "submitted"}
	w := quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1","n":3}`)
	require.Equal(t, 202, w.Code)
	require.Contains(t, w.Body.String(), "original-task")
	require.Len(t, s.settlements, 1)
	s.replay = nil
	r = quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) {
		require.True(t, h.beginMediaQuotaDispatch(c))
		c.JSON(200, gin.H{"id": "failed-task", "status": "failed"})
	})
	require.Equal(t, 200, quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1"}`).Code)
	require.Equal(t, []int64{2, 0}, s.settlements)
}
func TestOrganizationMediaQuotaBoundsAndPersonalUnaffected(t *testing.T) {
	s := &mediaQuotaStub{enabled: true}
	calls := 0
	r := quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) { calls++; c.Status(200) })
	for _, body := range []string{`{"model":"seedream-4","n":1,"sequential_image_generation":"auto"}`, `{"model":"gpt-image-1","stream":true}`, `{"model":"gpt-image-1","n":1.5}`} {
		require.GreaterOrEqual(t, quotaRequest(r, "/v1/images/generations", body).Code, 400)
	}
	require.Equal(t, 422, quotaRequest(r, "/v1/responses", `{"model":"gpt-4.1","tools":[{"type":"image_generation"}]}`).Code)
	require.Equal(t, 422, quotaRequest(r, "/v1/videos/generations", `{"model":"seedance-2-fast","duration":-1}`).Code)
	require.Zero(t, calls)
	r = quotaRouter(s, "personal", func(h *OpenAIGatewayHandler, c *gin.Context) { calls++; c.Status(200) })
	require.Equal(t, 200, quotaRequest(r, "/v1/images/generations", `{"model":"gpt-image-1","stream":true}`).Code)
	require.Equal(t, 1, calls)
	require.Empty(t, s.claims)
}

func TestOrganizationMediaQuotaAcceptsSeedreamWatermarkWithoutChangingCount(t *testing.T) {
	for _, flag := range []string{"true", "false"} {
		s := &mediaQuotaStub{enabled: true}
		r := quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) {
			require.True(t, h.beginMediaQuotaDispatch(c))
			c.JSON(200, gin.H{"data": []gin.H{{"url": "https://fixture/image"}}})
		})
		body := `{"model":"doubao-seedream-5-0-pro","prompt":"fixture","n":1,"size":"1:1","metadata":{"resolution":"1K","watermark":` + flag + `}}`
		require.Equal(t, 200, quotaRequest(r, "/v1/images/generations", body).Code)
		require.Len(t, s.claims, 1)
		require.Equal(t, int64(1), s.claims[0].Units)
		require.Equal(t, []int64{1}, s.settlements)
	}
	s := &mediaQuotaStub{enabled: true}
	r := quotaRouter(s, "organization_service", func(_ *OpenAIGatewayHandler, _ *gin.Context) {
		t.Fatal("invalid metadata reached the supplier")
	})
	for _, metadata := range []string{`{"watermark":"false"}`, `{"watermark":1}`, `{"watermark":false,"sequential_image_generation":"auto"}`} {
		require.Equal(t, 422, quotaRequest(r, "/v1/images/generations", `{"model":"doubao-seedream-5-0-pro","n":1,"metadata":`+metadata+`}`).Code)
	}
	require.Empty(t, s.claims)
}

func TestOrganizationMediaQuotaSeedanceFirstFrame(t *testing.T) {
	for _, model := range []string{"seedance-2", "seedance-2-fast", "seedance-2-mini"} {
		for _, image := range []string{"https://fixture/frame.png", "data:image/png;base64,aW1hZ2U="} {
			t.Run(model+"/"+strings.SplitN(image, ":", 2)[0], func(t *testing.T) {
				s := &mediaQuotaStub{enabled: true}
				calls := 0
				r := quotaRouter(s, "organization_service", func(h *OpenAIGatewayHandler, c *gin.Context) {
					require.True(t, h.beginMediaQuotaDispatch(c))
					calls++
					body, err := io.ReadAll(c.Request.Body)
					require.NoError(t, err)
					var forwarded struct {
						Duration int `json:"duration"`
						Images   []struct {
							URL  string `json:"url"`
							Role string `json:"role"`
						} `json:"image_with_roles"`
					}
					require.NoError(t, json.Unmarshal(body, &forwarded))
					require.Equal(t, 5, forwarded.Duration)
					require.Len(t, forwarded.Images, 1)
					require.Equal(t, image, forwarded.Images[0].URL)
					require.Equal(t, "first_frame", forwarded.Images[0].Role)
					c.JSON(202, gin.H{"id": "first-frame-task", "status": "submitted"})
				})
				body := `{"model":"` + model + `","prompt":"fixture","duration":5,"aspect_ratio":"adaptive","image_with_roles":[{"url":"` + image + `","role":"first_frame"}]}`
				require.Equal(t, 202, quotaRequest(r, "/v1/videos/generations", body).Code)
				require.Len(t, s.claims, 1)
				require.Equal(t, int64(5), s.claims[0].Units)
				require.Equal(t, []string{"first-frame-task"}, s.bindings)
				require.Empty(t, s.settlements)
				s.replay = &service.MediaQuotaReservation{Replay: true, TaskID: "first-frame-task", Status: "submitted"}
				w := quotaRequest(r, "/v1/videos/generations", body)
				require.Equal(t, 202, w.Code)
				require.Contains(t, w.Body.String(), "first-frame-task")
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestOrganizationMediaQuotaSeedanceFrameKeepsBounds(t *testing.T) {
	frame := `,"image_with_roles":[{"url":"https://fixture/frame.png","role":"first_frame"}]`
	for _, fields := range []string{
		`"model":"seedance-2-fast","duration":-1`,
		`"model":"seedance-2-fast","duration":5.5`,
		`"model":"seedance-2-fast","duration":5,"n":2`,
		`"model":"seedance-2-fast","duration":5,"num_outputs":2`,
		`"model":"seedance-2-fast","duration":5,"stream":true`,
		`"model":"seedance-2-fast","duration":5,"metadata":{"num_outputs":2}`,
		`"model":"unknown-video","duration":5`,
	} {
		s := &mediaQuotaStub{enabled: true}
		r := quotaRouter(s, "organization_service", func(_ *OpenAIGatewayHandler, _ *gin.Context) {
			t.Fatal("unbounded request reached the supplier")
		})
		require.GreaterOrEqual(t, quotaRequest(r, "/v1/videos/generations", "{"+fields+frame+"}").Code, 400)
		require.Empty(t, s.claims)
	}
	s := &mediaQuotaStub{enabled: true, reserveErr: service.ErrMediaQuotaExceeded}
	r := quotaRouter(s, "organization_service", func(_ *OpenAIGatewayHandler, _ *gin.Context) {
		t.Fatal("quota rejection reached the supplier")
	})
	body := `{"model":"seedance-2-fast","duration":5` + frame + `}`
	require.Equal(t, 429, quotaRequest(r, "/v1/videos/generations", body).Code)
	require.Empty(t, s.settlements)
	require.Equal(t, 422, quotaRequest(r, "/v1/images/generations", body).Code)
}
