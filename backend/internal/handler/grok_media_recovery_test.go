package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type videoRecoveryAccountRepo struct {
	service.AccountRepository
	account *service.Account
}

func (r *videoRecoveryAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

func (r *videoRecoveryAccountRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}

type videoRecoveryBindingCache struct {
	service.GatewayCache
	key    string
	writes int
	err    error
}

func (r *videoRecoveryBindingCache) GetSessionAccountID(_ context.Context, group int64, key string) (int64, error) {
	if r.err != nil {
		return 0, r.err
	}
	if group == 4 && key == r.key {
		return 6, nil
	}
	return 0, nil
}

func (r *videoRecoveryBindingCache) SetSessionAccountID(_ context.Context, _ int64, key string, _ int64, _ time.Duration) error {
	r.key = key
	r.writes++
	return nil
}

type videoRecoveryUpstream struct {
	service.HTTPUpstream
	requests []*http.Request
	status   int
}

func (u *videoRecoveryUpstream) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.requests = append(u.requests, r)
	if u.status != 0 {
		return &http.Response{StatusCode: u.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Service temporarily unavailable"}}`))}, nil
	}
	body, contentType := `{"id":"original-task","status":"completed","progress":100}`, "application/json"
	if strings.HasSuffix(r.URL.Path, "/content") {
		body, contentType = "original-video", "video/mp4"
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestGrokVideoRecoveryHandlerKeepsPaidTaskAndBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                                                 string
		content, foreignKey, paused, cacheDown, upstreamDown bool
		status                                               int
	}{
		{name: "status after balance exhausted and upstream cooldown", status: 200},
		{name: "content after balance exhausted and upstream cooldown", content: true, status: 200},
		{name: "another key cannot retrieve task", foreignKey: true, status: 404},
		{name: "manual pause remains enforced", paused: true, status: 503},
		{name: "binding outage does not claim task is missing", cacheDown: true, status: 503},
		{name: "upstream outage preserves original task without resubmit", upstreamDown: true, status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := int64(4)
			until := time.Now().Add(2 * time.Minute)
			account := &service.Account{ID: 6, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: !tc.paused, Concurrency: 1, GroupIDs: []int64{group},
				TempUnschedulableUntil: &until, TempUnschedulableReason: "grok upstream temporary error",
				Credentials: map[string]any{"api_key": "fixture-only", "base_url": "https://relay.example/v1"}}
			cache := &videoRecoveryBindingCache{}
			upstream := &videoRecoveryUpstream{}
			if tc.upstreamDown {
				upstream.status = http.StatusServiceUnavailable
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			concurrencyCache := &helperConcurrencyCacheStub{accountSeq: []bool{false, true}, userSeq: []bool{true}}
			concurrency := service.NewConcurrencyService(concurrencyCache)
			gateway := service.NewOpenAIGatewayService(&videoRecoveryAccountRepo{account: account}, nil, nil, nil, nil, nil, cache, cfg, nil, concurrency, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
			gateway.BlockAccountScheduling(account, until, "grok upstream temporary error")
			require.NoError(t, gateway.BindGrokMediaVideoRequestAccount(context.Background(), &group, "original-task", 6, 6, 6))
			cache.writes = 0
			if tc.cacheDown {
				cache.err = errors.New("binding store unavailable")
			}
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			keyID := int64(6)
			if tc.foreignKey {
				keyID = 7
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			path := "/v1/videos/original-task"
			if tc.content {
				path += "/content"
			}
			c.Request = httptest.NewRequest(http.MethodGet, path, nil)
			c.Params = gin.Params{{Key: "request_id", Value: "original-task"}}
			c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: keyID, UserID: 6, GroupID: &group, Group: &service.Group{ID: group}, User: &service.User{ID: 6, Balance: 0}})
			c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 6, Concurrency: 1})
			if tc.content {
				h.GrokVideoContent(c)
			} else {
				h.GrokVideoStatus(c)
			}
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			require.Zero(t, cache.writes, "lookups must not shorten, delete or rebind the task")
			if tc.status == 200 {
				require.NotEmpty(t, upstream.requests)
				for _, request := range upstream.requests {
					require.Equal(t, http.MethodGet, request.Method)
				}
				require.Equal(t, 2, concurrencyCache.accountAcquireCalls, "exercise queued slot acquisition")
				require.Equal(t, 1, concurrencyCache.accountReleaseCalls)
				if tc.content {
					require.Equal(t, "original-video", recorder.Body.String())
				}
			} else {
				if tc.upstreamDown {
					require.Len(t, upstream.requests, 1)
					require.Equal(t, http.MethodGet, upstream.requests[0].Method)
				} else {
					require.Empty(t, upstream.requests)
				}
				if tc.status == 503 {
					require.Equal(t, "15", recorder.Header().Get("Retry-After"))
				}
			}
		})
	}
}
