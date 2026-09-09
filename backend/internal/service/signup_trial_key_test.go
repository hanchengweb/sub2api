//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type fakeTrialKeyIssuer struct {
	existing   []APIKey
	listErr    error
	createErr  error
	createdReq *CreateAPIKeyRequest
	createCall int
}

func (f *fakeTrialKeyIssuer) Create(_ context.Context, _ int64, req CreateAPIKeyRequest) (*APIKey, error) {
	f.createCall++
	f.createdReq = &req
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &APIKey{ID: 42, Quota: req.Quota}, nil
}

func (f *fakeTrialKeyIssuer) List(_ context.Context, _ int64, _ pagination.PaginationParams, _ APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	return f.existing, nil, f.listErr
}

// 用 map 直接喂设置，绕开真实仓储。
type trialSettingRepo struct {
	SettingRepository
	data map[string]string
}

func (r *trialSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		out[k] = r.data[k]
	}
	return out, nil
}

func newTrialAuthService(data map[string]string, issuer SignupTrialKeyIssuer) *AuthService {
	return &AuthService{
		settingService: &SettingService{settingRepo: &trialSettingRepo{data: data}},
		trialKeyIssuer: issuer,
	}
}

// 额度上限必须收敛：赠送额度直接对应真金白银的上游成本，配置写错一个零就是十倍支出。
func TestIssueSignupTrialKey_ClampsToMaxQuota(t *testing.T) {
	issuer := &fakeTrialKeyIssuer{}
	svc := newTrialAuthService(map[string]string{
		SettingSignupTrialKeyEnabled: "true",
		SettingSignupTrialKeyQuota:   "999999",
	}, issuer)

	svc.issueSignupTrialKey(context.Background(), 7)

	require.Equal(t, 1, issuer.createCall)
	require.NotNil(t, issuer.createdReq)
	require.InDelta(t, float64(MaxSignupTrialKeyQuota), issuer.createdReq.Quota, 1e-9,
		"超过上限必须收敛到 %d，不能照配置发", MaxSignupTrialKeyQuota)
}

// 未配置额度时走默认 1000。
func TestIssueSignupTrialKey_DefaultQuota(t *testing.T) {
	issuer := &fakeTrialKeyIssuer{}
	svc := newTrialAuthService(map[string]string{SettingSignupTrialKeyEnabled: "true"}, issuer)

	svc.issueSignupTrialKey(context.Background(), 7)
	require.InDelta(t, float64(DefaultSignupTrialKeyQuota), issuer.createdReq.Quota, 1e-9)
}

// 幂等：已有密钥就不再发。postAuthUserBootstrap 在 OAuth 登录路径上也会走到，
// 老用户每次登录都送一份试用额度就是白送钱。
func TestIssueSignupTrialKey_SkipsWhenUserAlreadyHasKey(t *testing.T) {
	issuer := &fakeTrialKeyIssuer{existing: []APIKey{{ID: 1}}}
	svc := newTrialAuthService(map[string]string{SettingSignupTrialKeyEnabled: "true"}, issuer)

	svc.issueSignupTrialKey(context.Background(), 7)
	require.Zero(t, issuer.createCall, "已有密钥的用户不该再拿试用额度")
}

// 开关关闭时不发。
func TestIssueSignupTrialKey_DisabledByDefault(t *testing.T) {
	issuer := &fakeTrialKeyIssuer{}
	for _, data := range []map[string]string{
		{},
		{SettingSignupTrialKeyEnabled: "false"},
		{SettingSignupTrialKeyEnabled: ""},
	} {
		svc := newTrialAuthService(data, issuer)
		svc.issueSignupTrialKey(context.Background(), 7)
	}
	require.Zero(t, issuer.createCall, "开关未显式打开时不得发放")
}

// 发放失败不能阻断注册：这个函数没有返回值，出错只记日志，调用方继续。
func TestIssueSignupTrialKey_FailuresAreNonFatal(t *testing.T) {
	data := map[string]string{SettingSignupTrialKeyEnabled: "true"}
	require.NotPanics(t, func() {
		svc := newTrialAuthService(data, &fakeTrialKeyIssuer{createErr: errors.New("boom")})
		svc.issueSignupTrialKey(context.Background(), 7)
	}, "创建失败不得 panic")
	require.NotPanics(t, func() {
		svc := newTrialAuthService(data, &fakeTrialKeyIssuer{listErr: errors.New("boom")})
		svc.issueSignupTrialKey(context.Background(), 7)
	}, "查询失败不得 panic")
	require.NotPanics(t, func() {
		newTrialAuthService(data, nil).issueSignupTrialKey(context.Background(), 7)
	}, "未注入 issuer 时应静默跳过")
}

// 查询已有密钥失败时不能继续发放：状态未知就发，可能重复送。
func TestIssueSignupTrialKey_ListErrorDoesNotIssue(t *testing.T) {
	issuer := &fakeTrialKeyIssuer{listErr: errors.New("db down")}
	svc := newTrialAuthService(map[string]string{SettingSignupTrialKeyEnabled: "true"}, issuer)

	svc.issueSignupTrialKey(context.Background(), 7)
	require.Zero(t, issuer.createCall, "查不到既有密钥时不能盲发")
}

// 配置了分组就带上，没配则为空（由 APIKeyService 走默认分组逻辑）。
func TestIssueSignupTrialKey_PassesGroupID(t *testing.T) {
	issuer := &fakeTrialKeyIssuer{}
	svc := newTrialAuthService(map[string]string{
		SettingSignupTrialKeyEnabled: "true",
		SettingSignupTrialKeyGroupID: "4",
	}, issuer)
	svc.issueSignupTrialKey(context.Background(), 7)
	require.NotNil(t, issuer.createdReq.GroupID)
	require.Equal(t, int64(4), *issuer.createdReq.GroupID)

	issuer2 := &fakeTrialKeyIssuer{}
	svc2 := newTrialAuthService(map[string]string{SettingSignupTrialKeyEnabled: "true"}, issuer2)
	svc2.issueSignupTrialKey(context.Background(), 7)
	require.Nil(t, issuer2.createdReq.GroupID)
}
