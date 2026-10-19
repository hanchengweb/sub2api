package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterDesktopDriveRoutes(r *gin.Engine, h *handler.DesktopDriveHandler, auth middleware.APIKeyAuthMiddleware) {
	group := r.Group("/v1/desktop/drive", middleware.RequestBodyLimit(82<<20), gin.HandlerFunc(auth))
	group.GET("", h.Catalog)
	group.POST("", h.Write("create"))
	group.GET("/:resource_id", h.List)
	group.PATCH("/:resource_id", h.Write("rename"))
	group.DELETE("/:resource_id", h.Write("delete"))
	group.POST("/:resource_id/folders", h.Write("folder"))
	group.POST("/:resource_id/upload", h.Write("upload"))
	group.GET("/:resource_id/items/:item_id", h.Item)
	group.GET("/:resource_id/items/:item_id/original", h.Original)
	group.PATCH("/:resource_id/items/:item_id", h.Write("rename"))
	group.DELETE("/:resource_id/items/:item_id", h.Write("delete"))
}
