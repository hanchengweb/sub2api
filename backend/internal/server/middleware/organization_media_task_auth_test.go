package middleware

import "testing"

func TestOrganizationMediaTaskBillingExceptionIsExact(t *testing.T) {
	for _, row := range []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/v1/audio/generations", true}, {"POST", "/v1/model3d/generations", true}, {"GET", "/v1/audio/:id", true}, {"GET", "/v1/model3d/:id/content", true}, {"GET", "/v1/media/models", true},
		{"POST", "/v1/audio/:id", false}, {"POST", "/v1/audio/speech", false}, {"GET", "/v1/model3d/admin", false}, {"POST", "/v1/chat/completions", false}, {"GET", "/v1/media/models/other", false},
		{"GET", "/v1/voice-assets", true}, {"POST", "/v1/voice-assets/clone", true}, {"PATCH", "/v1/voice-assets/:id", true}, {"DELETE", "/v1/voice-assets/:id", true}, {"POST", "/v1/voice-assets/:id/refresh", true},
		{"POST", "/v1/voice-assets/:id", false}, {"GET", "/v1/voice-assets/:id/refresh", false}, {"DELETE", "/v1/voice-assets/clone", false}, {"POST", "/v1/voice-assets/admin", false},
	} {
		if got := isOrganizationMediaTaskRequest(row.method, row.path); got != row.allowed {
			t.Fatalf("%s %s: %v", row.method, row.path, got)
		}
	}
}
