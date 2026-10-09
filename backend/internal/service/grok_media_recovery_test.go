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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type mediaRecoveryRepo struct {
	AccountRepository
	account    *Account
	model      string
	modelUntil time.Time
	tempCalls  int
}

func (r *mediaRecoveryRepo) GetByID(context.Context, int64) (*Account, error) { return r.account, nil }
func (r *mediaRecoveryRepo) SetModelRateLimit(_ context.Context, _ int64, model string, until time.Time, _ ...string) error {
	r.model, r.modelUntil = model, until
	r.account.Extra = map[string]any{modelRateLimitsKey: map[string]any{model: map[string]any{"rate_limit_reset_at": until.Format(time.RFC3339)}}}
	return nil
}
func (r *mediaRecoveryRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.tempCalls++
	return nil
}
func (r *mediaRecoveryRepo) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]Account, error) {
	a := r.account
	if a == nil || !a.IsActive() || !a.Schedulable || groupID != nil && !openAIStickyAccountMatchesGroup(a, groupID) || groupID == nil && !includeGrouped && len(a.GroupIDs) > 0 {
		return nil, nil
	}
	for _, platform := range platforms {
		if a.Platform == platform {
			return []Account{*a}, nil
		}
	}
	return nil, nil
}

type mediaRecoveryCache struct {
	GatewayCache
	key string
}

func (c *mediaRecoveryCache) GetSessionAccountID(_ context.Context, group int64, key string) (int64, error) {
	if group == 4 && key == c.key {
		return 6, nil
	}
	return 0, errors.New("missing binding")
}

func recoveryAccount() *Account {
	return &Account{ID: 6, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{4},
		Credentials: map[string]any{"api_key": "test-only", "model_mapping": map[string]any{"video-alias": "kling-v3-omni", "kling-v3": "kling-v3"}}}
}

func TestMediaCatalogSurvivesCooldownAndHonorsConfiguration(t *testing.T) {
	group := int64(4)
	account := recoveryAccount()
	until := time.Now().Add(2 * time.Minute)
	account.TempUnschedulableUntil = &until
	account.RateLimitResetAt = &until
	repo := &mediaRecoveryRepo{account: account}
	svc := &GatewayService{accountRepo: repo}
	require.Equal(t, []string{"kling-v3", "video-alias"}, svc.GetAvailableModels(context.Background(), &group, PlatformGrok))
	require.Contains(t, svc.GetConfiguredPlatforms(context.Background(), &group), PlatformGrok)
	otherGroup := int64(5)
	require.Empty(t, svc.GetAvailableModels(context.Background(), &otherGroup, PlatformGrok))
	account.Schedulable = false
	require.Empty(t, svc.GetAvailableModels(context.Background(), &group, PlatformGrok))
	require.Empty(t, svc.GetConfiguredPlatforms(context.Background(), &group))
}

func TestGrokMediaModelCircuitDoesNotPauseOtherModels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		scoped bool
	}{
		{"known model outage", 503, `{"error":{"message":"模型 kling-v3-omni 的所有渠道当前不可用（熔断保护已触发）"}}`, true},
		{"generic service outage", 503, `{"error":{"message":"service unavailable"}}`, false},
		{"another model", 503, `{"error":{"message":"模型 other 的所有渠道当前不可用（熔断保护已触发）"}}`, false},
		{"another model with same prefix", 503, `{"error":{"message":"模型 kling-v3-omni-pro 的所有渠道当前不可用（熔断保护已触发）"}}`, false},
		{"credential error", 401, `{"error":{"message":"invalid key"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := recoveryAccount()
			repo := &mediaRecoveryRepo{account: account}
			svc := &OpenAIGatewayService{accountRepo: repo}
			require.Equal(t, tc.scoped, svc.cooldownGrokMediaModel(context.Background(), account, "video-alias", tc.status, []byte(tc.body)))
			require.Zero(t, repo.tempCalls)
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			if tc.scoped {
				require.Equal(t, "kling-v3-omni", repo.model)
				require.WithinDuration(t, time.Now().Add(2*time.Minute), repo.modelUntil, time.Second)
				require.False(t, account.IsSchedulableForModelWithContext(context.Background(), "video-alias"))
				require.True(t, account.IsSchedulableForModelWithContext(context.Background(), "kling-v3"))
			} else {
				require.Empty(t, repo.model)
			}
		})
	}
	// Exercise the actual upstream response handler, not only classification.
	account := recoveryAccount()
	repo := &mediaRecoveryRepo{account: account}
	svc := &OpenAIGatewayService{accountRepo: repo}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/generations", nil)
	_, err := svc.handleGrokMediaErrorResponse(context.Background(), &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"模型 kling-v3-omni 的所有渠道当前不可用（熔断保护已触发）"}}`))}, c, account, "upstream-test", "video-alias")
	require.Error(t, err)
	require.Equal(t, "kling-v3-omni", repo.model)
	require.Zero(t, repo.tempCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
}

