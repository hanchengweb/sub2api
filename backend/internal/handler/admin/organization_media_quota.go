package admin

import (
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AdminAPIKeyHandler) OrganizationMediaQuota(c *gin.Context) {
	f, err := organizationFilters(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	u, err := h.organization(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.mediaQuota == nil {
		response.Error(c, 503, "media_quota_unavailable")
		return
	}
	page, _ := strconv.Atoi(c.Query("page"))
	result, err := h.mediaQuota.Summary(c.Request.Context(), u.ID, page)
	if err != nil {
		response.Error(c, 503, "media_quota_unavailable")
		return
	}
	response.Success(c, result)
}
func (h *AdminAPIKeyHandler) GrantOrganizationMediaQuota(c *gin.Context) {
	f, err := organizationFilters(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	u, err := h.organization(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var grant service.MediaQuotaGrant
	if c.ShouldBindJSON(&grant) != nil || grant.Images+grant.VideoSeconds <= 0 || !grant.ExpiresAt.After(grant.EffectiveAt) || c.GetHeader("Idempotency-Key") != grant.RequestID {
		response.BadRequest(c, "invalid_media_grant")
		return
	}
	if h.mediaQuota == nil {
		response.Error(c, 503, "media_quota_unavailable")
		return
	}
	version, err := h.mediaQuota.Grant(c.Request.Context(), u.ID, grant)
	if errors.Is(err, service.ErrMediaQuotaConflict) {
		response.Error(c, 409, "media_quota_conflict")
		return
	}
	if err != nil {
		response.Error(c, 503, "media_quota_unavailable")
		return
	}
	response.Success(c, gin.H{"request_id": grant.RequestID, "version": version, "organization_id": f.OrganizationID, "organization_issuer": f.OrganizationIssuer, "organization_environment": f.OrganizationEnvironment})
}
