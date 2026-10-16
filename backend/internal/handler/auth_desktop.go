package handler

import (
	"regexp"
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
	wxmDesktopClientID         = "com.weimozhixue.desktop"
	wxmDesktopRedirectURI      = "weimozhixue://oauth/callback"
)

var wxmLoopbackRedirect = regexp.MustCompile(`^http://127\.0\.0\.1:([1-9][0-9]{3,4})/oauth/callback$`)
var wxmState = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var wxmChallenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var wxmVerifier = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

func validDesktopRedirect(clientID, redirectURI string) bool {
	switch clientID {
	case fengxingDesktopClientID:
		return redirectURI == fengxingDesktopRedirectURI
	case wxmDesktopClientID:
		if redirectURI == wxmDesktopRedirectURI {
			return true
		}
		match := wxmLoopbackRedirect.FindStringSubmatch(redirectURI)
		if match == nil {
			return false
		}
		port, err := strconv.Atoi(match[1])
		return err == nil && port >= 1024 && port <= 65535
	}
	return false
}

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

func validDesktopAuthorizationRequest(req desktopAuthorizationRequest) bool {
	if strings.TrimSpace(req.ClientID) == wxmDesktopClientID &&
		(!wxmState.MatchString(req.State) || !wxmChallenge.MatchString(req.CodeChallenge)) {
		return false
	}
	return validDesktopRedirect(strings.TrimSpace(req.ClientID), strings.TrimSpace(req.RedirectURI)) &&
		strings.TrimSpace(req.State) != "" &&
		strings.TrimSpace(req.CodeChallenge) != "" &&
		strings.TrimSpace(req.CodeChallengeMethod) == "S256"
}

func validDesktopTokenExchangeRequest(req desktopTokenExchangeRequest) bool {
	if strings.TrimSpace(req.ClientID) == wxmDesktopClientID &&
		(!wxmState.MatchString(req.State) || !wxmVerifier.MatchString(req.CodeVerifier)) {
		return false
	}
	return validDesktopRedirect(strings.TrimSpace(req.ClientID), strings.TrimSpace(req.RedirectURI)) &&
		strings.TrimSpace(req.Code) != "" &&
		strings.TrimSpace(req.State) != "" &&
		strings.TrimSpace(req.CodeVerifier) != ""
}

// AuthorizeDesktop creates a one-time code for the authenticated WindHub user.
// POST /api/v1/auth/desktop/authorize
func (h *AuthHandler) AuthorizeDesktop(c *gin.Context) {
	var req desktopAuthorizationRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validDesktopAuthorizationRequest(req) {
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
			ProviderKey:     strings.TrimSpace(req.ClientID),
			ProviderSubject: strconv.FormatInt(user.ID, 10),
		},
		TargetUserID:           &targetUserID,
		BrowserSessionKey:      state,
		UpstreamIdentityClaims: map[string]any{},
		LocalFlowState: map[string]any{
			"desktop_authorization": map[string]any{
				"client_id":             strings.TrimSpace(req.ClientID),
				"redirect_uri":          strings.TrimSpace(req.RedirectURI),
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
	if err := c.ShouldBindJSON(&req); err != nil || !validDesktopTokenExchangeRequest(req) {
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
