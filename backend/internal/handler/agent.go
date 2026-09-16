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

// AgentHandler 代理身份与代理定价。
//
// 198 迁移建了合作申请表，但 status='accepted' 一直只是个状态字——
// 审核通过之后平台侧什么都不会发生。这个 handler 补上「通过之后」的那一半：
// 代理档案、代理列表、以及面向代理的批发价怎么算出来。
type AgentHandler struct {
	agents      *service.AgentService
	plans       *service.AgentPricingPlanService
	channels    *service.ChannelService
	payments    *service.PaymentConfigService
	settlements *service.AgentSettlementService
}

func NewAgentHandler(
	agents *service.AgentService,
	plans *service.AgentPricingPlanService,
	channels *service.ChannelService,
	payments *service.PaymentConfigService,
	settlements *service.AgentSettlementService,
) *AgentHandler {
	return &AgentHandler{
		agents:      agents,
		plans:       plans,
		channels:    channels,
		payments:    payments,
		settlements: settlements,
	}
}

// AdminSettleAgent 给一个代理结算一次返现。
//
// 手动触发而不是定时任务：第一版先让运营决定什么时候结、结完看数对不对，
// 跑顺了再挂定时。自动发钱在没人核对过一次之前不该开。
func (h *AgentHandler) AdminSettleAgent(c *gin.Context) {
	if h.settlements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent settlement is unavailable"}})
		return
	}
	userID, ok := parseAgentUserIDParam(c)
	if !ok {
		return
	}
	creditsPerCNY, err := h.creditsPerCNY(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code":    "CREDIT_RATE_UNAVAILABLE",
			"message": "取不到积分汇率，无法把让利金额折算成积分",
		}})
		return
	}

	settlement, err := h.settlements.SettleAgent(c.Request.Context(), userID, creditsPerCNY)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"data": settlement})
	case errors.Is(err, service.ErrAgentSettlementNothingToDo):
		// 区分于失败：上次结算之后没有新消费是正常状态，不是错误。
		c.JSON(http.StatusOK, gin.H{"data": nil, "message": "no new usage to settle"})
	case errors.Is(err, service.ErrAgentProfileNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "agent not found or not active"}})
	case errors.Is(err, service.ErrAgentSettlementInvalid), errors.Is(err, service.ErrAgentPricingPlanInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
	default:
		requestLogger(c, "handler.agent").Warn("agent.settle_failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to settle: " + err.Error()}})
	}
}

// AdminListAgentSettlements 管理员查某个代理的结算历史。
func (h *AgentHandler) AdminListAgentSettlements(c *gin.Context) {
	userID, ok := parseAgentUserIDParam(c)
	if !ok {
		return
	}
	h.writeSettlements(c, userID)
}

// ListMySettlements 代理查自己的结算历史。
//
// 走登录态里的 user id，不接受路径参数——否则改个数字就能看别人的账。
func (h *AgentHandler) ListMySettlements(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	h.writeSettlements(c, subject.UserID)
}

func (h *AgentHandler) writeSettlements(c *gin.Context, agentUserID int64) {
	if h.settlements == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent settlement is unavailable"}})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	list, total, err := h.settlements.ListByAgent(c.Request.Context(), agentUserID, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load settlements"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "total": total})
}

// agentProfileResponse 代理自己看到的档案。
//
// 刻意不包含 note：那是平台内部备注（「这家欠过款」之类），
// 直接把 service.AgentProfile 序列化出去就会漏给代理本人。
type agentProfileResponse struct {
	Mode            string `json:"mode"`
	Direction       string `json:"direction"`
	Status          string `json:"status"`
	PricingPlanID   *int64 `json:"pricing_plan_id,omitempty"`
	ResellerGroupID *int64 `json:"reseller_group_id,omitempty"`
	ActivatedAt     string `json:"activated_at"`
}

// GetMine 代理查看自己的身份。
//
// 不是代理时回 404 而不是空对象：前端据此决定显示「申请入口」还是「代理面板」，
// 一个字段全空的对象会让这个判断变成猜。
func (h *AgentHandler) GetMine(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "unauthorized"}})
		return
	}
	if h.agents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent service is unavailable"}})
		return
	}
	profile, err := h.agents.Get(c.Request.Context(), subject.UserID)
	switch {
	case errors.Is(err, service.ErrAgentProfileNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"code":    "NOT_AN_AGENT",
			"message": "尚未开通代理",
		}})
		return
	case err != nil:
		requestLogger(c, "handler.agent").Warn("agent.get_mine_failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load agent profile"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": agentProfileResponse{
		Mode:            profile.Mode,
		Direction:       profile.Direction,
		Status:          profile.Status,
		PricingPlanID:   profile.PricingPlanID,
		ResellerGroupID: profile.ResellerGroupID,
		ActivatedAt:     profile.ActivatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}})
}

