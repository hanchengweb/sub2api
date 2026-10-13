package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	mw "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

type RemoteAuthorizationHandler struct {
	svc *service.RemoteAuthorizationService
}

func (h *AuthHandler) RemoteAuthorization(cache *redis.Client, keys *service.APIKeyService) *RemoteAuthorizationHandler {
	return &RemoteAuthorizationHandler{service.NewRemoteAuthorizationService(cache, h.userService, keys, h.authService)}
}

func (h *RemoteAuthorizationHandler) Authorize(c *gin.Context) {
	var req service.RemoteAuthorizationRequest
	if c.ShouldBindJSON(&req) != nil || !req.Valid() {
		response.BadRequest(c, "Invalid remote authorization request")
		return
	}
	subject, ok := mw.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Login required")
		return
	}
	sid, _ := c.Get(mw.ContextKeySessionID)
	sessionID, _ := sid.(string)
	code, err := h.svc.Authorize(c.Request.Context(), subject.UserID, sessionID, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"code": code, "expires_in": 180})
}

func (h *RemoteAuthorizationHandler) Proof(c *gin.Context) {
	key, ok := mw.GetAPIKeyFromContext(c)
	var req struct {
		Challenge string `json:"code_challenge"`
	}
	if !ok {
		response.Unauthorized(c, "Personal connection required")
		return
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	code, identity, err := h.svc.DesktopProof(c.Request.Context(), key, req.Challenge)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"code": code, "identity": identity, "expires_in": 180})
}

func (h *RemoteAuthorizationHandler) Exchange(c *gin.Context) {
	var req struct {
		ClientID    string `json:"client_id"`
		RedirectURI string `json:"redirect_uri"`
		Code        string `json:"code"`
		Verifier    string `json:"code_verifier"`
		State       string `json:"state"`
		Purpose     string `json:"purpose"`
	}
	if c.ShouldBindJSON(&req) != nil || req.ClientID != service.RemoteClientID ||
		(req.Purpose != "browser" && req.Purpose != "desktop") ||
		(req.Purpose == "browser" && req.RedirectURI != service.RemoteCallback) ||
		(req.Purpose == "desktop" && (req.RedirectURI != "" || req.State != "")) {
		response.BadRequest(c, "Invalid remote exchange")
		return
	}
	token, identity, err := h.svc.Exchange(c.Request.Context(), req.Code, req.Verifier, req.State, req.Purpose)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"token": token, "identity": identity})
}

func (h *RemoteAuthorizationHandler) Inspect(c *gin.Context) {
	identity, err := h.svc.Inspect(c.Request.Context(), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, identity)
}

func (h *RemoteAuthorizationHandler) Revoke(c *gin.Context) {
	if err := h.svc.Revoke(c.Request.Context(), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}
