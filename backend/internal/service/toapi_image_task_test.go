//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type toAPI26PollStub struct {
	calls  int
	bodies []string
	err    error
}

func (f *toAPI26PollStub) Do(r *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	f.calls++
	if r.Method != http.MethodGet || r.URL.String() != "https://toapis.cn/v1/images/generations/tsk_img_test" || r.Body != nil || r.Header.Get("Authorization") != "Bearer fixture" {
		return nil, errors.New("wrong task transport")
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.calls > len(f.bodies) {
		return nil, errors.New("unexpected extra poll")
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(f.bodies[f.calls-1]))}, nil
}
func (f *toAPI26PollStub) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return f.Do(r, p, id, n)
}

func TestToAPI26TaskWaitsAndPreservesExactUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, w := gin.CreateTestContext(httptest.NewRecorder())
	_ = w
	r, _ := http.NewRequest(http.MethodPost, "https://toapis.cn/v1/images/generations", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer fixture")
	f := &toAPI26PollStub{bodies: []string{`{"id":"tsk_img_test","status":"completed","billing":{"cost_usd":"0.019852"},"usage":{"input_tokens":19,"output_tokens":824,"input_tokens_details":{"image_tokens":0,"cached_tokens":0,"cached_tokens_details":{"image_tokens":0,"text_tokens":0}}},"result":{"data":[{"url":"https://example.com/fixture.png"}]}}`}}
	initial := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"tsk_img_test","status":"queued"}`))}
	result, err := awaitToAPI26ImageTask(context.Background(), c, &Account{ID: 23}, r, initial, "", f, time.Millisecond, time.Second)
	require.NoError(t, err)
	body, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	u, ok := extractOpenAIUsageFromJSONBytes(body)
	require.NoError(t, validateGPTImage26TokenUsage(u, ok))
	require.Equal(t, 824, u.OutputTokens)
	require.True(t, u.CacheReadImageInputTokensKnown)
	require.Equal(t, 1, f.calls)
	require.NotContains(t, string(body), "billing")
	require.Contains(t, string(body), "data")
}

func TestToAPI26TaskUnknownDoesNotSubmitOrReturnImages(t *testing.T) {
	for _, test := range []struct {
		name, body   string
		transportErr error
	}{
		{"mismatch", `{"id":"tsk_img_other","status":"completed","result":{"data":[{}]}}`, nil},
		{"missing_usage", `{"id":"tsk_img_test","status":"completed","result":{"data":[{}]}}`, nil},
		{"unknown_state", `{"id":"tsk_img_test","status":"unknown"}`, nil},
		{"transport", "", errors.New("unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			r, _ := http.NewRequest(http.MethodPost, "https://toapis.cn/v1/images/generations", nil)
			r.Header.Set("Authorization", "Bearer fixture")
			f := &toAPI26PollStub{bodies: []string{test.body}, err: test.transportErr}
			initial := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"tsk_img_test","status":"pending"}`))}
			result, err := awaitToAPI26ImageTask(context.Background(), c, &Account{ID: 23}, r, initial, "", f, time.Millisecond, time.Second)
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, 1, f.calls)
			require.Equal(t, 409, w.Code)
			require.Contains(t, w.Body.String(), `"retryable":false`)
			require.Contains(t, w.Body.String(), "tsk_img_test")
		})
	}
}

func TestToAPI26TaskCancelledKeepsOriginalID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	r, _ := http.NewRequest(http.MethodPost, "https://toapis.cn/v1/images/generations", nil)
	f := &toAPI26PollStub{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	initial := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"tsk_img_test","status":"queued"}`))}
	_, err := awaitToAPI26ImageTask(ctx, c, &Account{ID: 23}, r, initial, "", f, time.Hour, time.Second)
	require.Error(t, err)
	require.Equal(t, 0, f.calls)
	require.Contains(t, w.Body.String(), "tsk_img_test")
}

func TestToAPI26TaskHeaderPreservesHeartbeat(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	stop := StartOpenAIImagesJSONKeepalive(c, time.Hour)
	defer stop()
	setToAPI26TaskHeader(c, "tsk_img_test")
	k := openAIImagesJSONKeepaliveFromContext(c)
	require.True(t, k.beat())
	require.Equal(t, "tsk_img_test", w.Header().Get("X-WindHub-Upstream-Task-ID"))
	require.Equal(t, -1, OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c))
}

func TestToAPI26TaskErrorAfterHeartbeatRetainsOriginalID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	stop := StartOpenAIImagesJSONKeepalive(c, time.Hour)
	defer stop()
	require.True(t, openAIImagesJSONKeepaliveFromContext(c).beat())
	require.Error(t, toAPI26TaskError(c, "tsk_img_test", "upstream_task_result_unknown"))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"retryable":false`)
	require.Contains(t, w.Body.String(), `"task_id":"tsk_img_test"`)
	require.JSONEq(t, `{"error":{"type":"upstream_task_error","code":"upstream_task_result_unknown","message":"图片任务结果尚未确认，请勿重复生成；请凭原任务编号查询或联系管理员。","task_id":"tsk_img_test","retryable":false}}`, strings.TrimSpace(w.Body.String()))
}
