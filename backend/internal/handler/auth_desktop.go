package handler

import (
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

const (
	fengxingDesktopClientID    = "com.fengxingzhonghe.desktop"
	fengxingDesktopRedirectURI = "fengxingzhonghe://auth/callback"
)

type desktopAuthorizationRequest struct {
	ClientID            string `json:"client_id" binding:"required"`
	RedirectURI         string `json:"redirect_uri" binding:"required"`
	State               string `json:"state" binding:"required"`
	CodeChallenge       string `json:"code_challenge" binding:"required"`
	CodeChallengeMethod string `json:"code_challenge_method" binding:"required"`
}

type desktopAuthorizationResponse struct {
	Code      string `json:"code"`
	ExpiresIn int    `json:"expires_in"`
}

type desktopTokenExchangeRequest struct {
	ClientID     string `json:"client_id" binding:"required"`
	RedirectURI  string `json:"redirect_uri" binding:"required"`
	Code         string `json:"code" binding:"required"`
	State        string `json:"state" binding:"required"`
	CodeVerifier string `json:"code_verifier" binding:"required"`
}

func validFengxingDesktopAuthorizationRequest(req desktopAuthorizationRequest) bool {
	return strings.TrimSpace(req.ClientID) == fengxingDesktopClientID &&
		strings.TrimSpace(req.RedirectURI) == fengxingDesktopRedirectURI &&
		strings.TrimSpace(req.State) != "" &&
		strings.TrimSpace(req.CodeChallenge) != "" &&
		strings.TrimSpace(req.CodeChallengeMethod) == "S256"
}

func validFengxingDesktopTokenExchangeRequest(req desktopTokenExchangeRequest) bool {
	return strings.TrimSpace(req.ClientID) == fengxingDesktopClientID &&
		strings.TrimSpace(req.RedirectURI) == fengxingDesktopRedirectURI &&
		strings.TrimSpace(req.Code) != "" &&
		strings.TrimSpace(req.State) != "" &&
		strings.TrimSpace(req.CodeVerifier) != ""
}

// AuthorizeDesktop creates a one-time code for the authenticated WindHub user.
// POST /api/v1/auth/desktop/authorize
func (h *AuthHandler) AuthorizeDesktop(c *gin.Context) {
	var req desktopAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validFengxingDesktopAuthorizationRequest(req) {
		response.BadRequest(c, "Invalid desktop authorization request")
		return
	}

	subject, ok := servermiddleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h == nil || h.userService == nil {
		response.InternalError(c, "Authentication service is not ready")
		return
	}

	user, err := h.userService.GetByID(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	state := strings.TrimSpace(req.State)
	svc, err := h.pendingIdentityService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	targetUserID := user.ID
	session, err := svc.CreatePendingSession(c.Request.Context(), service.CreatePendingAuthSessionInput{
		Intent: "login",
		Identity: service.PendingAuthIdentityKey{
			ProviderType:    "email",
			ProviderKey:     fengxingDesktopClientID,
			ProviderSubject: strconv.FormatInt(user.ID, 10),
		},
		TargetUserID:           &targetUserID,
		BrowserSessionKey:      state,
		UpstreamIdentityClaims: map[string]any{},
		LocalFlowState: map[string]any{
			"desktop_authorization": map[string]any{
				"client_id":             fengxingDesktopClientID,
				"redirect_uri":          fengxingDesktopRedirectURI,
				"state":                 state,
				"code_challenge":        strings.TrimSpace(req.CodeChallenge),
				"code_challenge_method": "S256",
			},
		},
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	issued, err := svc.IssueCompletionCode(c.Request.Context(), service.IssuePendingAuthCompletionCodeInput{
		PendingAuthSessionID: session.ID,
		BrowserSessionKey:    state,
		TTL:                  5 * time.Minute,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	expiresIn := int(time.Until(issued.ExpiresAt).Seconds())
	if expiresIn < 1 {
		expiresIn = 1
	}
	response.Success(c, desktopAuthorizationResponse{Code: issued.Code, ExpiresIn: expiresIn})
}

// ExchangeDesktopAuthorizationCode exchanges the one-time code for a WindHub token pair.
// POST /api/v1/auth/desktop/token
func (h *AuthHandler) ExchangeDesktopAuthorizationCode(c *gin.Context) {
	var req desktopTokenExchangeRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validFengxingDesktopTokenExchangeRequest(req) {
		response.BadRequest(c, "Invalid desktop token request")
		return
	}

	svc, err := h.pendingIdentityService()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	session, err := svc.ConsumeDesktopCompletionCode(c.Request.Context(), strings.TrimSpace(req.Code), service.ConsumeDesktopAuthCompletionCodeInput{
		ClientID:     strings.TrimSpace(req.ClientID),
		RedirectURI:  strings.TrimSpace(req.RedirectURI),
		State:        strings.TrimSpace(req.State),
		CodeVerifier: strings.TrimSpace(req.CodeVerifier),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if session.TargetUserID == nil || *session.TargetUserID <= 0 || h == nil || h.userService == nil {
		response.ErrorFrom(c, service.ErrDesktopAuthorizationInvalid)
		return
	}

	user, err := h.userService.GetByID(c.Request.Context(), *session.TargetUserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := ensureLoginUserActive(user); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := h.ensureBackendModeAllowsUser(c.Request.Context(), user); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	tokenPair, err := h.authService.GenerateTokenPair(c.Request.Context(), user, "")
	if err != nil {
		response.InternalError(c, "Failed to generate token pair")
		return
	}
	h.authService.RecordSuccessfulLogin(c.Request.Context(), user.ID)
	writeOAuthTokenPairResponse(c, tokenPair)
}
