package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDigitalTeacherBillingBypassIsExact(t *testing.T) {
	for _, v := range []struct {
		method, route string
		ok            bool
	}{
		{"POST", "/v1/digital-teachers/sessions", true}, {"POST", "/v1/digital-teachers/sessions/:id/stop", true},
		{"GET", "/v1/digital-teachers/sessions/:id", true}, {"GET", "/v1/digital-teachers/settings", true},
		{"POST", "/v1/digital-teachers/settings", false}, {"POST", "/v1/digital-teachers/sessions/unknown", false},
		{"POST", "/v1/chat/completions", false}, {"POST", "/v1/digital-teachers/sdk/v1/sessions/start", false},
	} {
		require.Equal(t, v.ok, isDigitalTeacherRequest(v.method, v.route), v)
	}
}
func TestDigitalTeacherNullOriginOnlyAllowsCapabilitySDK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CORS(config.CORSConfig{}))
	r.POST("/v1/digital-teachers/sdk/v1/sessions/start", func(c *gin.Context) { c.Status(401) })
	for _, path := range []string{"/v1/digital-teachers/sdk/v1/sessions/start", "/api/v1/admin/digital-teachers", "/v1/digital-teachers/sessions", "/v1/digital-teachers/sdk/unknown"} {
		req := httptest.NewRequest("OPTIONS", path, nil)
		req.Header.Set("Origin", "null")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if path == "/v1/digital-teachers/sdk/v1/sessions/start" {
			require.Equal(t, http.StatusNoContent, w.Code)
			require.Equal(t, "null", w.Header().Get("Access-Control-Allow-Origin"))
			require.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
		} else {
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}
