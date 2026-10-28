package routes

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

// organizationVideoQuotaModel 返回组织的 /v1/videos/generations 请求带着这个模型名时，额度守卫
// 实际看到的模型名：和 compositeTargetPlatformMiddleware 用同一组函数改写（先组织改道表，再组合路由）。
func organizationVideoQuotaModel(ctx context.Context, resolver *service.CompositeRouteResolver, overrides organizationModelOverrider, apiKey *service.APIKey, model string) (string, error) {
	if apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return model, nil
	}
	body, err := sjson.SetBytes([]byte(`{}`), "model", model)
	if err != nil {
		return "", err
	}
	model, _ = applyOrganizationModelOverride(ctx, overrides, apiKey, model, body)
	decision, err := resolver.Resolve(ctx, apiKey.Group.ID, model, compositeRouteEndpointForPath("/v1/videos/generations"))
	if err != nil {
		return "", err
	}
	if upstream := strings.TrimSpace(decision.UpstreamModel); decision.Matched && upstream != "" {
		return upstream, nil
	}
	return model, nil
}

// organizationVideoQuotaRuleHandler 处理 GET /v1/organization/media-quota/video-rule?model=：组织用自己的
// 服务密钥查询某个视频模型在额度模式下接受的时长。租户据此只给用户合法的时长选项；
// supported=false 表示开了媒体额度后这个模型生成不了视频。
func organizationVideoQuotaRuleHandler(resolver *service.CompositeRouteResolver, overrides organizationModelOverrider) gin.HandlerFunc {
	if resolver == nil {
		resolver = service.NewCompositeRouteResolver(nil)
	}
	return func(c *gin.Context) {
		key, ok := middleware.GetAPIKeyFromContext(c)
		if !ok || key == nil || key.User == nil || key.User.AccountType != "organization_service" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		model := strings.TrimSpace(c.Query("model"))
		if model == "" || len(model) > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"code": "invalid_model", "message": "model is required"}})
			return
		}
		quotaModel, err := organizationVideoQuotaModel(c.Request.Context(), resolver, overrides, key, model)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"code": "composite_route_unavailable", "message": "Failed to resolve composite model route"}})
			return
		}
		result := gin.H{"model": model, "quota_model": quotaModel, "supported": false}
		if rule, ok := service.MediaQuotaVideoRule(quotaModel); ok {
			result["supported"], result["rule"] = true, rule
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, result)
	}
}
