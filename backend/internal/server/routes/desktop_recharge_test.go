package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDesktopRechargeRouteAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := &service.APIKey{ID: 71, UserID: 1, Key: "fixture-private", Status: service.StatusActive, User: &service.User{ID: 1, Status: service.StatusActive, Balance: 0}}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keySvc := service.NewAPIKeyService(&keyBillingRouteAPIKeyRepo{apiKey: key}, nil, nil, nil, &keyBillingRouteRateRepo{}, nil, cfg)
	r := gin.New()
	RegisterDesktopRechargeRoutes(r, handler.NewPaymentHandler(nil, nil), middleware.NewAPIKeyAuthMiddleware(keySvc, nil, cfg))
	for _, test := range []struct {
		name, key, path string
		status          int
	}{
		{"missing key", "", "/v1/desktop/recharge/orders", 401},
		{"bad key", "unknown", "/v1/desktop/recharge/orders", 401},
		{"zero balance gets validation", "fixture-private", "/v1/desktop/recharge/orders", 400},
		{"no login surface", "fixture-private", "/v1/desktop/recharge/login", 404},
		{"no refund surface", "fixture-private", "/v1/desktop/recharge/orders/1/refund", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(`{"amount":10}`))
			req.Header.Set("Content-Type", "application/json")
			if test.key != "" {
				req.Header.Set("Authorization", "Bearer "+test.key)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, test.status, w.Code, w.Body.String())
		})
	}
	old := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(old) })
	req := httptest.NewRequest(http.MethodPost, "/v1/desktop/recharge/orders", strings.NewReader(`{"amount":10}`))
	req.Header.Set("Authorization", "Bearer "+key.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 503, w.Code)
}