// AdminList 管理员分页查看代理。
func (h *AgentHandler) AdminList(c *gin.Context) {
	if h.agents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent service is unavailable"}})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	list, total, err := h.agents.List(c.Request.Context(), service.AgentProfileFilters{
		Status: strings.TrimSpace(c.Query("status")),
		Mode:   strings.TrimSpace(c.Query("mode")),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		if errors.Is(err, service.ErrAgentProfileInvalid) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid filters"}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load agents"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "total": total})
}

// AdminUpdateStatus 停用 / 恢复 / 终止一个代理。
func (h *AgentHandler) AdminUpdateStatus(c *gin.Context) {
	if h.agents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent service is unavailable"}})
		return
	}
	userID, ok := parseAgentUserIDParam(c)
	if !ok {
		return
	}
	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	err := h.agents.UpdateStatus(c.Request.Context(), userID, req.Status, req.Note)
	writeAgentMutationResult(c, err, "failed to update agent")
}

// AdminUpdatePricing 改代理的定价方案与专属分组。
//
// 两个字段都允许显式置空：把代理从批发价上摘下来是正常运营动作，
// 不该只能靠删档案实现。
func (h *AgentHandler) AdminUpdatePricing(c *gin.Context) {
	if h.agents == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent service is unavailable"}})
		return
	}
	userID, ok := parseAgentUserIDParam(c)
	if !ok {
		return
	}
	var req struct {
		PricingPlanID   *int64 `json:"pricing_plan_id"`
		ResellerGroupID *int64 `json:"reseller_group_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	err := h.agents.UpdatePricing(c.Request.Context(), userID, req.PricingPlanID, req.ResellerGroupID)
	writeAgentMutationResult(c, err, "failed to update agent pricing")
}

// AdminListPlans 定价方案列表。
func (h *AgentHandler) AdminListPlans(c *gin.Context) {
	if h.plans == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent pricing is unavailable"}})
		return
	}
	list, err := h.plans.List(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load pricing plans"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

// AdminCreatePlan 新建定价方案。
func (h *AgentHandler) AdminCreatePlan(c *gin.Context) {
	if h.plans == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent pricing is unavailable"}})
		return
	}
	var plan service.AgentPricingPlan
	if err := c.ShouldBindJSON(&plan); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	if err := h.plans.Create(c.Request.Context(), &plan); err != nil {
		if errors.Is(err, service.ErrAgentPricingPlanInvalid) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"code":    "INVALID_PLAN",
				"message": "折扣需落在 (0,1]，让利金额不能为负",
			}})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to create pricing plan"}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": plan})
}

// AdminUpdatePlan 改定价方案。
//
// 改完不会自动重算已经生成的价格：那是一次写库动作，必须经过预览。
// 静默重算等于改一个数字就悄悄改掉了全部代理的成交价。
func (h *AgentHandler) AdminUpdatePlan(c *gin.Context) {
	if h.plans == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent pricing is unavailable"}})
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid id"}})
		return
	}
	var plan service.AgentPricingPlan
	if err := c.ShouldBindJSON(&plan); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return
	}
	plan.ID = id
	switch err := h.plans.Update(c.Request.Context(), &plan); {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"data": plan})
	case errors.Is(err, service.ErrAgentPricingPlanInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code":    "INVALID_PLAN",
			"message": "折扣需落在 (0,1]，让利金额不能为负",
		}})
	case errors.Is(err, service.ErrAgentPricingPlanNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "pricing plan not found"}})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to update pricing plan"}})
	}
}

// AdminPreviewPricing 按方案算出面向代理的批发价，只算不落库。
//
// 预览是这个模块的核心：直接把算出来的价格写进库，配错了要等到用户
// 调用并被扣了错的钱才会发现。返回里带上本次用的积分汇率，
// 让运营能核对「一毛」到底折成了多少积分。
func (h *AgentHandler) AdminPreviewPricing(c *gin.Context) {
	preview, _, ok := h.resolveAgentPricing(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": preview})
}

// AdminApplyPricing 把算好的批发价写进目标渠道。
//
// 走 ChannelService.Update 而不是自己写 SQL：那条路上带着 validateChannelConfig、
// 渠道缓存失效和分组鉴权缓存失效。绕开它直接改表，价格会配了却不生效——
// 得等渠道缓存自己过期才冒出来，中间那段时间足够让人怀疑是算错了。
func (h *AgentHandler) AdminApplyPricing(c *gin.Context) {
	preview, req, ok := h.resolveAgentPricing(c)
	if !ok {
		return
	}
	if req.TargetChannelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "target_channel_id is required"}})
		return
	}
	// 目标和来源同一个渠道 = 把零售价原地改成代理价，全站降价。
	// 这是本接口唯一一个不可逆的手滑方式，硬挡。
	if req.TargetChannelID == req.SourceChannelID {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code":    "TARGET_EQUALS_SOURCE",
			"message": "目标渠道不能与来源渠道相同：那会把零售价直接改成代理价",
		}})
		return
	}

	target, err := h.channels.GetByID(c.Request.Context(), req.TargetChannelID)
	if err != nil || target == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "target channel not found"}})
		return
	}

	rows := preview.Rows
	if _, err := h.channels.Update(c.Request.Context(), req.TargetChannelID, &service.UpdateChannelInput{
		// 只带 ModelPricing：UpdateChannelInput 其余字段都是「零值不改」语义，
		// 所以目标渠道的名称、状态、分组、模型映射都不会被这次调用碰到。
		ModelPricing: &rows,
	}); err != nil {
		requestLogger(c, "handler.agent").Warn("agent.apply_pricing_failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{
			"message": "failed to write agent pricing: " + err.Error(),
		}})
		return
	}

	// 没挂分组的渠道谁都路由不到，价格写了也没人用。
	// 不拦下来（先配价后挂组是正常顺序），但必须说出来。
	warning := ""
	if len(target.GroupIDs) == 0 {
		warning = "目标渠道尚未关联任何分组，代理的请求不会路由到这里；请在渠道管理里挂上代理分组"
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"preview":          preview,
		"target_channel":   target.Name,
		"written_pricings": len(rows),
		"warning":          warning,
	}})
}

type agentPricingRequest struct {
	PlanID          int64 `json:"plan_id"`
	SourceChannelID int64 `json:"source_channel_id"`
	TargetChannelID int64 `json:"target_channel_id"`
}

// resolveAgentPricing 预览与应用共用的计算。
//
// 两条路径共用同一次计算，是为了让运营看到的 diff 就是实际会写进去的东西；
// 各算一遍的话，中间零售价一改，确认过的预览和落库结果就对不上了。
func (h *AgentHandler) resolveAgentPricing(c *gin.Context) (*service.AgentPricingPreview, agentPricingRequest, bool) {
	var req agentPricingRequest
	if h.plans == nil || h.channels == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"message": "agent pricing is unavailable"}})
		return nil, req, false
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.PlanID <= 0 || req.SourceChannelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid body"}})
		return nil, req, false
	}

	plan, err := h.plans.Get(c.Request.Context(), req.PlanID)
	if errors.Is(err, service.ErrAgentPricingPlanNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "pricing plan not found"}})
		return nil, req, false
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "failed to load pricing plan"}})
		return nil, req, false
	}

	channel, err := h.channels.GetByID(c.Request.Context(), req.SourceChannelID)
	if err != nil || channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "source channel not found"}})
		return nil, req, false
	}

	creditsPerCNY, err := h.creditsPerCNY(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
			"code":    "CREDIT_RATE_UNAVAILABLE",
			"message": "取不到积分汇率，无法把让利金额折算成积分",
		}})
		return nil, req, false
	}

	preview, err := service.BuildAgentPricingPreview(
		plan,
		channel.ModelPricing,
		creditsPerCNY,
		service.BuildAgentCostLookup(channel.AccountStatsPricingRules),
	)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return nil, req, false
	}
	return preview, req, true
}

// creditsPerCNY 每元对应多少积分。
//
// 这个值直接决定「让一毛」折算成多少积分，取错了算出来的价格看不出问题。
// 取不到就报错而不是用 100 兜底：兜底值会把一次配置故障变成一批错价。
func (h *AgentHandler) creditsPerCNY(c *gin.Context) (float64, error) {
	if h.payments == nil {
		return 0, errors.New("payment config service is unavailable")
	}
	cfg, err := h.payments.GetPaymentConfig(c.Request.Context())
	if err != nil {
		return 0, err
	}
	if cfg == nil || cfg.BalanceRechargeMultiplier <= 0 {
		return 0, errors.New("invalid balance recharge multiplier")
	}
	return cfg.BalanceRechargeMultiplier, nil
}

func parseAgentUserIDParam(c *gin.Context) (int64, bool) {
	userID, err := strconv.ParseInt(strings.TrimSpace(c.Param("userID")), 10, 64)
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid user id"}})
		return 0, false
	}
	return userID, true
}

// writeAgentMutationResult 把 service 层的错误映射成状态码。
//
// 「改了 0 行」在 repo 层已经被翻译成 not found，这里保持那个语义：
// 改一个不存在的代理必须报错，不能静默成功让管理员以为改好了。
func writeAgentMutationResult(c *gin.Context, err error, failMessage string) {
	switch {
	case err == nil:
		c.Status(http.StatusNoContent)
	case errors.Is(err, service.ErrAgentProfileInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "invalid request"}})
	case errors.Is(err, service.ErrAgentProfileNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "agent not found"}})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": failMessage}})
	}
}
