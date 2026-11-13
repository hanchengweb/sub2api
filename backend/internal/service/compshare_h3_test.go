//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func compShareH3TestAccount() *Account {
	return &Account{ID: 18, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 2, GroupIDs: []int64{4}, Credentials: map[string]any{"base_url": compShareH3BaseURL, "api_key": "fixture-only", "model_mapping": map[string]any{CompShareH3Model: CompShareH3Model}}}
}
func compShareH3TestSnapshot() *CompShareH3Snapshot {
	body, _ := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","prompt":"fixture","resolution":"4k"}`))
	return &CompShareH3Snapshot{RequestID: "csh3-fixture", UserID: 1, APIKeyID: 7, GroupID: 4, AccountID: 18, Request: body, Resolution: "4k", Duration: 5, Prices: map[string]float64{"480p": 1, "768p": 2, "1080p": 3, "2k": 4, "4k": 5}, Multiplier: 2, BaseCost: 25, Credits: 50}
}

type compShareH3RepoStub struct {
	UsageBillingRepository
	snap                   *CompShareH3Snapshot
	bindCalls, settleCalls int
	claimErr, settleErr    error
}

func (r *compShareH3RepoStub) GetCompShareH3(_ context.Context, id string, user, key int64) (*CompShareH3Snapshot, error) {
	if r.snap != nil && (id == r.snap.RequestID || id == r.snap.TaskID) && user == r.snap.UserID && key == r.snap.APIKeyID {
		return r.snap, nil
	}
	return nil, nil
}
func (r *compShareH3RepoStub) ClaimCompShareH3(context.Context, string) (bool, error) {
	if r.claimErr != nil {
		return false, r.claimErr
	}
	if r.snap.Dispatched {
		return false, nil
	}
	r.snap.Dispatched = true
	return true, nil
}
func (r *compShareH3RepoStub) BindCompShareH3(_ context.Context, id, task string) error {
	r.bindCalls++
	r.snap.TaskID = task
	return nil
}
func (r *compShareH3RepoStub) SettleCompShareH3(_ context.Context, id string, user, key int64, status, res string, seconds float64) (float64, error) {
	r.settleCalls++
	if r.settleErr != nil {
		return 0, r.settleErr
	}
	_, err := CompShareH3SettlementAmount(r.snap, status, res, seconds)
	return 0, err
}

func TestCompShareH3ExpiredUndispatchedQuotaReleasesReservation(t *testing.T) {
	for _, tc := range []struct {
		name                string
		claimErr, settleErr error
		wantSettlements     int
	}{
		{"expired", ErrMediaQuotaExceeded, nil, 1},
		{"expiry_release_pending", ErrMediaQuotaExceeded, errors.New("fixture settlement unavailable"), 1},
		{"claim_unknown", errors.New("fixture transport unavailable"), nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snap := compShareH3TestSnapshot()
			snap.MediaQuotaRequestID = snap.RequestID
			repo := &compShareH3RepoStub{snap: snap, claimErr: tc.claimErr, settleErr: tc.settleErr}
			svc := &OpenAIGatewayService{usageBillingRepo: repo}
			claimed, err := svc.ClaimCompShareH3(context.Background(), snap)
			require.False(t, claimed)
			require.Error(t, err)
			require.False(t, snap.Dispatched)
			require.Equal(t, tc.wantSettlements, repo.settleCalls)
			if tc.settleErr != nil {
				require.ErrorIs(t, err, tc.settleErr)
				require.NotErrorIs(t, err, ErrMediaQuotaExceeded)
			} else {
				require.ErrorIs(t, err, tc.claimErr)
			}
		})
	}
}

func TestCompShareH3TerminalResponseUsesFrozenBillingMode(t *testing.T) {
	for _, organizationQuota := range []bool{false, true} {
		snap := compShareH3TestSnapshot()
		snap.TaskID = "provider-fixture"
		wantMode := "balance"
		if organizationQuota {
			snap.MediaQuotaRequestID = snap.RequestID
			wantMode = "organization_quota"
		}
		repo := &compShareH3RepoStub{snap: snap}
		upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{grokMediaContentStatusResponse(`{"task":{"id":"provider-fixture","status":"succeeded","resolution":"768P","usage":{"output_seconds":4},"content":{"url":"https://cdn-files.compshare.cn/fixture.mp4"}}}`)}}
		svc := &OpenAIGatewayService{cfg: &config.Config{}, usageBillingRepo: repo, httpUpstream: upstream}
		c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/provider-fixture", nil)
		c.Set(CompShareH3ContextKey, snap)
		_, err := svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideoStatus, snap.TaskID, nil, "")
		require.NoError(t, err)
		require.Equal(t, 200, w.Code)
		require.Equal(t, wantMode, gjson.Get(w.Body.String(), "billing_mode").String())
		require.Equal(t, int64(4), gjson.Get(w.Body.String(), "video.duration").Int())
		require.Equal(t, 1, repo.settleCalls)
	}
}

