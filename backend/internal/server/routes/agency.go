package routes

import (
	"github.com/gin-gonic/gin"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
)

// RegisterAgencyRoutes 注册「成为代理」的合作申请接口。
//
// 用户侧挂 JWT：表单在登录态后面，既能把申请关联到账号，也让刷单有界。
// 管理员侧单独挂 adminAuth——申请里有联系人邮箱，不能让普通用户翻到别人的。
func RegisterAgencyRoutes(
	v1 *gin.RouterGroup,
	h *handler.AgencyHandler,
	jwtAuth middleware.JWTAuthMiddleware,
	adminAuth middleware.AdminAuthMiddleware,
) {
	user := v1.Group("/agency")
	user.Use(gin.HandlerFunc(jwtAuth))
	{
		user.POST("/applications", h.Submit)
		user.GET("/applications/mine", h.ListMine)
	}

	admin := v1.Group("/admin/agency-applications")
	admin.Use(gin.HandlerFunc(adminAuth))
	{
		admin.GET("", h.AdminList)
		admin.PUT("/:id", h.AdminUpdate)
	}
}
