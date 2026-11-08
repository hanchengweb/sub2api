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
	own.GET("/voice-assets/catalog", h.VoiceAssetCatalog)
	own.GET("/voice-assets", h.VoiceAssetsList)
	own.POST("/voice-assets", h.VoiceAssetCreate)
	own.GET("/voice-assets/:id", h.VoiceAssetGet)
	own.PATCH("/voice-assets/:id", h.VoiceAssetPatch)
	own.DELETE("/voice-assets/:id", h.VoiceAssetDelete)
	own.POST("/voice-assets/:id/refresh", h.VoiceAssetRefresh)
	// Uploads have a separate bounded body; ordinary task JSON stays at 32 KiB.
	r.POST("/v1/voice-assets/clone", middleware.RequestBodyLimit((8<<20)+(16<<10)), gin.HandlerFunc(auth), h.VoiceAssetClone)
	for _, kind := range []string{mediatasks.Audio, mediatasks.Model3D} {
		if kind == mediatasks.Model3D {
			r.POST("/v1/model3d/generations", middleware.RequestBodyLimit(mediatasks.MaxModel3DRequestBytes), gin.HandlerFunc(auth), h.Create(kind))
		} else {
			own.POST("/"+kind+"/generations", h.Create(kind))
		}
		own.GET("/"+kind+"/:id", h.Get(kind))
		own.GET("/"+kind+"/:id/content", h.Content(kind))
	}
}
