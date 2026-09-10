//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 这个端点用服务器身份去取任意 URL，白名单是它唯一的防线——
// 放开就能被拿来探测内网与云元数据地址（169.254.169.254）。
func TestMediaProxyRejectsDisallowedTargets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &PlaygroundHandler{}

	cases := []struct {
		name string
		url  string
		want int
	}{
		{"缺参数", "", http.StatusBadRequest},
		{"非 https", "http://files.toapis.cn/a.png", http.StatusBadRequest},
		{"云元数据地址", "https://169.254.169.254/latest/meta-data/", http.StatusForbidden},
		{"内网地址", "https://127.0.0.1/admin", http.StatusForbidden},
		{"任意外部域名", "https://evil.example.com/x.png", http.StatusForbidden},
		{"白名单的子域伪装", "https://files.toapis.cn.evil.com/x.png", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/media?url="+c.url, nil)
			h.Media(ctx)
			if rec.Code != c.want {
				t.Fatalf("%s: 状态码 = %d, want %d", c.url, rec.Code, c.want)
			}
		})
	}
}

func TestMediaProxyAllowsUpstreamHost(t *testing.T) {
	if _, ok := mediaProxyAllowedHosts["files.toapis.cn"]; !ok {
		t.Fatal("上游图床域名必须在白名单里，否则图片永远取不到")
	}
}