func TestCompShareH3RequestBoundariesAndExactTiers(t *testing.T) {
	for _, res := range []string{"480p", "768p", "1080p", "2k", "4k"} {
		body, err := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","prompt":"fixture","resolution":"` + res + `"}`))
		require.NoError(t, err)
		require.Equal(t, strings.ToUpper(res), gjson.GetBytes(body, "resolution").String())
		require.Equal(t, int64(5), gjson.GetBytes(body, "duration").Int())
		require.Equal(t, "16:9", gjson.GetBytes(body, "ratio").String())
		require.False(t, gjson.GetBytes(body, "use_context_ir").Bool())
		require.Equal(t, res, ParseGrokMediaRequest("application/json", body).Resolution)
	}
	for _, d := range []string{"4", "30"} {
		_, err := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","prompt":"f","duration":` + d + `}`))
		require.NoError(t, err)
	}
	for _, fields := range []string{`"duration":3`, `"duration":31`, `"duration":4.5`, `"duration":null`, `"duration":"5"`, `"resolution":"720p"`, `"resolution":"1440p"`, `"ratio":"garbage"`, `"n":2`, `"use_context_ir":true`, `"use_context_ir":null`, `"skill_id":"fixture"`, `"callback_url":"https://no-such-host.invalid/callback"`, `"callback_token":"fixture"`, `"video_url":"https://no-such-host.invalid/v.mp4"`} {
		_, err := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","prompt":"fixture",` + fields + `}`))
		require.Error(t, err, fields)
	}
}

func TestCompShareH3FreeReferencesAndModeration(t *testing.T) {
	body := []byte(`{"model":"MiniMax-H3","content":[{"type":"text","text":"reference fixture"},{"type":"image_url","image_url":{"url":"https://no-such-host.invalid/i.png"},"role":"reference_image"},{"type":"audio_url","audio_url":{"url":"https://no-such-host.invalid/a.wav"},"role":"reference_audio"}]}`)
	normalized, err := NormalizeCompShareH3Request(body)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(normalized, "content").Array(), 3)
	info := ParseGrokMediaRequest("application/json", body)
	require.Equal(t, "reference fixture", info.Prompt)
	require.Len(t, info.InputImageURLs, 1)
	require.Contains(t, string(info.ModerationBody()), "no-such-host.invalid")
	for _, asset := range []string{`{"type":"video_url","video_url":{"url":"https://no-such-host.invalid/v.mp4"}}`, `{"type":"image_url","image_url":{"url":"https:///missing-host"}}`, `{"type":"audio_url","audio_url":{"url":"https://user@no-such-host.invalid/a.wav"},"role":"reference_audio"}`, `{"type":"image_url","image_url":{"url":"https://no-such-host.invalid:8443/i.png"}}`} {
		_, err := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","content":[{"type":"text","text":"f"},` + asset + `]}`))
		require.Error(t, err, asset)
	}
}

func TestCompShareH3ImageReferenceInputs(t *testing.T) {
	const imageURL = "https://no-such-host.invalid/frame.png"
	const dataURL = "data:image/png;base64,aW1hZ2U="
	for _, tc := range []struct {
		name, fields, role, url string
	}{
		{"image_https", `"image":"` + imageURL + `"`, "first_frame", imageURL},
		{"image_data", `"image":"` + dataURL + `"`, "first_frame", dataURL},
		{"image_url_object", `"image":{"url":"` + imageURL + `"}`, "first_frame", imageURL},
		{"image_legacy_object", `"image":{"image_url":"` + imageURL + `"}`, "first_frame", imageURL},
		{"reference_https", `"reference_images":["` + imageURL + `"]`, "reference_image", imageURL},
		{"reference_data", `"reference_images":["` + dataURL + `"]`, "reference_image", dataURL},
		{"reference_url_object", `"reference_images":[{"url":"` + imageURL + `"}]`, "reference_image", imageURL},
		{"reference_legacy_object", `"reference_images":[{"image_url":"` + imageURL + `"}]`, "reference_image", imageURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","prompt":"animate","resolution":"768p","duration":5,"aspect_ratio":"16:9","video_generation_mode":"first_frame_image_to_video",` + tc.fields + `}`))
			require.NoError(t, err)
			require.Len(t, gjson.GetBytes(body, "content").Array(), 2)
			require.Equal(t, "animate", gjson.GetBytes(body, "content.0.text").String())
			require.Equal(t, "image_url", gjson.GetBytes(body, "content.1.type").String())
			require.Equal(t, tc.url, gjson.GetBytes(body, "content.1.image_url.url").String())
			require.Equal(t, tc.role, gjson.GetBytes(body, "content.1.role").String())

			native, err := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","content":[{"type":"text","text":"animate"},{"type":"image_url","image_url":{"url":"` + tc.url + `"},"role":"` + tc.role + `"}]}`))
			require.NoError(t, err)
			require.JSONEq(t, string(native), string(body))
		})
	}
}

