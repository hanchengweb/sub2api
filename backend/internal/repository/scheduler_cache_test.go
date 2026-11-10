package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFilterSchedulerCredentialsKeepsSubscriptionPlanType(t *testing.T) {
	filtered := filterSchedulerCredentials(map[string]any{
		"plan_type":     "plus",
		"access_token":  "secret-access-token",
		"refresh_token": "secret-refresh-token",
	})

	require.Equal(t, "plus", filtered["plan_type"])
	require.NotContains(t, filtered, "access_token")
	require.NotContains(t, filtered, "refresh_token")
}

func TestSchedulerMetadataAccountKeepsOpenAISubscriptionIdentity(t *testing.T) {
	account := service.Account{
		ID:       24,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"plan_type":    "plus",
			"access_token": "secret-access-token",
		},
	}

	metadata := buildSchedulerMetadataAccount(account)

	require.True(t, metadata.IsOpenAIChatGPTSubscription())
	require.Empty(t, metadata.GetCredential("access_token"))
}

func TestSchedulerMetadataRoundTripPreservesVideoRoutingEligibility(t *testing.T) {
	for _, tc := range []struct {
		name       string
		platform   string
		baseURL    string
		model      string
		capability service.OpenAIEndpointCapability
		eligible   bool
	}{
		{"official_h3", service.PlatformGrok, "https://cp.compshare.cn", service.CompShareH3Model, service.OpenAIEndpointCapabilityCompShareH3, true},
		{"ordinary_grok_media", service.PlatformGrok, "https://api.x.ai/v1", "grok-imagine-video", service.OpenAIEndpointCapabilityGrokMediaGeneration, true},
		{"official_ark_video", service.PlatformOpenAI, "https://ark.cn-beijing.volces.com/api/v3", "doubao-seedance-1-0-pro", service.OpenAIEndpointCapabilityArkVideo, true},
		{"reseller_is_not_h3", service.PlatformGrok, "https://toapis.cn/v1", service.CompShareH3Model, service.OpenAIEndpointCapabilityCompShareH3, false},
		{"lookalike_origin_is_not_h3", service.PlatformGrok, "https://cp.compshare.cn.evil.invalid", service.CompShareH3Model, service.OpenAIEndpointCapabilityCompShareH3, false},
		{"wrong_platform_is_not_h3", service.PlatformOpenAI, "https://cp.compshare.cn", service.CompShareH3Model, service.OpenAIEndpointCapabilityCompShareH3, false},
		{"reseller_is_not_ark", service.PlatformOpenAI, "https://toapis.cn/v1", "doubao-seedance-1-0-pro", service.OpenAIEndpointCapabilityArkVideo, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := service.Account{
				ID: 27, Platform: tc.platform, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{4},
				Credentials: map[string]any{
					"base_url":      tc.baseURL,
					"api_key":       "fixture-api-key",
					"model_mapping": map[string]any{tc.model: tc.model},
				},
			}
			// Exercise the metadata wire format consumed by GetSnapshot before
			// the scheduler hydrates full credentials or rechecks the database.
			_, payload, err := marshalSchedulerCacheAccount(account)
			require.NoError(t, err)
			metadata, err := decodeCachedAccount(payload)
			require.NoError(t, err)
			require.True(t, metadata.IsSchedulableForModel(tc.model))
			require.True(t, metadata.IsModelSupported(tc.model))
			require.Equal(t, tc.eligible, metadata.SupportsOpenAIEndpointCapability(tc.capability))
		})
	}
}

func TestSchedulerMetadataRoundTripRetainsCredentialBoundary(t *testing.T) {
	account := service.Account{
		ID: 27, Platform: service.PlatformGrok, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{
			"base_url":      "https://cp.compshare.cn",
			"api_key":       "fixture-api-key",
			"model_mapping": map[string]any{service.CompShareH3Model: service.CompShareH3Model},
			"access_token":  "fixture-access-secret",
			"refresh_token": "fixture-refresh-secret",
			"client_secret": "fixture-client-secret",
			"cookie":        "fixture-cookie-secret",
		},
	}
	full, payload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)
	metadata, err := decodeCachedAccount(payload)
	require.NoError(t, err)
	require.True(t, metadata.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilityCompShareH3))
	// api_key was already part of the scheduler contract; retaining the route
	// must not widen the whitelist to other authentication material.
	require.Equal(t, "fixture-api-key", metadata.GetCredential("api_key"))
	for _, secret := range []string{"fixture-access-secret", "fixture-refresh-secret", "fixture-client-secret", "fixture-cookie-secret"} {
		require.Contains(t, string(full), secret)
		require.NotContains(t, string(payload), secret)
	}
}

func TestSchedulerMetadataAccountProjectsUpstreamBillingProbe(t *testing.T) {
	lastError := strings.Repeat("upstream diagnostic ", 512)
	probe := map[string]any{
		"status": "ok",
		"data": map[string]any{
			"billing_scope":             "token",
			"resolved_rate_multiplier":  0.03,
			"peak_rate_enabled":         true,
			"peak_start":                "09:00",
			"peak_end":                  "18:00",
			"peak_rate_multiplier":      2.0,
			"timezone":                  "Asia/Shanghai",
			"effective_rate_multiplier": 0.03,
			"remote_diagnostic":         lastError,
		},
		"received_at":   "2026-07-13T10:00:00Z",
		"fresh_until":   "2026-07-13T11:00:00Z",
		"next_probe_at": "2026-07-13T10:30:00Z",
		"http_status":   502,
		"last_error":    lastError,
	}
	account := service.Account{
		ID: 42,
		Extra: map[string]any{
			"upstream_billing_probe": probe,
			"unused_large_field":     "drop-me",
		},
	}

	metadata := buildSchedulerMetadataAccount(account)
	fullPayload, metaPayload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)

	filtered, ok := metadata.Extra["upstream_billing_probe"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ok", filtered["status"])
	require.Equal(t, "2026-07-13T10:00:00Z", filtered["received_at"])
	require.Equal(t, "2026-07-13T11:00:00Z", filtered["fresh_until"])
	require.Equal(t, "2026-07-13T10:30:00Z", filtered["next_probe_at"])
	require.NotContains(t, filtered, "http_status")
	require.NotContains(t, filtered, "last_error")
	filteredData, ok := filtered["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "token", filteredData["billing_scope"])
	require.Equal(t, 0.03, filteredData["resolved_rate_multiplier"])
	require.Equal(t, true, filteredData["peak_rate_enabled"])
	require.Equal(t, "09:00", filteredData["peak_start"])
	require.Equal(t, "18:00", filteredData["peak_end"])
	require.Equal(t, 2.0, filteredData["peak_rate_multiplier"])
	require.Equal(t, "Asia/Shanghai", filteredData["timezone"])
	require.NotContains(t, filteredData, "effective_rate_multiplier")
	require.NotContains(t, filteredData, "remote_diagnostic")
	require.NotContains(t, metadata.Extra, "unused_large_field")
	require.Contains(t, string(fullPayload), lastError)
	require.NotContains(t, string(metaPayload), "last_error")
	require.Less(t, len(metaPayload)*4, len(fullPayload))
}

func TestSchedulerMetadataAccountDropsInvalidUpstreamBillingProbe(t *testing.T) {
	for _, probe := range []any{
		"invalid",
		map[string]any{},
		map[string]any{"status": ""},
	} {
		metadata := buildSchedulerMetadataAccount(service.Account{
			Extra: map[string]any{service.UpstreamBillingProbeExtraKey: probe},
		})

		require.NotContains(t, metadata.Extra, service.UpstreamBillingProbeExtraKey)
	}
}
