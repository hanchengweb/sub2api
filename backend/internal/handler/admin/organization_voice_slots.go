package admin

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/gin-gonic/gin"
)

type organizationSlotSummary struct {
	*voiceassets.SlotSummary
	OrganizationID          string `json:"organization_id"`
	OrganizationIssuer      string `json:"organization_issuer"`
	OrganizationEnvironment string `json:"organization_environment"`
}

func voiceSlotError(c *gin.Context, err error) {
	status, code := 503, "voice_clone_slots_unavailable"
	switch {
	case errors.Is(err, voiceassets.ErrSlotRequest):
		status, code = 400, voiceassets.ErrSlotRequest.Error()
	case errors.Is(err, voiceassets.ErrSlotIdentity), errors.Is(err, service.ErrServiceAccountIdentity):
		status, code = 409, voiceassets.ErrSlotIdentity.Error()
	case errors.Is(err, voiceassets.ErrSlotVersion):
		status, code = 409, voiceassets.ErrSlotVersion.Error()
	case errors.Is(err, voiceassets.ErrSlotBelowUsed):
		status, code = 409, voiceassets.ErrSlotBelowUsed.Error()
	case errors.Is(err, voiceassets.ErrSlotInventory):
		status, code = 409, voiceassets.ErrSlotInventory.Error()
	case errors.Is(err, voiceassets.ErrSlotIdempotency):
		status, code = 409, voiceassets.ErrSlotIdempotency.Error()
	case errors.Is(err, service.ErrUserNotFound), errors.Is(err, mediatasks.ErrNotFound):
		status, code = 404, "organization_not_found"
	}
	response.Error(c, status, code)
}

func (h *AdminAPIKeyHandler) voiceSlotOrganization(c *gin.Context) (*service.User, bool) {
	c.Header("Cache-Control", "no-store")
	f, err := organizationFilters(c)
	if err != nil {
		voiceSlotError(c, voiceassets.ErrSlotRequest)
		return nil, false
	}
	user, err := h.organization(c.Request.Context(), f)
	if err != nil {
		voiceSlotError(c, err)
		return nil, false
	}
	if h.voiceSlots == nil {
		voiceSlotError(c, mediatasks.ErrUnavailable)
		return nil, false
	}
	return user, true
}

func (h *AdminAPIKeyHandler) OrganizationVoiceCloneSlots(c *gin.Context) {
	user, ok := h.voiceSlotOrganization(c)
	if !ok {
		return
	}
	value, err := h.voiceSlots.OrganizationSlots(c.Request.Context(), user.ID, user.AllowedGroups)
	if err != nil {
		voiceSlotError(c, err)
		return
	}
	response.Success(c, organizationSlotSummary{value, user.OrganizationID, user.OrganizationIssuer, user.OrganizationEnvironment})
}

func (h *AdminAPIKeyHandler) SetOrganizationVoiceCloneSlots(c *gin.Context) {
	user, ok := h.voiceSlotOrganization(c)
	if !ok {
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication_required")
		return
	}
	var body struct {
		RequestID       *string `json:"request_id"`
		ExpectedUserID  *int64  `json:"expected_user_id"`
		ExpectedVersion *int64  `json:"expected_version"`
		Total           *int64  `json:"total_slots"`
		Reason          *string `json:"reason"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF || body.RequestID == nil || body.ExpectedUserID == nil || body.ExpectedVersion == nil || body.Total == nil || body.Reason == nil {
		voiceSlotError(c, voiceassets.ErrSlotRequest)
		return
	}
	change := voiceassets.SlotChange{RequestID: *body.RequestID, ExpectedUserID: *body.ExpectedUserID, ExpectedVersion: *body.ExpectedVersion, Total: *body.Total, Reason: strings.TrimSpace(*body.Reason)}
	if change.Validate() != nil || c.GetHeader("Idempotency-Key") != change.RequestID {
		voiceSlotError(c, voiceassets.ErrSlotRequest)
		return
	}
	value, err := h.voiceSlots.SetOrganizationSlots(c.Request.Context(), user.ID, user.AllowedGroups, change, subject.UserID)
	if err != nil {
		voiceSlotError(c, err)
		return
	}
	response.Success(c, organizationSlotSummary{value, user.OrganizationID, user.OrganizationIssuer, user.OrganizationEnvironment})
}
