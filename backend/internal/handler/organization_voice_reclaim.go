package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/gin-gonic/gin"
)

func (h *OrganizationMediaTaskHandler) VoiceMemberState(c *gin.Context) {
	o, ok := mediaTaskOwner(c)
	if !ok {
		return
	}
	if h.reclaim == nil || !h.reclaim.Config.MemberEnabled {
		voiceAssetError(c, mediatasks.ErrRoute)
		return
	}
	var state voiceassets.MemberState
	if !decodeVoiceAssetJSON(c, &state) {
		return
	}
	items, err := h.reclaim.MemberState(c.Request.Context(), o.UserID, state)
	if err != nil {
		voiceAssetError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": items, "limit": voiceassets.ReclaimBatchLimit, "limit_reached": len(items) == voiceassets.ReclaimBatchLimit})
}

func (h *OrganizationMediaTaskHandler) VoiceReclaimPreview(c *gin.Context) {
	o, ok := mediaTaskOwner(c)
	if !ok {
		return
	}
	if h.reclaim == nil {
		voiceAssetError(c, mediatasks.ErrRoute)
		return
	}
	items, err := h.reclaim.Preview(c.Request.Context(), o.UserID)
	if err != nil {
		voiceAssetError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": items, "limit": voiceassets.ReclaimBatchLimit, "limit_reached": len(items) == voiceassets.ReclaimBatchLimit})
}