func TestCompShareH3RejectsUnsafeImageReferences(t *testing.T) {
	for _, imageURL := range []string{
		"http://no-such-host.invalid/frame.png", "file:///frame.png", "https:///missing-host",
		"https://user@no-such-host.invalid/frame.png", "https://no-such-host.invalid:8443/frame.png",
	} {
		for _, field := range []string{`"image":"` + imageURL + `"`, `"reference_images":["` + imageURL + `"]`} {
			_, err := NormalizeCompShareH3Request([]byte(`{"model":"MiniMax-H3","prompt":"animate",` + field + `}`))
			require.EqualError(t, err, "invalid H3 media URL", field)
		}
	}
}

func TestCompShareH3SettlementUsesFrozenPriceAndActualDelivery(t *testing.T) {
	snap := compShareH3TestSnapshot()
	amount, err := CompShareH3SettlementAmount(snap, "succeeded", "768P", 4)
	require.NoError(t, err)
	require.Equal(t, 16.0, amount)
	for _, status := range []string{"failed", "cancelled", "blocked", "submission_rejected"} {
		amount, err := CompShareH3SettlementAmount(snap, status, "", 0)
		require.NoError(t, err)
		require.Zero(t, amount)
	}
	for _, seconds := range []float64{0, -1, 6, math.NaN(), math.Inf(1)} {
		_, err := CompShareH3SettlementAmount(snap, "succeeded", "768P", seconds)
		require.Error(t, err)
	}
	for _, status := range []string{"unknown", "queued", "running"} {
		_, err := CompShareH3SettlementAmount(snap, status, "768P", 5)
		require.Error(t, err)
	}
	for _, res := range []string{"720P", "2K", "480P", ""} {
		_, err := CompShareH3SettlementAmount(snap, "succeeded", res, 5)
		require.Error(t, err)
	}
	snap.Resolution = "768p"
	_, err = CompShareH3SettlementAmount(snap, "succeeded", "4K", 5)
	require.Error(t, err)
}

func TestCompShareH3ProtocolAndDurableUnknownReceipt(t *testing.T) {
	snap := compShareH3TestSnapshot()
	repo := &compShareH3RepoStub{snap: snap}
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"task_id":"provider-fixture"}`))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, usageBillingRepo: repo}
	c, w := grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
	c.Set(CompShareH3ContextKey, snap)
	result, err := svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideosGenerations, "", nil, "application/json")
	require.NoError(t, err)
	require.Equal(t, compShareH3BaseURL+"/minimax/v2/video_generation", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer fixture-only", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, snap.RequestID, upstream.lastReq.Header.Get("Idempotency-Key"))
	require.Equal(t, "4K", gjson.GetBytes(upstream.lastBody, "resolution").String())
	require.Equal(t, "provider-fixture", result.ResponseID)
	require.Equal(t, 1, repo.bindCalls)
	require.Equal(t, "provider-fixture", gjson.Get(w.Body.String(), "request_id").String())
	upstream.resp = &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`malformed`))}
	c, w = grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
	c.Set(CompShareH3ContextKey, snap)
	_, err = svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideosGenerations, "", nil, "application/json")
	require.Error(t, err)
	require.Equal(t, 502, w.Code)
	require.Equal(t, snap.RequestID, gjson.Get(w.Body.String(), "id").String())
	require.Equal(t, "h3_submission_unknown", gjson.Get(w.Body.String(), "error.code").String())
	for _, code := range []int{302, 500, 502, 504} {
		upstream.resp = &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"https://private.invalid/?token=fixture"}`))}
		c, w = grokMediaContentTestContext(http.MethodPost, "/v1/videos/generations", nil)
		c.Set(CompShareH3ContextKey, snap)
		_, err = svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideosGenerations, "", nil, "application/json")
		require.Error(t, err)
		require.Equal(t, 502, w.Code)
		require.Equal(t, "h3_submission_unknown", gjson.Get(w.Body.String(), "error.code").String())
		require.NotContains(t, w.Body.String(), "private.invalid")
		require.Zero(t, repo.settleCalls)
	}
	snap.TaskID = ""
	upstream.requests = nil
	c, w = grokMediaContentTestContext(http.MethodGet, "/v1/videos/"+snap.RequestID, nil)
	c.Set(CompShareH3ContextKey, snap)
	_, err = svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideoStatus, snap.RequestID, nil, "")
	require.NoError(t, err)
	require.Empty(t, upstream.requests)
	require.Equal(t, "unknown", gjson.Get(w.Body.String(), "status").String())
	snap.Settled = true
	c, w = grokMediaContentTestContext(http.MethodGet, "/v1/videos/"+snap.RequestID, nil)
	c.Set(CompShareH3ContextKey, snap)
	_, err = svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideoStatus, snap.RequestID, nil, "")
	require.NoError(t, err)
	require.Equal(t, "failed", gjson.Get(w.Body.String(), "status").String())
}

