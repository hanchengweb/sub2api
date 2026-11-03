package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func registerOrganizationMediaTasks(r *gin.Engine, h *handler.OrganizationMediaTaskHandler, auth middleware.APIKeyAuthMiddleware) {
	if h == nil {
		return
	}
	own := r.Group("/v1", middleware.RequestBodyLimit(32<<10), gin.HandlerFunc(auth))
	own.GET("/media/models", h.Models)
	for _, kind := range []string{mediatasks.Audio, mediatasks.Model3D} {
		own.POST("/"+kind+"/generations", h.Create(kind))
		own.GET("/"+kind+"/:id", h.Get(kind))
		own.GET("/"+kind+"/:id/content", h.Content(kind))
	}
}
