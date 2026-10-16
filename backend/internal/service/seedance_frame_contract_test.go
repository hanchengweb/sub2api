//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSeedanceFirstFramePreservesModerationAndForwarding(t *testing.T) {
	for _, image := range []string{"https://fixture/frame.png", "data:image/png;base64," + strings.Repeat("aW1n", 400000)} {
		t.Run(strings.SplitN(image, ":", 2)[0], func(t *testing.T) {
			body := []byte(`{"model":"seedance-2-fast","prompt":"animate","duration":5,"aspect_ratio":"adaptive","image_with_roles":[{"url":"` + image + `","role":"first_frame"}]}`)
			info := ParseGrokMediaRequest("application/json", body)
			require.True(t, info.HasInputImage())
			require.Equal(t, []string{image}, info.InputImageURLs)
			require.Equal(t, image, gjson.GetBytes(info.ModerationBody(), "images.0.image_url").String())
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			account := &Account{ID: 63, Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "fixture-key", "base_url": "https://api.x.ai/v1"}}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusAccepted,
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   io.NopCloser(strings.NewReader(`{"id":"seedance-test-task","status":"submitted"}`))}}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			result, err := svc.ForwardGrokMedia(context.Background(), c, account, GrokMediaEndpointVideosGenerations, "", body, "application/json")
			require.NoError(t, err)
			require.JSONEq(t, string(body), string(upstream.lastBody))
			require.Equal(t, "seedance-test-task", result.ResponseID)
			require.Equal(t, "seedance-2-fast", result.BillingModel)
			require.Equal(t, 5, result.VideoDurationSeconds)
			require.Equal(t, 1, result.VideoCount)
		})
	}
}
