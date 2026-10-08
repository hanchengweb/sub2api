package handler

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

var desktopRechargeUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func desktopRechargeKey(c *gin.Context) *service.APIKey {
	c.Header("Cache-Control", "no-store")
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		response.Unauthorized(c, "个人连接不可用")
		return nil
	}
	if err := service.ValidateDesktopRechargeKey(key); err != nil {
		response.ErrorFrom(c, err)
		return nil
	}
	return key
}

func (h *PaymentHandler) DesktopRechargeCheckout(c *gin.Context) {
	key := desktopRechargeKey(c)
	if key == nil {
		return
	}
	result, err := h.paymentService.DesktopRechargeCheckout(c.Request.Context(), key)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *PaymentHandler) CreateDesktopRecharge(c *gin.Context) {
	key := desktopRechargeKey(c)
	if key == nil {
		return
	}
	requestID := c.GetHeader("Idempotency-Key")
	if !desktopRechargeUUID.MatchString(requestID) {
		response.BadRequest(c, "有效的充值请求标识不可缺少")
		return
	}
	var input service.DesktopRechargeInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "充值金额格式错误")
		return
	}
	coordinator := service.DefaultIdempotencyCoordinator()
	if coordinator == nil {
		response.ErrorFrom(c, service.ErrIdempotencyStoreUnavail)
		return
	}
	result, err := coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
		Scope: fmt.Sprintf("desktop.recharge.key:%d", key.ID), ActorScope: fmt.Sprintf("user:%d", key.UserID),
		Method: "POST", Route: c.FullPath(), IdempotencyKey: requestID, Payload: input, RequireKey: true, TTL: 24 * time.Hour,
	}, func(ctx context.Context) (any, error) {
		return h.paymentService.CreateDesktopRecharge(ctx, key, requestID, input, c.ClientIP())
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result.Data)
}

func (h *PaymentHandler) ReadDesktopRecharge(c *gin.Context) {
	key := desktopRechargeKey(c)
	if key == nil {
		return
	}
	requestID := c.Param("request_id")
	if !desktopRechargeUUID.MatchString(requestID) {
		response.BadRequest(c, "充值请求标识无效")
		return
	}
	result, err := h.paymentService.ReadDesktopRecharge(c.Request.Context(), key, requestID, c.Request.Method == "POST")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
