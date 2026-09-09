//go:build unit

package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func imagesRequestContext(t *testing.T, body []byte) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}

// 默认档在任何配置下都必须放行，否则等于把生图整个关掉。
func TestValidateOpenAIImagesQualityAllowsDefaultTiers(t *testing.T) {
	svc := &OpenAIGatewayService{}
	for _, q := range []string{"", "  ", "low", "LOW", "standard", " Standard "} {
		req := &OpenAIImagesRequest{Model: "gpt-image-2", Quality: q}
		if err := svc.validateOpenAIImagesQuality(imagesRequestContext(t, nil), req); err != nil {
			t.Fatalf("quality %q 应放行，实际报错：%v", q, err)
		}
	}
}

// 拿不到分组/渠道定价时放行：查不到定价说明走的不是渠道自定义按次价，
// 而且不能让「查不到定价」升级成生图全线不可用。
func TestValidateOpenAIImagesQualitySkipsWithoutPricing(t *testing.T) {
	svc := &OpenAIGatewayService{}
	req := &OpenAIImagesRequest{Model: "gpt-image-2", Quality: "high"}
	if err := svc.validateOpenAIImagesQuality(imagesRequestContext(t, nil), req); err != nil {
		t.Fatalf("无渠道定价时应放行，实际报错：%v", err)
	}
}

// 端到端：原生 OpenAI（token 计费）那条路径不受影响——一刀切会把它也砍掉。
func TestParseOpenAIImagesRequestKeepsQualityForTokenBilling(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat","size":"1024x1024","quality":"high"}`)
	svc := &OpenAIGatewayService{}
	parsed, err := svc.ParseOpenAIImagesRequest(imagesRequestContext(t, body), body)
	if err != nil {
		t.Fatalf("token 计费路径不应被拦：%v", err)
	}
	if parsed.Quality != "high" {
		t.Fatalf("quality 应原样保留，实际 %q", parsed.Quality)
	}
}
