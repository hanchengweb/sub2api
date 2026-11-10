package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/mediatasks"
	"github.com/Wei-Shaw/sub2api/internal/voiceassets"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (r mediaTaskRoutes) CloneSlots(ctx context.Context, o mediatasks.Owner) ([]voiceassets.Slot, error) {
	if !r.allowed(ctx, o, mediatasks.Audio, mediatasks.CloneModel) || r.accounts == nil {
		return nil, mediatasks.ErrRoute
	}
	accounts, e := r.accounts.ListSchedulableByGroupID(ctx, o.GroupID)
	if e != nil {
		return nil, mediatasks.ErrUnavailable
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		return accounts[i].ID < accounts[j].ID
	})
	slots := make([]voiceassets.Slot, 0)
	for _, a := range accounts {
		if !mediaTaskAccount(a, mediatasks.Audio, mediatasks.CloneModel) || strings.TrimSpace(a.GetCredential("api_key")) == "" {
			continue
		}
		for _, slot := range voiceassets.ConfiguredSlots(a.ID, o.UserID, a.Extra) {
			slots = append(slots, slot)
			if len(slots) >= 1000 {
				return slots, nil
			}
		}
	}
	return slots, nil
}
func voiceAssetError(c *gin.Context, e error) {
	switch {
	case errors.Is(e, voiceassets.ErrNoSlot):
		c.AbortWithStatusJSON(409, gin.H{"error": gin.H{"code": "voice_clone_slot_unavailable", "message": "暂无已配置的克隆音色名额，请联系管理员。"}})
	case errors.Is(e, voiceassets.ErrConsent):
		c.AbortWithStatusJSON(400, gin.H{"error": gin.H{"code": "voice_clone_consent_required", "message": "请确认录音属于本人或已获得授权。"}})
	default:
		mediaTaskError(c, e)
	}
}
func (h *OrganizationMediaTaskHandler) voiceAssetOwner(c *gin.Context) (mediatasks.Owner, bool) {
	o, ok := mediaTaskOwner(c)
	if !ok {
		return o, false
	}
	if o.ActorID == "" {
		mediaTaskError(c, mediatasks.ErrNotFound)
		return o, false
	}
	if h.voices == nil || h.voices.Store == nil {
		mediaTaskError(c, mediatasks.ErrUnavailable)
		return o, false
	}
	return o, true
}
func (h *OrganizationMediaTaskHandler) VoiceAssetCatalog(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	available := false
	reason := "media_feature_disabled"
	if h.enabled {
		_, e := h.engine.Routes.Resolve(c.Request.Context(), o, mediatasks.Audio, mediatasks.SpeechModel)
		available = e == nil
		reason = "media_route_unavailable"
	}
	cloning := h.cloneAvailability(c.Request.Context(), o)
	value := gin.H{"available": available, "voices": mediatasks.Voices, "scenes": mediatasks.Scenes, "emotions": mediatasks.Emotions, "cloning": cloning}
	if !available {
		value["reason"] = reason
	}
	c.JSON(200, value)
}
func (h *OrganizationMediaTaskHandler) cloneAvailability(ctx context.Context, o mediatasks.Owner) gin.H {
	if !h.enabled {
		return gin.H{"available": false, "reason": "media_feature_disabled"}
	}
	if !voiceassets.DecoderAvailable() {
		return gin.H{"available": false, "reason": "voice_clone_decoder_unavailable"}
	}
	free, e := h.voices.Cloning(ctx, o)
	if e != nil {
		return gin.H{"available": false, "reason": "media_route_unavailable"}
	}
	if !free {
		return gin.H{"available": false, "reason": "voice_clone_slot_unavailable"}
	}
	return gin.H{"available": true}
}
func (h *OrganizationMediaTaskHandler) VoiceAssetsList(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	items, e := h.voices.List(c.Request.Context(), o)
	if e != nil {
		voiceAssetError(c, e)
		return
	}
	value := gin.H{"items": items, "available": h.enabled, "cloning": h.cloneAvailability(c.Request.Context(), o)}
	if !h.enabled {
		value["reason"] = "media_feature_disabled"
	}
	c.JSON(200, value)
}
func (h *OrganizationMediaTaskHandler) VoiceAssetGet(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	id, ok := voiceAssetPathID(c)
	if !ok {
		return
	}
	a, e := h.voices.Get(c.Request.Context(), o, id)
	if e != nil {
		voiceAssetError(c, e)
		return
	}
	c.JSON(200, a)
}
func voiceAssetPathID(c *gin.Context) (string, bool) {
	id, e := uuid.Parse(c.Param("id"))
	if e != nil || id == uuid.Nil {
		mediaTaskError(c, mediatasks.ErrNotFound)
		return "", false
	}
	return id.String(), true
}
func decodeVoiceAssetJSON(c *gin.Context, target any) bool {
	d := json.NewDecoder(io.LimitReader(c.Request.Body, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(&struct{}{}) != io.EOF {
		mediaTaskError(c, mediatasks.ErrInvalid)
		return false
	}
	return true
}
func (h *OrganizationMediaTaskHandler) VoiceAssetCreate(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	if !h.enabled {
		voiceAssetError(c, mediatasks.ErrRoute)
		return
	}
	var body struct {
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Voice string `json:"voice"`
	}
	if !decodeVoiceAssetJSON(c, &body) {
		return
	}
	if body.Kind != "official" {
		voiceAssetError(c, mediatasks.ErrInvalid)
		return
	}
	a, e := h.voices.Official(c.Request.Context(), o, body.Name, body.Voice)
	if e != nil {
		voiceAssetError(c, e)
		return
	}
	c.JSON(http.StatusCreated, a)
}
func (h *OrganizationMediaTaskHandler) VoiceAssetPatch(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	id, ok := voiceAssetPathID(c)
	if !ok {
		return
	}
	var body struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
	}
	if !decodeVoiceAssetJSON(c, &body) {
		return
	}
	a, e := h.voices.Patch(c.Request.Context(), o, id, body.Name, body.Enabled)
	if e != nil {
		voiceAssetError(c, e)
		return
	}
	c.JSON(200, a)
}
func (h *OrganizationMediaTaskHandler) VoiceAssetDelete(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	id, ok := voiceAssetPathID(c)
	if !ok {
		return
	}
	if e := h.voices.Delete(c.Request.Context(), o, id); e != nil {
		voiceAssetError(c, e)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}
func (h *OrganizationMediaTaskHandler) VoiceAssetRefresh(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	if !h.enabled {
		voiceAssetError(c, mediatasks.ErrRoute)
		return
	}
	id, ok := voiceAssetPathID(c)
	if !ok {
		return
	}
	a, e := h.voices.Refresh(c.Request.Context(), o, id)
	if e != nil {
		voiceAssetError(c, e)
		return
	}
	c.JSON(200, a)
}
func (h *OrganizationMediaTaskHandler) VoiceAssetClone(c *gin.Context) {
	o, ok := h.voiceAssetOwner(c)
	if !ok {
		return
	}
	if !h.enabled {
		voiceAssetError(c, mediatasks.ErrRoute)
		return
	}
	id, e := uuid.Parse(c.GetHeader("X-Client-Request-ID"))
	if e != nil || id == uuid.Nil {
		voiceAssetError(c, mediatasks.ErrInvalid)
		return
	}
	// Stream multipart into memory with an overall middleware cap. ParseMultipartForm
	// is deliberately avoided because it can write private samples to temp files.
	reader, e := c.Request.MultipartReader()
	if e != nil {
		voiceAssetError(c, mediatasks.ErrInvalid)
		return
	}
	fields := map[string]string{}
	var audio []byte
	format := ""
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			voiceAssetError(c, mediatasks.ErrInvalid)
			return
		}
		key := part.FormName()
		if key == "audio" {
			if audio != nil {
				part.Close()
				voiceAssetError(c, mediatasks.ErrInvalid)
				return
			}
			format = strings.TrimPrefix(strings.ToLower(filepath.Ext(part.FileName())), ".")
			audio, e = io.ReadAll(io.LimitReader(part, voiceassets.MaxSampleBytes+1))
			part.Close()
			if e != nil || len(audio) > voiceassets.MaxSampleBytes {
				voiceAssetError(c, mediatasks.ErrInvalid)
				return
			}
			continue
		}
		if (key != "name" && key != "demo_text" && key != "consent") || part.FileName() != "" {
			part.Close()
			voiceAssetError(c, mediatasks.ErrInvalid)
			return
		}
		if _, exists := fields[key]; exists {
			part.Close()
			voiceAssetError(c, mediatasks.ErrInvalid)
			return
		}
		b, err := io.ReadAll(io.LimitReader(part, 2049))
		part.Close()
		if err != nil || len(b) > 2048 {
			voiceAssetError(c, mediatasks.ErrInvalid)
			return
		}
		fields[key] = string(b)
	}
	a, e := h.voices.Clone(c.Request.Context(), o, id.String(), fields["name"], format, fields["demo_text"], audio, fields["consent"] == "true")
	if e != nil {
		voiceAssetError(c, e)
		return
	}
	c.JSON(http.StatusAccepted, a)
}