func TestGrokVideoLookupRecoveryPreservesOwnerAndSafety(t *testing.T) {
	group := int64(4)
	for _, tc := range []struct {
		name      string
		change    func(*Account, *OpenAIGatewayService)
		user, key int64
		ok        bool
	}{
		{name: "recover original task", user: 6, key: 6, ok: true},
		{name: "another user", user: 7, key: 6},
		{name: "another API key", user: 6, key: 7},
		{name: "revoked group", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) { a.GroupIDs = []int64{5} }},
		{name: "manual pause", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) { a.Schedulable = false }},
		{name: "inactive", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) { a.Status = "disabled" }},
		{name: "credential failure", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) { a.TempUnschedulableReason = "grok credentials unauthorized" }},
		{name: "rate limit", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) { a.RateLimitResetAt = a.TempUnschedulableUntil }},
		{name: "overloaded", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) { a.OverloadUntil = a.TempUnschedulableUntil }},
		{name: "expired", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) {
			past := time.Now().Add(-time.Minute)
			a.ExpiresAt = &past
			a.AutoPauseOnExpired = true
		}},
		{name: "stronger runtime quarantine", user: 6, key: 6, change: func(a *Account, s *OpenAIGatewayService) {
			s.BlockAccountScheduling(a, time.Now().Add(10*time.Minute), "unauthorized")
		}},
		{name: "shorter credential quarantine", user: 6, key: 6, change: func(a *Account, s *OpenAIGatewayService) {
			s.BlockAccountScheduling(a, time.Now().Add(time.Minute), "unauthorized")
		}},
		{name: "transient failure cannot weaken credential quarantine", user: 6, key: 6, change: func(a *Account, s *OpenAIGatewayService) {
			s.BlockAccountScheduling(a, time.Now().Add(time.Minute), "unauthorized")
			s.BlockAccountScheduling(a, time.Now().Add(2*time.Minute), "grok upstream temporary error")
		}},
		{name: "credential rollback restores prior transient recovery", user: 6, key: 6, ok: true, change: func(a *Account, s *OpenAIGatewayService) {
			rollback := s.blockGrokCredentialRuntime(a, time.Now().Add(10*time.Minute), "unauthorized")
			rollback()
		}},
		{name: "OAuth retains credential policy", user: 6, key: 6, change: func(a *Account, _ *OpenAIGatewayService) { a.Type = AccountTypeOAuth }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := recoveryAccount()
			until := time.Now().Add(2 * time.Minute)
			account.TempUnschedulableUntil, account.TempUnschedulableReason = &until, "grok upstream temporary error"
			cache := &mediaRecoveryCache{}
			svc := &OpenAIGatewayService{accountRepo: &mediaRecoveryRepo{account: account}, cache: cache}
			cache.key = svc.openAISessionCacheKey(GrokMediaVideoRequestSessionHash("original-task", 6, 6))
			svc.BlockAccountScheduling(account, until, "grok upstream temporary error")
			if tc.change != nil {
				tc.change(account, svc)
			}
			selection, err := svc.SelectGrokMediaVideoLookupAccount(context.Background(), &group, "original-task", tc.user, tc.key)
			if tc.ok {
				require.NoError(t, err)
				require.Equal(t, int64(6), selection.Account.ID)
				require.True(t, selection.Acquired)
				selection.ReleaseFunc()
				require.False(t, account.IsSchedulable(), "new generations must remain blocked")
				require.True(t, svc.isOpenAIAccountRuntimeBlocked(account), "lookup must not clear shared cooldown")
			} else {
				require.Error(t, err)
				require.Nil(t, selection)
			}
		})
	}
}