func TestCompShareH3UnifiedBillingUsesFrozenReceiptAndBalance(t *testing.T) {
	snap := compShareH3TestSnapshot()
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	groupID := int64(4)
	key := &APIKey{ID: 7, UserID: 1, Quota: 1000, GroupID: &groupID, Group: &Group{ID: 4, RateMultiplier: 999, SubscriptionType: "subscription"}}
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{CompShareH3: snap, Model: CompShareH3Model, UpstreamModel: CompShareH3Model, VideoCount: 1, VideoResolution: "4k", VideoDurationSeconds: 5}, APIKey: key, User: &User{ID: 1}, Account: compShareH3TestAccount(), Subscription: &UserSubscription{ID: 9}, APIKeyService: &openAIRecordUsageAPIKeyQuotaStub{}})
	require.NoError(t, err)
	require.Equal(t, snap.RequestID, billingRepo.lastCmd.RequestID)
	require.Equal(t, snap.Credits, billingRepo.lastCmd.BalanceCost)
	require.Zero(t, billingRepo.lastCmd.SubscriptionCost)
	require.Same(t, snap, billingRepo.lastCmd.CompShareH3)
	require.Equal(t, snap.Credits, usageRepo.lastLog.ActualCost)
	require.Equal(t, snap.BaseCost, usageRepo.lastLog.TotalCost)
	require.Equal(t, snap.Multiplier, usageRepo.lastLog.RateMultiplier)
	require.Nil(t, usageRepo.lastLog.SubscriptionID)
}

func TestCompShareH3SchedulerNeverSelectsMatchingReseller(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	group := int64(10119)
	official := *compShareH3TestAccount()
	official.GroupIDs = []int64{group}
	reseller := *compShareH3TestAccount()
	reseller.ID = 99
	reseller.GroupIDs = []int64{group}
	reseller.Credentials = map[string]any{"base_url": "https://toapis.cn/v1", "model_mapping": map[string]any{CompShareH3Model: CompShareH3Model}}
	for _, accounts := range [][]Account{{reseller, official}, {reseller}} {
		svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: &schedulerTestGatewayCache{}, cfg: &config.Config{}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
		selected, _, err := svc.SelectAccountWithSchedulerForCapability(context.Background(), &group, "", "", CompShareH3Model, nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityCompShareH3, false, false, false, PlatformGrok)
		if len(accounts) == 2 {
			require.NoError(t, err)
			require.Equal(t, official.ID, selected.Account.ID)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
		} else {
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selected)
		}
	}
}

