package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterPlaygroundRoutes 注册「在线使用」页的网关代理。
//
// 挂 JWT 而非 API key 鉴权：调用方是登录中的网页端，它拿不到明文密钥。
// 代理内部再取该用户自己的密钥去调网关，明文 key 不出浏览器。
func RegisterPlaygroundRoutes(v1 *gin.RouterGroup, h *handler.PlaygroundHandler, jwtAuth middleware.JWTAuthMiddleware) {
	pg := v1.Group("/playground")
	pg.Use(gin.HandlerFunc(jwtAuth))
	{
		pg.GET("/models", h.Models)
		// 媒体中转：上游图床在部分网络下不可达，由服务器代取（域名白名单防 SSRF）
		pg.GET("/media", h.Media)
		pg.POST("/chat/completions", h.ChatCompletions)
		pg.POST("/images/generations", h.ImageGenerations)
		pg.GET("/images/generations/:task_id", h.ImageStatus)
		// 参考图上传（图生视频）：网关的 /v1/uploads/images 认 API key，
		// 网页端只有 JWT，必须经这层代理注入密钥
		pg.POST("/uploads/images", h.UploadReferenceImage)
		pg.POST("/videos/generations", h.VideoGenerations)
		pg.GET("/videos/:request_id", h.VideoStatus)
	}
}
