package handler

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// playgroundMaxBody 在线使用页的请求体上限。对话/生图参数都很小，
// 4MB 足以容纳参考图的 base64，同时挡住异常大的请求。
const playgroundMaxBody = 4 << 20

// PlaygroundHandler 为「在线使用」页提供网关代理。
//
// 存在的理由：网页端拿不到明文 API key（只在创建时显示一次），把 key 放进浏览器
// 也不安全。代理用登录态 JWT 认证，服务端内部取该用户自己的密钥去调网关。
//
// 关键设计——不复制计费链路：
// 代理只做「认 JWT → 取密钥 → 注入 Authorization → 交回同一套网关路由」，
// 计费、额度、分组、退款全部复用原有链路。若代理自己重写一套调用逻辑，就会出现
// 两套计费口径，而同一件事两套口径正是这个项目反复出问题的根源。
type PlaygroundHandler struct {
	engine         *gin.Engine
	apiKeyService  *service.APIKeyService
	settingService *service.SettingService
}

func NewPlaygroundHandler(
	engine *gin.Engine,
	apiKeyService *service.APIKeyService,
	settingService *service.SettingService,
) *PlaygroundHandler {
	return &PlaygroundHandler{
		engine:         engine,
		apiKeyService:  apiKeyService,
		settingService: settingService,
	}
}

// resolveUserKey 取该用户可用于在线体验的密钥。
//
// 选取规则：状态正常、未过期、额度未耗尽的第一把。优先试用密钥没有特殊处理——
// 用户自己建的密钥同样可用，体验页不该强制绑定某一把。
func (h *PlaygroundHandler) resolveUserKey(c *gin.Context, userID int64) (*service.APIKey, bool) {
	keys, _, err := h.apiKeyService.List(c.Request.Context(), userID,
		pagination.PaginationParams{Page: 1, PageSize: 50}, service.APIKeyListFilters{})
	if err != nil {
		return nil, false
	}
	for i := range keys {
		k := &keys[i]
		if k.Status != service.StatusActive || k.IsExpired() || k.IsQuotaExhausted() {
			continue
		}
		if strings.TrimSpace(k.Key) == "" {
			continue
		}
		return k, true
	}
	// 一把可用的都没有——补发试用密钥。
	//
	// 试用密钥功能上线前注册的老用户手上一把钥匙都没有，整个在线使用页对他们
	// 完全不可用：模型列表是空的，发什么都失败。让他们自己先去建密钥不合理，
	// 这页的卖点就是「开箱即用」。
	return h.ensureTrialKey(c, userID)
}

// ensureTrialKey 给没有任何可用密钥的用户补发一把试用密钥。
//
// 复用注册时那套配置（signup_trial_key_*），额度口径完全一致；开关关闭时不补发。
// 失败只记日志并返回 false，由调用方回 4xx，不把内部错误细节抛给前端。
func (h *PlaygroundHandler) ensureTrialKey(c *gin.Context, userID int64) (*service.APIKey, bool) {
	if h.apiKeyService == nil || h.settingService == nil {
		return nil, false
	}
	enabled, quota, groupID := h.settingService.SignupTrialKeyConfig(c.Request.Context())
	if !enabled || quota <= 0 {
		return nil, false
	}
	key, err := h.apiKeyService.Create(c.Request.Context(), userID, service.CreateAPIKeyRequest{
		Name:    service.SignupTrialKeyName,
		GroupID: groupID,
		Quota:   quota,
	})
	if err != nil {
		logger.LegacyPrintf("handler.playground",
			"[Playground] backfill trial key failed user=%d err=%v", userID, err)
		return nil, false
	}
	logger.LegacyPrintf("handler.playground",
		"[Playground] backfilled trial key user=%d key_id=%d quota=%.2f", userID, key.ID, quota)
	return key, true
}

