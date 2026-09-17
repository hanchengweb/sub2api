package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
)

// RegisterAgentRoutes 注册代理身份与代理定价接口。
//
// 与 RegisterAgencyRoutes 分开：那边是「申请合作」（任何登录用户都能提交），
// 这边是「已经是代理之后」的身份和价格。混在一起会让人以为申请表和
// 定价方案是同一层的东西。
//
// 定价相关的接口全部挂 adminAuth：代理批发价是平台的成本底线，
// 让代理自己能读到整张价目表，等于把议价空间摊开给他看。
func RegisterAgentRoutes(
	v1 *gin.RouterGroup,
	h *handler.AgentHandler,
	jwtAuth middleware.JWTAuthMiddleware,
	adminAuth middleware.AdminAuthMiddleware,
) {
	user := v1.Group("/agent")
	user.Use(gin.HandlerFunc(jwtAuth))
	{
		// 不是代理时回 404，前端据此决定显示申请入口还是代理面板。
		user.GET("/profile", h.GetMine)
		// 以下都走 requireActiveAgent：作用域根取自登录态，不收路径参数——
		// 收了的话改个数字就能看别人的客户和账。
		user.GET("/settlements", h.ListMySettlements)
		user.GET("/customers", h.ListMyCustomers)
		user.GET("/customers/usage", h.ListMyCustomerUsage)
	}

	admin := v1.Group("/admin/agents")
	admin.Use(gin.HandlerFunc(adminAuth))
	{
		admin.GET("", h.AdminList)
		admin.PUT("/:userID/status", h.AdminUpdateStatus)
		admin.PUT("/:userID/pricing", h.AdminUpdatePricing)
		admin.GET("/:userID/settlements", h.AdminListAgentSettlements)
		// 手动触发结算。自动发钱在没人核对过一次之前不该开。
		admin.POST("/:userID/settle", h.AdminSettleAgent)
	}

	plans := v1.Group("/admin/agent-pricing-plans")
	plans.Use(gin.HandlerFunc(adminAuth))
	{
		plans.GET("", h.AdminListPlans)
		plans.POST("", h.AdminCreatePlan)
		plans.PUT("/:id", h.AdminUpdatePlan)
		// 只算不落库。配错价要等到用户被扣了错的钱才会发现，所以先看 diff。
		plans.POST("/preview", h.AdminPreviewPricing)
		// 落库。目标渠道必须不同于来源渠道，否则会把零售价改成代理价。
		plans.POST("/apply", h.AdminApplyPricing)
	}
}
