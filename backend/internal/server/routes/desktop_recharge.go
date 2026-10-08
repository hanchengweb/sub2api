package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterDesktopRechargeRoutes(r *gin.Engine, h *handler.PaymentHandler, auth middleware.APIKeyAuthMiddleware) {
	group := r.Group("/v1/desktop/recharge", middleware.RequestBodyLimit(4096), gin.HandlerFunc(auth))
	group.GET("/checkout", h.DesktopRechargeCheckout)
	group.POST("/orders", h.CreateDesktopRecharge)
	group.GET("/orders/:request_id", h.ReadDesktopRecharge)
	group.POST("/orders/:request_id/cancel", h.ReadDesktopRecharge)
}
