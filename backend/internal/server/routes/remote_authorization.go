package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	mw "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func RegisterRemoteAuthorizationRoutes(r *gin.Engine, v1 *gin.RouterGroup, h *handler.AuthHandler, jwt mw.JWTAuthMiddleware, key mw.APIKeyAuthMiddleware, cache *redis.Client, keys *service.APIKeyService, settings *service.SettingService) {
	remote := h.RemoteAuthorization(cache, keys)
	limit := middleware.NewRateLimiter(cache)
	guard := func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	}
	g := v1.Group("/auth/remote", mw.RequestBodyLimit(4096), guard, mw.BackendModeAuthGuard(settings))
	g.POST("/authorize", gin.HandlerFunc(jwt), limit.LimitWithOptions("remote-authorize", 20, time.Minute, middleware.RateLimitOptions{FailureMode: middleware.RateLimitFailClose}), remote.Authorize)
	g.POST("/exchange", limit.LimitWithOptions("remote-exchange", 120, time.Minute, middleware.RateLimitOptions{FailureMode: middleware.RateLimitFailClose}), remote.Exchange)
	// Opaque grants are themselves authentication; a shared BFF IP must not make
	// unrelated users consume the same low login-rate allowance.
	g.POST("/inspect", remote.Inspect)
	g.POST("/revoke", remote.Revoke)
	r.POST("/v1/desktop/remote/proof", mw.RequestBodyLimit(4096), guard, gin.HandlerFunc(key), remote.Proof)
}
