//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

const testOrgOverrides = `{"doubao-seedream-5-0-pro": {"model": "v-doubao-seedream-5-0-pro", "sizes": {"1.5K": "1536x1536"}}}`

func TestParseOrganizationModelOverrides(t *testing.T) {
	got := parseOrganizationModelOverrides(testOrgOverrides)
	require.Equal(t, "v-doubao-seedream-5-0-pro", got["doubao-seedream-5-0-pro"].Model)

	// 写坏的配置等于没配：宁可照常走 C 端的路，也不能把组织请求改成不存在的模型。
	for _, raw := range []string{"", "   ", "not json", `["a"]`} {
		require.Empty(t, parseOrganizationModelOverrides(raw), raw)
	}

	// 目标为空、或者改成自己的条目直接丢掉；两侧空白要去掉。
	got = parseOrganizationModelOverrides(`{"a": {"model": ""}, "b": {"model": "b"}, " c ": {"model": " d "}}`)
	require.Len(t, got, 1)
	require.Equal(t, "d", got["c"].Model)
}

func TestOrganizationModelOverrideSizeFor(t *testing.T) {
	override := parseOrganizationModelOverrides(testOrgOverrides)["doubao-seedream-5-0-pro"]
	for _, size := range []string{"1.5K", "1.5k", " 1.5K "} {
		got, ok := override.SizeFor(size)
		require.True(t, ok, size)
		require.Equal(t, "1536x1536", got)
	}
	// 表里没列的尺寸原样放行。
	for _, size := range []string{"", "2K", "1536x1536"} {
		_, ok := override.SizeFor(size)
		require.False(t, ok, size)
	}
}

// flakySettingRepo 在 err 非空时让 GetValue 失败，用来模拟读库出错。
type flakySettingRepo struct {
	*stubSettingRepo
	err error
}

func (r *flakySettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.stubSettingRepo.GetValue(ctx, key)
}

func newOverrideSettingService(t *testing.T, raw string) (*SettingService, *flakySettingRepo) {
	t.Helper()
	repo := &flakySettingRepo{stubSettingRepo: newStubSettingRepo()}
	require.NoError(t, repo.Set(context.Background(), SettingKeyOrganizationModelOverrides, raw))
	return &SettingService{settingRepo: repo}, repo
}

func expireOrganizationModelOverrides(s *SettingService) {
	if cached, ok := s.organizationModelOverridesCache.Load().(*cachedOrganizationModelOverrides); ok && cached != nil {
		s.organizationModelOverridesCache.Store(&cachedOrganizationModelOverrides{value: cached.value})
	}
}

func TestSettingServiceOrganizationModelOverride(t *testing.T) {
	ctx := context.Background()
	svc, repo := newOverrideSettingService(t, testOrgOverrides)

	override, ok := svc.OrganizationModelOverride(ctx, "doubao-seedream-5-0-pro")
	require.True(t, ok)
	require.Equal(t, "v-doubao-seedream-5-0-pro", override.Model)

	_, ok = svc.OrganizationModelOverride(ctx, "deepseek-v4-flash")
	require.False(t, ok, "表里没有的模型不改道")

	// 缓存期内改库不回源：这是热路径。
	require.NoError(t, repo.Set(ctx, SettingKeyOrganizationModelOverrides, ""))
	_, ok = svc.OrganizationModelOverride(ctx, "doubao-seedream-5-0-pro")
	require.True(t, ok)

	// 过期后读到删掉的配置 = 回滚生效。
	expireOrganizationModelOverrides(svc)
	_, ok = svc.OrganizationModelOverride(ctx, "doubao-seedream-5-0-pro")
	require.False(t, ok)
}

// 读库失败不能让组织请求在这一分钟里悄悄改回 C 端的上游：沿用上一份。
func TestSettingServiceOrganizationModelOverrideKeepsLastGoodOnError(t *testing.T) {
	ctx := context.Background()
	svc, repo := newOverrideSettingService(t, testOrgOverrides)
	_, ok := svc.OrganizationModelOverride(ctx, "doubao-seedream-5-0-pro")
	require.True(t, ok)

	expireOrganizationModelOverrides(svc)
	repo.err = errors.New("db down")
	_, ok = svc.OrganizationModelOverride(ctx, "doubao-seedream-5-0-pro")
	require.True(t, ok)
}

// 设置不存在 = 没有改道，不当成错误。
func TestSettingServiceOrganizationModelOverrideNotConfigured(t *testing.T) {
	repo := &flakySettingRepo{stubSettingRepo: newStubSettingRepo(), err: ErrSettingNotFound}
	svc := &SettingService{settingRepo: repo}
	_, ok := svc.OrganizationModelOverride(context.Background(), "doubao-seedream-5-0-pro")
	require.False(t, ok)
}

// 路由注册时传进来的 *SettingService 可能是 nil，装进接口后照样会被调用。
func TestNilSettingServiceHasNoOrganizationOverride(t *testing.T) {
	var svc *SettingService
	_, ok := svc.OrganizationModelOverride(context.Background(), "doubao-seedream-5-0-pro")
	require.False(t, ok)
}

// 组织身份必须能穿过认证缓存：快照丢了它，走缓存的请求就认不出组织，改道静默失效。
// 这和视频按模型定价那次是同一个坑（快照漏字段，计费全按分组价）。
func TestAPIKeyAuthSnapshotCarriesOrganizationID(t *testing.T) {
	svc := &APIKeyService{}
	apiKey := &APIKey{
		ID: 1, Key: "k", Status: StatusActive,
		User: &User{ID: 9, Status: StatusActive, Role: RoleUser, OrganizationID: "org-college"},
	}
	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	require.NotNil(t, snapshot)
	require.Equal(t, "org-college", snapshot.User.OrganizationID)

	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	var decoded APIKeyAuthSnapshot
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "org-college", svc.snapshotToAPIKey("k", &decoded).User.OrganizationID)
}
