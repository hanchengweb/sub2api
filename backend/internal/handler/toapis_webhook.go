package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// toAPIsWebhookProvider 是幂等表里的 provider 标识。
const toAPIsWebhookProvider = "toapis"

// toAPIsWebhookMaxBody 限制读入的请求体大小。事件体只有几百字节，
// 给 64KB 余量足够，同时避免被超大 body 拖住。
const toAPIsWebhookMaxBody = 64 << 10

// ToAPIsTaskWebhook 接收 ToAPIs 异步任务的最终状态事件。
//
// 为什么需要它：失败任务的退款原本只能靠客户端轮询状态接口触发，而生产环境该接口
// 的历史调用次数是 0——机制建好了却从不会被触发。接上 Webhook 后失败即退。
//
// 响应约定（按官方文档）：只有 2xx 算投递成功；4xx/5xx 都会按
// 10s/30s/2m/10m/1h/6h/24h 重投。所以：
//   - 验签失败 → 401，让它重试（多半是密钥配错，重试期间修好即可自愈）
//   - 重复事件、未知任务 → 200，这些重试再多次也不会有新结果
//   - 退款本身出错 → 500，让上游重投（此时幂等记录已回滚，重投能重新处理）
func (h *OpenAIGatewayHandler) ToAPIsTaskWebhook(c *gin.Context) {
	reqLog := requestLogger(c, "handler.webhook.toapis")
	ctx := c.Request.Context()

	enabled, secrets := h.gatewayService.ToAPIsWebhookConfig(ctx)
	if !enabled {
		// 未启用时不暴露端点存在与否的差异，统一 404。
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "not found"}})
		return
	}

	// 必须拿未经解析、未经重新序列化的原始字节：签名就是对它做的。
	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, toAPIsWebhookMaxBody))
	if err != nil {
		reqLog.Warn("toapis.webhook.read_body_failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}

	eventID := c.GetHeader(service.ToAPIsWebhookHeaderID)
	timestamp := c.GetHeader(service.ToAPIsWebhookHeaderTimestamp)
	signature := c.GetHeader(service.ToAPIsWebhookHeaderSignature)
	if !service.VerifyToAPIsWebhookSignature(eventID, timestamp, signature, rawBody, secrets, time.Now()) {
		reqLog.Warn("toapis.webhook.signature_invalid",
			zap.String("event_id", eventID),
			zap.String("timestamp", timestamp),
		)
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "invalid signature"}})
		return
	}

	var event service.ToAPIsWebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		// 验签已过，说明确实来自上游；解析不了就别让它无限重投。
		reqLog.Warn("toapis.webhook.decode_failed", zap.String("event_id", eventID), zap.Error(err))
		c.JSON(http.StatusOK, gin.H{"received": true, "ignored": "undecodable"})
		return
	}

	// 先落幂等记录再退款：上游是「至少一次」投递，同一事件重试时 id 不变，
	// 顺序反了就意味着一次重投等于一次重复退款。
	first, err := h.gatewayService.RecordWebhookEventOnce(ctx, toAPIsWebhookProvider, eventID, event.Type, event.Data.TaskID)
	if err != nil {
		reqLog.Error("toapis.webhook.record_event_failed", zap.String("event_id", eventID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "retry later"}})
		return
	}
	if !first {
		// 重复投递：直接 2xx，不重复执行副作用。
		c.JSON(http.StatusOK, gin.H{"received": true, "duplicate": true})
		return
	}

	if !event.IsTerminalFailure() {
		// completed / endpoint.test 没有可退金额，记录事件即可。
		c.JSON(http.StatusOK, gin.H{"received": true})
		return
	}

	credits, userID, refunded := h.gatewayService.RefundMediaTaskChargeByTaskID(ctx, event.Data.TaskID)
	if refunded {
		reqLog.Info("toapis.webhook.task_failed_refunded",
			zap.String("event_id", eventID),
			zap.String("task_id", event.Data.TaskID),
			zap.String("task_type", event.Data.TaskType),
			zap.Int64("user_id", userID),
			zap.Float64("credits", credits),
		)
	} else {
		// 未知任务、已退过、或该任务本就没扣费都会走到这里，都不是错误。
		// 盯「同一 task_id 首次出现却没退成」才有意义。
		reqLog.Warn("toapis.webhook.task_failed_no_charge_record",
			zap.String("event_id", eventID),
			zap.String("task_id", event.Data.TaskID),
		)
	}
	c.JSON(http.StatusOK, gin.H{"received": true, "refunded": refunded})
}
