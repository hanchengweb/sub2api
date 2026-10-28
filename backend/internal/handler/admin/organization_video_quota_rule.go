package admin

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// organizationModelOverrides 组织请求的模型改道表，由 *service.SettingService 实现。
type organizationModelOverrides interface {
	OrganizationModelOverride(ctx context.Context, model string) (service.OrganizationModelOverride, bool)
}

func (h *GroupHandler) SetOrganizationModelOverrides(overrides organizationModelOverrides) {
	h.modelOverrides = overrides
}

// OrganizationVideoQuotaRule 回答：一个 API key 在这个分组里的组织，能否用媒体额度为这个
// 视频模型计费。控制面在保存组织视频模型、发放媒体额度之前调用。
//
// 额度守卫看到的不是组织配置的名字：组合分组的请求先按组织改道表、再按组合路由改写模型名
// （compositeTargetPlatformMiddleware）。这里按同样的顺序改写后再查规则，否则路由把一个
// 有规则的名字改写成没有规则的上游模型时，会把用不了的配置放过去。
func (h *GroupHandler) OrganizationVideoQuotaRule(c *gin.Context) {
	groupID, ok := parsePositiveIDParam(c, "id")
	if !ok {
		return
	}
	model := strings.TrimSpace(c.Query("model"))
	if model == "" || len(model) > 200 {
		response.BadRequest(c, "model is required")
		return
	}
	ctx := c.Request.Context()
	group, err := h.adminService.GetGroup(ctx, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	quotaModel := model
	if group.Platform == service.PlatformComposite {
		if h.modelOverrides != nil {
			if override, ok := h.modelOverrides.OrganizationModelOverride(ctx, quotaModel); ok {
				quotaModel = override.Model
			}
		}
		// compositeRouteEndpointForPath 把 /v1/videos/generations 归为 any。
		decision, err := h.adminService.PreviewCompositeRoute(ctx, groupID, service.CompositeRoutePreviewRequest{
			Model: quotaModel, Endpoint: service.CompositeRouteEndpointAny,
		})
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		if upstream := strings.TrimSpace(decision.UpstreamModel); decision.Matched && upstream != "" {
			quotaModel = upstream
		}
	}
	result := gin.H{"group_id": groupID, "model": model, "quota_model": quotaModel, "supported": false}
	if rule, ok := service.MediaQuotaVideoRule(quotaModel); ok {
		result["supported"], result["rule"] = true, rule
	}
	response.Success(c, result)
}
