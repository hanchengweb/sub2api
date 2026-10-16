//go:build unit

package handler

import (
	"strings"
	"testing"
)

func TestWxmDesktopAuthorizationValidation(t *testing.T) {
	request := desktopAuthorizationRequest{ClientID: wxmDesktopClientID, RedirectURI: wxmDesktopRedirectURI,
		State: strings.Repeat("s", 43), CodeChallenge: strings.Repeat("c", 43), CodeChallengeMethod: "S256"}
	if !validDesktopAuthorizationRequest(request) {
		t.Fatal("installed callback rejected")
	}
	for _, uri := range []string{"http://127.0.0.1:43186/oauth/callback", "http://127.0.0.1:65535/oauth/callback"} {
		req := request
		req.RedirectURI = uri
		if !validDesktopAuthorizationRequest(req) {
			t.Fatalf("preview callback rejected: %s", uri)
		}
	}
	for _, uri := range []string{
		"http://localhost:43186/oauth/callback", "http://127.0.0.1:80/oauth/callback",
		"http://127.0.0.1:65536/oauth/callback", "http://127.0.0.1:43186/oauth/callback?next=evil",
		"http://127.0.0.1:43186/oauth/callback#fragment", "http://127.0.0.1:43186/other",
		"http://127.0.0.1.evil.test:43186/oauth/callback", "http://user@127.0.0.1:43186/oauth/callback",
		fengxingDesktopRedirectURI, "weimozhixue://oauth/callback/extra",
	} {
		req := request
		req.RedirectURI = uri
		if validDesktopAuthorizationRequest(req) {
			t.Fatalf("unregistered callback accepted: %s", uri)
		}
	}
	for _, change := range []func(*desktopAuthorizationRequest){
		func(r *desktopAuthorizationRequest) { r.State = "short" },
		func(r *desktopAuthorizationRequest) { r.CodeChallenge = "" },
		func(r *desktopAuthorizationRequest) { r.CodeChallengeMethod = "plain" },
		func(r *desktopAuthorizationRequest) { r.ClientID = fengxingDesktopClientID },
	} {
		req := request
		change(&req)
		if validDesktopAuthorizationRequest(req) {
			t.Fatal("invalid authorization accepted")
		}
	}
	token := desktopTokenExchangeRequest{ClientID: request.ClientID, RedirectURI: request.RedirectURI,
		State: request.State, Code: "one-time-code", CodeVerifier: strings.Repeat("v", 43)}
	if !validDesktopTokenExchangeRequest(token) {
		t.Fatal("valid exchange rejected")
	}
	token.CodeVerifier = "short"
	if validDesktopTokenExchangeRequest(token) {
		t.Fatal("weak verifier accepted")
	}
}

func TestFengxingDesktopAuthorizationRemainsCompatible(t *testing.T) {
	if !validDesktopAuthorizationRequest(desktopAuthorizationRequest{ClientID: fengxingDesktopClientID,
		RedirectURI: fengxingDesktopRedirectURI, State: "state", CodeChallenge: "challenge", CodeChallengeMethod: "S256"}) {
		t.Fatal("existing client rejected")
	}
	if validDesktopRedirect(fengxingDesktopClientID, wxmDesktopRedirectURI) || validDesktopRedirect(wxmDesktopClientID, fengxingDesktopRedirectURI) {
		t.Fatal("cross-client callback accepted")
	}
}
