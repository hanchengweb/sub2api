package handler

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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

// playgroundPersistMaxItems 一次转存请求最多处理几个地址。
// 生图一次最多 4 张，留一倍余量；再多就是异常调用。
const playgroundPersistMaxItems = 8

// playgroundUploadMaxBody 参考图上传的请求体上限。
//
// 必须比 playgroundMaxBody 大：网关那侧允许 10MB
// （service.GrokMediaImageUploadMaxBytes），代理这侧若仍按 4MB 截断，
// 超过 4MB 的图会被**悄悄截成半张**再转发上去——上游只会报「不是合法图片」，
// 现场根本看不出是代理砍的。多留 64KB 给 multipart 的边界与字段头。
const playgroundUploadMaxBody = (10 << 20) + 64<<10

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
	mediaService   *service.PlaygroundMediaService
	convService    *service.PlaygroundConversationService
}

func NewPlaygroundHandler(
	engine *gin.Engine,
	apiKeyService *service.APIKeyService,
	settingService *service.SettingService,
	mediaService *service.PlaygroundMediaService,
	convService *service.PlaygroundConversationService,
) *PlaygroundHandler {
	return &PlaygroundHandler{
		engine:         engine,
		apiKeyService:  apiKeyService,
		settingService: settingService,
		mediaService:   mediaService,
		convService:    convService,
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
	h.proxyToGatewayLimited(c, gatewayPath, playgroundMaxBody)
}

// proxyToGatewayLimited 同 proxyToGateway，但允许按路由放宽请求体上限。
// 只有参考图上传需要它——别的路由都只传参数，4MB 绰绰有余。
func (h *PlaygroundHandler) proxyToGatewayLimited(c *gin.Context, gatewayPath string, maxBody int64) {
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
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody))
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

// mediaProxyAllowedHosts 允许中转的上游媒体域名。
//
// **必须是白名单**：这个端点会用服务器身份去取任意 URL，放开就是一个 SSRF
// 漏洞——攻击者能借它探测内网、云元数据地址（169.254.169.254）等。
// 只放行已知会返回生成结果的域名。
var mediaProxyAllowedHosts = map[string]struct{}{
	"files.toapis.cn": {},
	"toapis.cn":       {},
}

// mediaProxyMaxBytes 单个媒体的大小上限。生图通常 1~5MB，视频大一些。
const mediaProxyMaxBytes = 64 << 20

// Media 把上游生成结果中转一层，用本站域名回给浏览器。
//
// 为什么需要：上游把图片放在 files.toapis.cn，部分网络环境访问不到那个域名
// （用户换了出口 IP 后就打不开了，页面上只剩一个碎图图标）。服务器侧一直是通的，
// 所以由服务器代取再吐给浏览器，结果地址就始终可达。
//
// 只做 GET 转发，不改内容；带上 Content-Type 与缓存头，让浏览器正常缓存图片。
func (h *PlaygroundHandler) Media(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("url"))
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "missing url"}})
		return
	}
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "https" {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "url must be a valid https address"}})
		return
	}
	if _, ok := mediaProxyAllowedHosts[strings.ToLower(target.Hostname())]; !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{"message": "host is not allowed"}})
		return
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "build upstream request failed"}})
		return
	}
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "fetch upstream media failed"}})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"message": "upstream returned " + resp.Status}})
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Header("Content-Type", ct)
	// 生成结果不会变，让浏览器长期缓存，避免每次渲染都回源。
	c.Header("Cache-Control", "private, max-age=86400")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, io.LimitReader(resp.Body, mediaProxyMaxBytes))
}

