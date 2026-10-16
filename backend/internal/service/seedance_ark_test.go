//go:build unit

package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func arkVideoTestAccount() *Account {
	return &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "fixture-only", "base_url": arkVideoBaseURL,
		"model_mapping": map[string]any{"doubao-seedance-2-5-260628": "doubao-seedance-2-5-260628"},
	}}
}

func TestSeedanceArkAccountCapabilityCannotSelectResellers(t *testing.T) {
	for _, base := range []string{arkVideoBaseURL, arkVideoBaseURL + "/", "https://toapis.cn/v1", "https://ark.cn-beijing.volces.com.evil.invalid/api/v3", "http://ark.cn-beijing.volces.com/api/v3", arkVideoBaseURL + "?token=x", "https://user@ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com:8443/api/v3"} {
		a := arkVideoTestAccount()
		a.Credentials["base_url"] = base
		want := base == arkVideoBaseURL || base == arkVideoBaseURL+"/"
		require.Equal(t, want, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityArkVideo), base)
	}
	a := arkVideoTestAccount()
	a.Platform = PlatformGrok
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityArkVideo))
	a.Platform, a.Type = PlatformOpenAI, AccountTypeOAuth
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityArkVideo))
}

func TestSeedanceArkForwardUsesOfficialTaskContractAndFixedDefault(t *testing.T) {
	for _, firstFrame := range []bool{false, true} {
		body := `{"model":"doubao-seedance-2-5-260628","prompt":"fixture","aspect_ratio":"9:16"}`
		if firstFrame {
			body = `{"model":"doubao-seedance-2-5-260628","prompt":"fixture","aspect_ratio":"9:16","image":"https://no-such-host.invalid/frame.png","video_generation_mode":"first_frame_image_to_video"}`
		}
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"cgt-test"}`))}}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
		result, err := svc.ForwardGrokMedia(context.Background(), c, arkVideoTestAccount(), GrokMediaEndpointVideosGenerations, "", []byte(body), "application/json")
		require.NoError(t, err)
		require.Len(t, upstream.requests, 1)
		require.Equal(t, arkVideoBaseURL+"/contents/generations/tasks", upstream.lastReq.URL.String())
		require.Equal(t, "Bearer fixture-only", upstream.lastReq.Header.Get("Authorization"))
		require.Equal(t, int64(5), gjson.GetBytes(upstream.lastBody, "duration").Int())
		require.Equal(t, "720p", gjson.GetBytes(upstream.lastBody, "resolution").String())
		require.Equal(t, "fixture", gjson.GetBytes(upstream.lastBody, "content.0.text").String())
		if firstFrame {
			require.Equal(t, "first_frame", gjson.GetBytes(upstream.lastBody, "content.1.role").String())
			require.Equal(t, "adaptive", gjson.GetBytes(upstream.lastBody, "ratio").String())
		} else {
			require.Equal(t, "9:16", gjson.GetBytes(upstream.lastBody, "ratio").String())
		}
		require.Equal(t, "cgt-test", result.ResponseID)
		require.Equal(t, 5, result.VideoDurationSeconds)
		require.Equal(t, "queued", gjson.Get(w.Body.String(), "status").String())
	}
}

func TestSeedanceArkRejectsUnboundedParametersBeforeDispatch(t *testing.T) {
	for _, fields := range []string{`"duration":-1`, `"duration":3`, `"duration":31`, `"duration":null`, `"duration":5.5`, `"frames":240`, `"content":[]`, `"n":2`, `"resolution":"4k"`, `"aspect_ratio":"garbage"`, `"image":[]`, `"video_generation_mode":"first_frame_image_to_video"`} {
		c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
		upstream := &httpUpstreamRecorder{}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
		_, err := svc.ForwardGrokMedia(context.Background(), c, arkVideoTestAccount(), GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"doubao-seedance-2-5-260628","prompt":"fixture",`+fields+`}`), "application/json")
		require.Error(t, err, fields)
		require.Equal(t, 400, w.Code, fields)
		require.Empty(t, upstream.requests)
	}
	_, err := normalizeSeedanceArkRequest([]byte(`{"prompt":"fixture --dur 30"}`), "doubao-seedance-2-5-260628")
	require.Error(t, err)
	_, err = normalizeSeedanceArkRequest([]byte(`{"prompt":"fixture"}`), "seedance-2-5")
	require.Error(t, err)
}

