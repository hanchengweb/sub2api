//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestSeedreamArkForwardingUsesNativeContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, watermark := range []string{"true", "false"} {
		body := []byte(`{"model":"v-doubao-seedream-5-0-pro","prompt":"fixture","size":"1:1","n":1,"metadata":{"resolution":"1K","watermark":` + watermark + `},"image_urls":["https://no-such-host.invalid/ref.png"]}`)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aGk="}]}`))}}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		parsed, err := svc.ParseOpenAIImagesRequest(c, body)
		require.NoError(t, err)
		account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
			"api_key": "fixture-only", "base_url": "https://ark.cn-beijing.volces.com/api/v3",
			"model_mapping": map[string]any{"v-doubao-seedream-5-0-pro": "doubao-seedream-5-0-pro-260628"},
		}}
		_, err = svc.ForwardImages(context.Background(), c, account, body, parsed, "")
		require.NoError(t, err)
		require.Len(t, upstream.requests, 1)
		require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/images/generations", upstream.lastReq.URL.String())
		require.JSONEq(t, `{"model":"doubao-seedream-5-0-pro-260628","prompt":"fixture","size":"1024x1024","watermark":`+watermark+`,"image":["https://no-such-host.invalid/ref.png"]}`, string(upstream.lastBody))
	}
}

func TestSeedreamArkAspectSizesStayWithinProviderBounds(t *testing.T) {
	for _, resolution := range []string{"1K", "1.5K", "2K"} {
		for _, ratio := range []string{"1:1", "3:2", "2:3", "4:3", "3:4", "5:4", "4:5", "16:9", "9:16", "2:1", "1:2", "21:9", "9:21", "1:16", "16:1"} {
			body, err := normalizeSeedreamArkRequest([]byte(fmt.Sprintf(`{"model":"doubao-seedream-5-0-pro-260628","size":%q,"metadata":{"resolution":%q}}`, ratio, resolution)), "application/json", "https://ark.cn-beijing.volces.com/api/v3")
			require.NoError(t, err)
			var width, height int
			_, err = fmt.Sscanf(gjson.GetBytes(body, "size").String(), "%dx%d", &width, &height)
			require.NoError(t, err)
			require.GreaterOrEqual(t, width*height, 921600)
			require.LessOrEqual(t, width*height, 4624220)
			require.Zero(t, width%16+height%16)
			var rw, rh float64
			_, err = fmt.Sscanf(ratio, "%f:%f", &rw, &rh)
			require.NoError(t, err)
			require.InEpsilon(t, rw/rh, float64(width)/float64(height), 0.02)
		}
	}
}

func TestSeedreamArkPreservesOtherProvidersAndNativePixels(t *testing.T) {
	body := []byte(`{"model":"doubao-seedream-5-0-pro","size":"3:2","metadata":{"resolution":"1K"}}`)
	unchanged, err := normalizeSeedreamArkRequest(body, "application/json", "https://no-such-host.invalid/v1")
	require.NoError(t, err)
	require.Equal(t, body, unchanged)
	native := []byte(`{"model":"doubao-seedream-5-0-pro-260628","size":"1536x1536","watermark":true,"image":"https://no-such-host.invalid/ref.png"}`)
	actual, err := normalizeSeedreamArkRequest(native, "application/json", "https://ark.cn-beijing.volces.com/api/v3")
	require.NoError(t, err)
	require.JSONEq(t, string(native), string(actual))
}

func TestSeedreamArkRejectsUnboundedOrInvalidParameters(t *testing.T) {
	for _, fields := range []string{`"n":2`, `"size":"0:1"`, `"size":"NaN:1"`, `"size":"17:1"`, `"size":"1:1","metadata":{"resolution":"4K"}`, `"metadata":{"watermark":"false"}`} {
		_, err := normalizeSeedreamArkRequest([]byte(`{"model":"doubao-seedream-5-0-pro-260628",`+fields+`}`), "application/json", "https://ark.cn-beijing.volces.com/api/v3")
		require.Error(t, err)
	}
}

func TestSeedreamArkInvalidCountIsRejectedBeforeSubmission(t *testing.T) {
	body := []byte(`{"model":"v-doubao-seedream-5-0-pro","prompt":"fixture","size":"1:1","n":2}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)
	account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "fixture-only", "base_url": "https://ark.cn-beijing.volces.com/api/v3",
		"model_mapping": map[string]any{"v-doubao-seedream-5-0-pro": "doubao-seedream-5-0-pro-260628"},
	}}
	_, err = svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	var rejected *OpenAIImagesUpstreamError
	require.ErrorAs(t, err, &rejected)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, upstream.requests)
}
