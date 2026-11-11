package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Precharge through the same unified billing command before the only allowed POST.
func (h *OpenAIGatewayHandler) prepareCompShareH3(c *gin.Context, key *service.APIKey, account *service.Account, body []byte) bool {
	id := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if id == "" {
		id = strings.TrimSpace(c.GetHeader("X-Client-Request-ID"))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 30*time.Second)
	defer cancel()
	snap, err := h.gatewayService.PrepareCompShareH3(ctx, key, account, body, id)
	if err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return false
	}
	if snap.AccountID != account.ID {
		h.errorResponse(c, 409, "h3_original_account_required", "Query the original request")
		return false
	}
	if snap.Dispatched || snap.Settled {
		taskID := snap.TaskID
		if taskID == "" {
			taskID = snap.RequestID
		}
		status := "unknown"
		if snap.Settled && snap.TaskID == "" {
			status = "failed"
		}
		c.Header("X-Media-Billing-Mode", snap.BillingMode())
		c.JSON(202, gin.H{"id": taskID, "request_id": taskID, "status": status, "billing_mode": snap.BillingMode(), "idempotent_replay": true, "message": "Query the original request"})
		return false
	}
	result := &service.OpenAIForwardResult{RequestID: snap.RequestID, Model: service.CompShareH3Model, BillingModel: service.CompShareH3Model, UpstreamModel: service.CompShareH3Model, VideoCount: 1, VideoResolution: snap.Resolution, VideoDurationSeconds: snap.Duration, CompShareH3: snap}
	if err = h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{Result: result, APIKey: key, User: key.User, Account: account, APIKeyService: h.apiKeyService, RequestPayloadHash: snap.PayloadHash, InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: "/minimax/v2/video_generation", QuotaPlatform: service.QuotaPlatform(c.Request.Context(), key)}); err != nil {
		if isH3MediaQuotaError(err) {
			mediaQuotaError(c, err)
			return false
		}
		if errors.Is(err, service.ErrInsufficientBalance) {
			h.errorResponse(c, 402, "insufficient_balance", "Insufficient balance for the H3 reservation")
			return false
		}
		c.JSON(502, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "unknown", "error": gin.H{"code": "h3_submission_unknown", "message": "Query the original request; billing receipt needs recovery before dispatch"}})
		return false
	}
	c.Header("X-Media-Billing-Mode", snap.BillingMode())
	claimed, err := h.gatewayService.ClaimCompShareH3(ctx, snap)
	if isH3MediaQuotaError(err) {
		mediaQuotaError(c, err)
		return false
	}
	if err != nil || !claimed {
		c.JSON(409, gin.H{"id": snap.RequestID, "request_id": snap.RequestID, "status": "unknown", "error": gin.H{"code": "h3_request_already_dispatched", "message": "Query the original request; do not resubmit"}})
		return false
	}
	c.Set(service.CompShareH3ContextKey, snap)
	return true
}

func isH3MediaQuotaError(err error) bool {
	return errors.Is(err, service.ErrMediaQuotaExceeded) || errors.Is(err, service.ErrMediaQuotaConflict) || errors.Is(err, service.ErrMediaQuotaReview)
}
