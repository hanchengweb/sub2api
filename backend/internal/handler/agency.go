package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// AgencyHandler 「成为代理」的合作申请。
//
// 此前这个页面是纯前端：表单只在本地拼一段文案让用户自己复制走，
// 提交按钮不发任何请求——申请根本进不到平台。
type AgencyHandler struct {
	service *service.AgencyApplicationService
}

func NewAgencyHandler(s *service.AgencyApplicationService) *AgencyHandler {
	return &AgencyHandler{service: s}
}

type agencySubmitRequest struct {
	Direction   string `json:"direction"`
	ContactName string `json:"contact_name"`
	Email       string `json:"email"`
	Company     string `json:"company"`
	Scenario    string `json:"scenario"`
}

// Submit 提交合作申请。
func (h *AgencyHandler) Submit(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agency applications are unavailable"}})
		return
	}
	var req agencySubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	app := &service.AgencyApplication{
		Direction:   req.Direction,
		ContactName: req.ContactName,
		Email:       req.Email,
		Company:     req.Company,
		Scenario:    req.Scenario,
	}
	switch err := h.service.Submit(c.Request.Context(), subject.UserID, app); {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"data": app})
	case errors.Is(err, service.ErrAgencyTooManyPending):
		// 明确区分于参数错误：前端要提示「已有申请在处理中」，而不是「填写有误」。
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{
			"code":    "TOO_MANY_PENDING",
			"message": "已有申请正在处理中，请等待平台联系",
		}})
	case errors.Is(err, service.ErrAgencyApplicationInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid application"}})
	default:
		requestLogger(c, "handler.agency").Warn("agency.submit_failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to submit application"}})
	}
}

// ListMine 用户查看自己提交过的申请。
//
// 提交完什么都看不到的话，用户只会反复再提交一遍——这条接口是提交功能的一部分。
func (h *AgencyHandler) ListMine(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agency applications are unavailable"}})
		return
	}
	list, err := h.service.ListMine(c.Request.Context(), subject.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load applications"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// AdminList 管理员分页查看全部申请。
func (h *AgencyHandler) AdminList(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agency applications are unavailable"}})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	list, total, err := h.service.List(c.Request.Context(), service.AgencyApplicationFilters{
		Status: strings.TrimSpace(c.Query("status")),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		if errors.Is(err, service.ErrAgencyApplicationInvalid) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid filters"}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load applications"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "total": total})
}

// AdminUpdate 管理员改处理状态与备注。
func (h *AgencyHandler) AdminUpdate(c *gin.Context) {
	if h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agency applications are unavailable"}})
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid id"}})
		return
	}
	var req struct {
		Status    string `json:"status"`
		AdminNote string `json:"admin_note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	if err := h.service.UpdateStatus(c.Request.Context(), id, req.Status, req.AdminNote); err != nil {
		if errors.Is(err, service.ErrAgencyApplicationInvalid) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid status"}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to update application"}})
		return
	}
	c.Status(http.StatusNoContent)
}
