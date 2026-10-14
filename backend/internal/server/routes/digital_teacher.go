package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func registerDigitalTeacherGateway(r *gin.Engine, h *handler.DigitalTeacherHandler, auth middleware.APIKeyAuthMiddleware) {
	if h == nil {
		return
	}
	sdk := r.Group("/v1/digital-teachers/sdk", middleware.RequestBodyLimit(4096))
	sdk.GET("/compose/webrtc/livekit", h.SimliStream)
	sdk.POST("/v1/sessions/start", h.SDK)
	sdk.POST("/v1/sessions/stop", h.SDK)
	sdk.POST("/v1/sessions/keep-alive", h.SDK)
	own := r.Group("/v1/digital-teachers", middleware.RequestBodyLimit(64*1024), gin.HandlerFunc(auth))
	own.GET("/settings", h.Settings)
	own.GET("/catalog", h.Catalog)
	own.GET("/avatars", h.AvatarJobs)
	own.GET("/avatars/:id", h.RefreshAvatar)
	own.PUT("/teachers", h.Save)
	own.POST("/sessions", h.Create)
	own.GET("/sessions", h.List)
	own.GET("/sessions/:id", h.Get)
	own.POST("/sessions/:id/stop", h.Stop)
	upload := r.Group("/v1/digital-teachers", middleware.RequestBodyLimit(6*1024*1024), gin.HandlerFunc(auth))
	upload.POST("/avatars", h.CreateAvatar)
}
func registerDigitalTeacherAdmin(r *gin.RouterGroup, h *handler.DigitalTeacherHandler) {
	if h == nil {
		return
	}
	admin := r.Group("/digital-teachers", middleware.RequestBodyLimit(64*1024))
	admin.GET("", h.AdminStatus)
	admin.PUT("/plans", h.SetPlan)
	admin.POST("/minutes", h.Grant)
	admin.GET("/minutes/:id", h.Minutes)
	admin.POST("/assets", h.BindAsset)
	admin.GET("/sessions", h.AdminSessions)
	admin.POST("/sessions/:id/reconcile", h.Reconcile)
}
