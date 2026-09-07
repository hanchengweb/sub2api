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

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// 账号 model_mapping 的值是运营者显式配置的上游真名，不应再按 gpt-image-*/
// grok-imagine-* 的命名习惯二次否决——否则火山方舟 doubao-seedream-*、Gemini
// image、nano_banana 这类非该命名的图片上游一概接不进来，且无配置可绕开。
func TestForwardImages_APIKeyAllowsNonGPTImageUpstreamName(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-image-1","prompt":"a plain grey square","size":"1024x1024"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	recorder := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"created":1710000000,"data":[{"b64_json":"aGk="}]}`)),
		},
	}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: recorder}

	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.NoError(t, err)

	account := &Account{
		ID:       2,
		Name:     "volcengine-seedream",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://ark.cn-beijing.volces.com/api/v3",
			"model_mapping": map[string]any{
				"gpt-image-1": "doubao-seedream-5-0-pro-260628",
			},
		},
	}

	_, forwardErr := svc.ForwardImages(context.Background(), c, account, body, parsed, "")
	if forwardErr != nil {
		require.NotContains(t, forwardErr.Error(), "requires an image model",
			"上游真名不应再因命名习惯被拒")
	}

	require.NotNil(t, recorder.lastBody, "请求应已转发到上游，而不是在本地被拦下")
	require.Contains(t, string(recorder.lastBody), "doubao-seedream-5-0-pro-260628",
		"转发出去的 model 应已重写为账号映射的上游真名")
}

// 对外契约不变：用户拿文本模型调图片接口仍在解析期被拒。
func TestParseOpenAIImagesRequest_StillRejectsTextModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-v4-flash","prompt":"draw a cat"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req

	svc := &OpenAIGatewayService{}
	parsed, err := svc.ParseOpenAIImagesRequest(c, body)
	require.Nil(t, parsed)
	require.ErrorContains(t, err, `images endpoint requires an image model, got "deepseek-v4-flash"`)
}