func TestCompShareH3ExactRouteAndDownloadSecurity(t *testing.T) {
	a := compShareH3TestAccount()
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityCompShareH3))
	for _, base := range []string{"https://toapis.cn/v1", "http://cp.compshare.cn", "https://cp.compshare.cn.evil.invalid", "https://user@cp.compshare.cn", "https://cp.compshare.cn:8443", "https://cp.compshare.cn/v1", "https://cp.compshare.cn?x=1"} {
		a.Credentials["base_url"] = base
		require.False(t, a.IsCompShareH3Account(), base)
	}
	a = compShareH3TestAccount()
	a.Credentials["model_mapping"] = map[string]any{"other-model": "other-model"}
	require.False(t, a.IsModelSupported(CompShareH3Model))
	for _, raw := range []string{
		"https://127.0.0.1/v.mp4",
		"https://compshare-files.cn-wlcb.ufileos.com.evil.invalid/v.mp4",
		"https://other.ufileos.com/v.mp4",
		"https://user@compshare-files.cn-wlcb.ufileos.com/v.mp4",
		"https://compshare-files.cn-wlcb.ufileos.com:8443/v.mp4",
		"http://cdn-files.compshare.cn/v.mp4",
		"https://cdn-files.compshare.cn.evil.invalid/v.mp4",
		"https://other.compshare.cn/v.mp4",
		"https://user@cdn-files.compshare.cn/v.mp4",
		"https://cdn-files.compshare.cn:8443/v.mp4",
	} {
		body, _ := json.Marshal(map[string]any{"task": map[string]any{"status": "succeeded", "content": map[string]string{"url": raw}}})
		_, err := compShareH3ContentURL(body)
		require.Error(t, err)
	}
	for _, host := range []string{"compshare-files.cn-wlcb.ufileos.com", "cdn-files.compshare.cn"} {
		for _, port := range []string{"", ":443"} {
			raw := "https://" + host + port + "/v.mp4?signature=fixture"
			body, _ := json.Marshal(map[string]any{"task": map[string]any{"status": "succeeded", "content": map[string]string{"url": raw}}})
			got, err := compShareH3ContentURL(body)
			require.NoError(t, err)
			require.Equal(t, raw, got)
		}
	}
	snap := compShareH3TestSnapshot()
	snap.TaskID = "provider-fixture"
	repo := &compShareH3RepoStub{snap: snap}
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{grokMediaContentStatusResponse(`{"task":{"id":"provider-fixture","status":"succeeded","resolution":"768P","usage":{"output_seconds":5},"content":{"url":"https://compshare-files.cn-wlcb.ufileos.com/v.mp4?signature=fixture"}}}`), {StatusCode: 302, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader(""))}}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, usageBillingRepo: repo}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/provider-fixture/content", nil)
	c.Set(CompShareH3ContextKey, snap)
	_, err := svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideoContent, "provider-fixture", nil, "")
	require.Error(t, err)
	require.NotContains(t, w.Body.String(), "signature=")
	require.Empty(t, upstream.request.Header.Get("Authorization"))
}

func TestCompShareH3CDNContentStreamsWithoutProviderCredential(t *testing.T) {
	snap := compShareH3TestSnapshot()
	snap.TaskID = "provider-fixture"
	repo := &compShareH3RepoStub{snap: snap}
	contentURL := "https://cdn-files.compshare.cn/v.mp4?signature=fixture"
	upstream := &grokMediaContentUpstreamStub{responses: []*http.Response{
		grokMediaContentStatusResponse(`{"task":{"id":"provider-fixture","status":"succeeded","resolution":"768P","usage":{"output_seconds":4},"content":{"url":"` + contentURL + `"}}}`),
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"video/mp4"}}, Body: io.NopCloser(strings.NewReader("video-payload"))},
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, usageBillingRepo: repo}
	c, w := grokMediaContentTestContext(http.MethodGet, "/v1/videos/provider-fixture/content", nil)
	c.Set(CompShareH3ContextKey, snap)
	_, err := svc.ForwardGrokMedia(context.Background(), c, compShareH3TestAccount(), GrokMediaEndpointVideoContent, snap.TaskID, nil, "")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "video-payload", w.Body.String())
	require.Len(t, upstream.requests, 2)
	require.Equal(t, contentURL, upstream.requests[1].URL.String())
	require.Empty(t, upstream.requests[1].Header.Get("Authorization"))
	require.Equal(t, 1, repo.settleCalls)
}

func TestCompShareH3DurableOwnerBindingWithoutCache(t *testing.T) {
	snap := compShareH3TestSnapshot()
	snap.TaskID = "provider-fixture"
	svc := &OpenAIGatewayService{usageBillingRepo: &compShareH3RepoStub{snap: snap}}
	group := int64(4)
	id, err := svc.ResolveGrokMediaVideoRequestAccount(context.Background(), &group, snap.TaskID, snap.UserID, snap.APIKeyID)
	require.NoError(t, err)
	require.Equal(t, snap.AccountID, id)
	_, err = svc.ResolveGrokMediaVideoRequestAccount(context.Background(), &group, snap.TaskID, 999, snap.APIKeyID)
	require.Error(t, err)
	_, err = svc.ResolveGrokMediaVideoRequestAccount(context.Background(), &group, snap.TaskID, snap.UserID, 999)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrInsufficientBalance))
}