func TestSeedanceArkPollingContentAndFailure(t *testing.T) {
	signedURL := "https://ark-content-generation-cn-beijing.tos-cn-beijing.volces.com/task.mp4?signature=fixture"
	body := `{"id":"cgt-test","status":"succeeded","duration":5,"content":{"video_url":"` + signedURL + `"}}`
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/cgt-test", nil)
	_, err := svc.ForwardGrokMedia(context.Background(), c, arkVideoTestAccount(), GrokMediaEndpointVideoStatus, "cgt-test", nil, "")
	require.NoError(t, err)
	require.Equal(t, arkVideoBaseURL+"/contents/generations/tasks/cgt-test", upstream.request.URL.String())
	require.Equal(t, "/v1/videos/cgt-test/content", gjson.Get(w.Body.String(), "video.url").String())
	require.NotContains(t, w.Body.String(), "signature=")
	upstream.responses = []*http.Response{grokMediaContentStatusResponse(body), {StatusCode: 206, Header: http.Header{"Content-Type": []string{"video/mp4"}}, Body: io.NopCloser(strings.NewReader("fixture-video"))}}
	c, w = grokMediaContentTestContext(http.MethodGet, "/v1/videos/cgt-test/content", map[string]string{"Range": "bytes=0-12"})
	_, err = svc.ForwardGrokMedia(context.Background(), c, arkVideoTestAccount(), GrokMediaEndpointVideoContent, "cgt-test", nil, "")
	require.NoError(t, err)
	require.Equal(t, signedURL, upstream.request.URL.String())
	require.Empty(t, upstream.request.Header.Get("Authorization"), "never send the Ark key to object storage")
	require.Equal(t, "bytes=0-12", upstream.request.Header.Get("Range"))
	require.Equal(t, "fixture-video", w.Body.String())
	for _, status := range []string{"failed", "expired", "cancelled"} {
		upstream.response = grokMediaContentStatusResponse(`{"id":"cgt-test","status":"` + status + `","error":{"message":"fixture"}}`)
		c, _ = grokMediaContentTestContext(http.MethodGet, "/v1/videos/cgt-test", nil)
		result, err := svc.ForwardGrokMedia(context.Background(), c, arkVideoTestAccount(), GrokMediaEndpointVideoStatus, "cgt-test", nil, "")
		require.NoError(t, err)
		require.True(t, result.TaskFailed, status)
	}
	for _, raw := range []string{"http://127.0.0.1/video.mp4", "https://ark-content-generation-cn-beijing.tos-cn-beijing.volces.com.evil.invalid/video.mp4", "https://toapis.cn/video.mp4"} {
		encoded, _ := json.Marshal(map[string]any{"status": "succeeded", "content": map[string]string{"video_url": raw}})
		_, err := arkVideoContentURL(encoded)
		require.Error(t, err)
	}
}

func TestSeedanceArkVideoQuotaBoundsAndOwnership(t *testing.T) {
	for _, model := range []string{"doubao-seedance-2-5-260628", "doubao-seedance-2-0-260128"} {
		units, err := MediaQuotaVideoUnits(model, 0)
		require.NoError(t, err)
		require.Equal(t, int64(5), units)
		_, err = MediaQuotaVideoUnits(model, -1)
		require.Error(t, err)
	}
	units, err := MediaQuotaVideoUnits("doubao-seedance-2-5-260628", 30)
	require.NoError(t, err)
	require.Equal(t, int64(30), units)
	_, err = MediaQuotaVideoUnits("doubao-seedance-2-0-260128", 30)
	require.Error(t, err)
	account := recoveryAccount()
	account.Platform = PlatformOpenAI
	account.Credentials["base_url"] = arkVideoBaseURL
	cache := &mediaRecoveryCache{}
	svc := &OpenAIGatewayService{accountRepo: &mediaRecoveryRepo{account: account}, cache: cache}
	cache.key = svc.openAISessionCacheKey(GrokMediaVideoRequestSessionHash("cgt-test", 6, 6))
	group := int64(4)
	selection, err := svc.SelectGrokMediaVideoLookupAccount(context.Background(), &group, "cgt-test", 6, 6)
	require.NoError(t, err)
	require.Equal(t, account.ID, selection.Account.ID)
	selection.ReleaseFunc()
	_, err = svc.SelectGrokMediaVideoLookupAccount(context.Background(), &group, "cgt-test", 7, 6)
	require.Error(t, err)
	account.GroupIDs = []int64{5}
	_, err = svc.SelectGrokMediaVideoLookupAccount(context.Background(), &group, "cgt-test", 6, 6)
	require.Error(t, err)
}

func TestSeedanceArkSchedulerDoesNotFallBackToMatchingReseller(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	group := int64(10114)
	model := "doubao-seedance-2-5-260628"
	official := *arkVideoTestAccount()
	official.Status, official.Schedulable, official.Concurrency = StatusActive, true, 1
	reseller := *arkVideoTestAccount()
	reseller.ID, reseller.Status, reseller.Schedulable, reseller.Concurrency = 99, StatusActive, true, 1
	reseller.Credentials = map[string]any{"api_key": "fixture-only", "base_url": "https://toapis.cn/v1", "model_mapping": map[string]any{model: model}}
	for _, accounts := range [][]Account{{reseller, official}, {reseller}} {
		svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{}, cfg: &config.Config{}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
		selected, _, err := svc.SelectAccountWithSchedulerForCapability(context.Background(), &group, "", "", model, nil,
			OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityArkVideo, false, false, false, PlatformOpenAI)
		if len(accounts) == 2 {
			require.NoError(t, err)
			require.Equal(t, official.ID, selected.Account.ID)
		} else {
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selected)
		}
	}
}

func TestSeedanceArkVideoEntitlementDoesNotQuarantineOfficialImages(t *testing.T) {
	a := arkVideoTestAccount()
	repo := &mediaRecoveryRepo{account: a}
	upstream := &grokMediaContentUpstreamStub{response: &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"ModelNotOpen","message":"model is not activated"}}`))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, accountRepo: repo}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
	_, err := svc.ForwardGrokMedia(context.Background(), c, a, GrokMediaEndpointVideosGenerations, "", []byte(`{"model":"doubao-seedance-2-5-260628","prompt":"fixture"}`), "application/json")
	require.Error(t, err)
	require.Equal(t, 403, w.Code)
	require.Zero(t, repo.tempCalls)
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(a))
}
