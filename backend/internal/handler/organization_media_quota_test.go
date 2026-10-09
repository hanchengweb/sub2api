package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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