// PersistMedia 把一批上游生成结果转存到本地盘，返回本站的永久地址。
//
// 为什么需要：上游结果 24 小时后过期。前端把地址存进会话也没用——
// 过一天那个地址就 404 了，用户会以为是我们把图弄丢了。
//
// 由前端在任务完成时调用，而不是在网关里自动做：网关那条路径是
// engine.HandleContext 直接把响应写给客户端的，中途插不进转存这一步。
//
// 逐条独立处理：一张图转存失败不该让整批都拿不到结果，失败的那条回退
// 到原地址（还能撑 24 小时），前端照常显示。
func (h *PlaygroundHandler) PersistMedia(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.mediaService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "media persistence is unavailable"}})
		return
	}

	var req struct {
		Urls   []string `json:"urls"`
		Kind   string   `json:"kind"`
		TaskID string   `json:"task_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	if len(req.Urls) == 0 || len(req.Urls) > playgroundPersistMaxItems {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "urls must contain 1 to 8 items"}})
		return
	}

	reqLog := requestLogger(c, "handler.playground")
	items := make([]gin.H, 0, len(req.Urls))
	for _, src := range req.Urls {
		media, err := h.mediaService.Persist(c.Request.Context(), subject.UserID, src, req.Kind, req.TaskID)
		if err != nil {
			// 只记日志不中断：磁盘紧张、上游已过期、类型不认识都可能走到这里，
			// 用户这一刻仍然该看得见他刚生成的图。
			reqLog.Warn("playground.media_persist_failed",
				zap.Int64("user_id", subject.UserID),
				zap.String("task_id", req.TaskID),
				zap.Error(err))
			items = append(items, gin.H{"source_url": src, "url": src, "persisted": false})
			continue
		}
		items = append(items, gin.H{
			"source_url": src,
			"url":        fmt.Sprintf("/api/v1/playground/media/%d", media.ID),
			"persisted":  true,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

// StoredMedia 回放一条已转存的生成结果。
//
// 归属校验在 SQL 的 WHERE 里做（GetOwned 带 user_id），不是查出来再比——
// 越权这种事不能指望调用方记得校验。别人的 id 一律当不存在。
func (h *PlaygroundHandler) StoredMedia(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.mediaService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "media persistence is unavailable"}})
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid media id"}})
		return
	}
	media, file, err := h.mediaService.Open(c.Request.Context(), subject.UserID, id)
	if err != nil || media == nil || file == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "media not found"}})
		return
	}
	defer func() { _ = file.Close() }()

	c.Header("Content-Type", media.MimeType)
	// 转存结果永不变更，让浏览器长期缓存；private 是因为它按登录态鉴权，
	// 不能被共享缓存留下来给别人。
	c.Header("Cache-Control", "private, max-age=31536000, immutable")
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, file)
}

// ListConversations 返回该用户的全部会话（含消息）。
//
// 会话搬到服务端之前只存浏览器 localStorage：换台电脑连列表都是空的，
// 196 那次把图片落了盘也照样看不到。
func (h *PlaygroundHandler) ListConversations(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.convService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "conversation sync is unavailable"}})
		return
	}
	convs, err := h.convService.List(c.Request.Context(), subject.UserID)
	if err != nil {
		requestLogger(c, "handler.playground").Warn("playground.conversations_list_failed",
			zap.Int64("user_id", subject.UserID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load conversations"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": convs})
}

// SaveConversation 整条覆盖写入一个会话。
//
// 覆盖而不是逐条增量：流式对话每秒要改好几次内容，逐条 diff 的复杂度远超收益。
// 会话 id 由前端生成，所以一律按 (user_id, id) 隔离——只按 id 写会让人改到别人的会话。
func (h *PlaygroundHandler) SaveConversation(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.convService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "conversation sync is unavailable"}})
		return
	}
	var conv service.PlaygroundConversation
	if err := c.ShouldBindJSON(&conv); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	// 路径里的 id 说了算，body 里的只是附带——两者不一致时以路径为准，
	// 否则可以拿一个自己的 id 去写另一条会话的内容。
	conv.ID = strings.TrimSpace(c.Param("id"))
	if err := h.convService.Save(c.Request.Context(), subject.UserID, &conv); err != nil {
		if errors.Is(err, service.ErrPlaygroundConversationInvalid) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid conversation"}})
			return
		}
		requestLogger(c, "handler.playground").Warn("playground.conversation_save_failed",
			zap.Int64("user_id", subject.UserID), zap.String("conversation_id", conv.ID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to save conversation"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"id": conv.ID}})
}

// DeleteConversation 删除一个会话及其消息。
func (h *PlaygroundHandler) DeleteConversation(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.convService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "conversation sync is unavailable"}})
		return
	}
	if err := h.convService.Delete(c.Request.Context(), subject.UserID, c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to delete conversation"}})
		return
	}
	c.Status(http.StatusNoContent)
}

// Models 列出该用户可调的模型。
//
// 必须走网关的 /v1/models，不能用模型广场：广场按定价配置拼装，会漏掉没配
// 定价行的模型（线上实测组 4 有 7 个可调模型，广场只回 6 个，漏的正是视频模型
// t-grok-video-1.5）。这里返回的就是密钥实际能调的那一份。
func (h *PlaygroundHandler) Models(c *gin.Context) {
	h.proxyToGateway(c, "/v1/models")
}

// UploadReferenceImage 上传参考图（图生视频用），转发到网关的 /v1/uploads/images。
//
// 为什么必须走代理而不是让网页端直连网关：网页端只有登录态 JWT，拿不到明文
// API key（只在创建时显示一次）。网关那个接口认的是 API key，所以得由代理
// 注入密钥。这也是上传能力早就做好、在线使用页却一直用不上的原因。
//
// multipart 请求体原样透传：Content-Type 连同 boundary 都不改动，
// 字段名与大小校验全部交给网关侧的 GrokImageUpload，不在这里重复一套。
func (h *PlaygroundHandler) UploadReferenceImage(c *gin.Context) {
	h.proxyToGatewayLimited(c, "/v1/uploads/images", playgroundUploadMaxBody)
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
