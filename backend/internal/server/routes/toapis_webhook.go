package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"

	"github.com/gin-gonic/gin"
)

// RegisterToAPIsWebhookRoutes 注册 ToAPIs 异步任务 Webhook。
//
// 刻意不挂任何鉴权中间件：上游不带我们的 API key / JWT，身份完全由
// HMAC-SHA256 签名验证（handler 内校验，密钥未配置时直接 404）。
// 这与 /payment/webhook 一组的处理方式一致。
func RegisterToAPIsWebhookRoutes(v1 *gin.RouterGroup, h *handler.Handlers) {
	webhook := v1.Group("/webhooks/toapis")
	{
		webhook.POST("/tasks", h.OpenAIGateway.ToAPIsTaskWebhook)
	}
}