// proxyToGateway 把当前请求改写成网关请求并重新派发。
//
// 用 engine.HandleContext 而不是自己发一次 HTTP 到 127.0.0.1：
// 流式响应由网关 handler 直接写进同一个 ResponseWriter，也就是真实的客户端连接，
// 不需要额外的管道转发，首字延迟也不会多一跳。
func (h *PlaygroundHandler) proxyToGateway(c *gin.Context, gatewayPath string) {
	reqLog := requestLogger(c, "handler.playground")

	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}

	key, ok := h.resolveUserKey(c, subject.UserID)
	if !ok {
		// 没有可用密钥（全部过期/停用/额度耗尽）——这是业务状态不是错误，
		// 前端据此引导用户去创建密钥或充值。
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"code":    "NO_USABLE_API_KEY",
			"message": "没有可用的 API 密钥，请先创建或充值",
		}})
		return
	}

	// 请求体要重新可读：网关链路上的中间件会再次读取它。
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, playgroundMaxBody))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))

	// 注入密钥并改写路径，交回网关路由。
	c.Request.Header.Set("Authorization", "Bearer "+key.Key)
	c.Request.URL.Path = gatewayPath

	reqLog.Debug("playground.proxy",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", key.ID),
		zap.String("gateway_path", gatewayPath),
	)

	h.engine.HandleContext(c)

	// 必须显式终止外层链路。
	//
	// HandleContext 会保存并恢复 c.index，所以内部路由跑完后，外层
	// /api/v1/playground/* 这条链路会从原处继续往下走——请求于是被处理两次。
	// 实测现象：响应体里正常 JSON 后面又拼了一段
	// {"error":{"message":"Request body is empty"}}（第二次进来时 body 已读空）。
	// Abort 只置终止标志，不写响应，网关 handler 已写好的内容不受影响。
	c.Abort()
}

// Models 列出该用户可调的模型。
//
// 必须走网关的 /v1/models，不能用模型广场：广场按定价配置拼装，会漏掉没配
// 定价行的模型（线上实测组 4 有 7 个可调模型，广场只回 6 个，漏的正是视频模型
// t-grok-video-1.5）。这里返回的就是密钥实际能调的那一份。
func (h *PlaygroundHandler) Models(c *gin.Context) {
	h.proxyToGateway(c, "/v1/models")
}

// ChatCompletions 在线对话（支持流式）。
func (h *PlaygroundHandler) ChatCompletions(c *gin.Context) {
	h.proxyToGateway(c, "/v1/chat/completions")
}

// ImageGenerations 在线生图（异步任务，返回 task id）。
func (h *PlaygroundHandler) ImageGenerations(c *gin.Context) {
	h.proxyToGateway(c, "/v1/images/generations")
}

// ImageStatus 查询生图任务状态。
//
// 与 VideoStatus 同理：异步生图失败后的退款也挂在状态查询这条路径上，
// 必须经由代理走用户自己的密钥。
func (h *PlaygroundHandler) ImageStatus(c *gin.Context) {
	taskID := strings.TrimSpace(c.Param("task_id"))
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "missing task_id"}})
		return
	}
	h.proxyToGateway(c, "/v1/images/generations/"+taskID)
}

// VideoGenerations 在线生视频（异步任务，返回 task id）。
func (h *PlaygroundHandler) VideoGenerations(c *gin.Context) {
	h.proxyToGateway(c, "/v1/videos/generations")
}

// VideoStatus 查询视频任务状态。
//
// 必须经由代理而不是让前端直连网关：失败任务的退款挂在这条路径上
// （grok_media.go 的 IsVideoLookupRequest 分支），前端直连就拿不到用户密钥。
func (h *PlaygroundHandler) VideoStatus(c *gin.Context) {
	requestID := strings.TrimSpace(c.Param("request_id"))
	if requestID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "missing request_id"}})
		return
	}
	h.proxyToGateway(c, "/v1/videos/"+requestID)
}
