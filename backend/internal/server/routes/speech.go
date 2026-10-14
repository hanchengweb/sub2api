package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func registerSpeechGateway(r *gin.Engine, h *handler.SpeechHandler, auth middleware.APIKeyAuthMiddleware) {
	if h == nil {
		return
	}
	own := r.Group("/v1/audio", middleware.RequestBodyLimit(8192), gin.HandlerFunc(auth))
	own.GET("/voices", h.Voices)
	own.POST("/speech", h.Synthesize)
}
