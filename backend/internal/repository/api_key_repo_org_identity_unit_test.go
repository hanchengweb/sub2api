package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 网关靠 organization_id 把 B 端组织的请求改道到组织专用上游。
// 认证查询漏选这一列的话，所有请求都认不出组织身份，改道静默失效——
// 和漏选 video_model_prices 导致视频全按分组价扣费是同一类坑。
func TestAPIKeyRepository_GetByKeyForAuth_LoadsOrganizationID_SQLite(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	u, err := client.User.Create().
		SetEmail("org-auth-test@example.test").
		SetPasswordHash("test-password-hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetAccountType(service.AccountTypeOrganizationService).
		SetOrganizationIssuer("wemoreai-ops").
		SetOrganizationID("org-auth-test").
		SetOrganizationEnvironment("test").
		Save(ctx)
	require.NoError(t, err)
	key := &service.APIKey{UserID: u.ID, Key: "test-org-auth-key", Name: "org-auth-test", Status: service.StatusActive}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.User)
	require.Equal(t, "org-auth-test", got.User.OrganizationID)
}

// 「在线使用」是系统替用户建的内部钥匙：用户的密钥列表看不到它，
// 在线使用页取钥匙时（不带过滤）必须还看得到，否则每次都会再建一把。
func TestAPIKeyRepository_ListByUserID_ExcludesInternalKey_SQLite(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "internal-key-test@example.test")
	for _, k := range []*service.APIKey{
		{UserID: user.ID, Key: "test-internal-playground-key", Name: service.PlaygroundKeyName, Status: service.StatusActive},
		{UserID: user.ID, Key: "test-internal-own-key", Name: "我的钥匙", Status: service.StatusActive},
	} {
		require.NoError(t, repo.Create(ctx, k))
	}
	params := pagination.PaginationParams{Page: 1, PageSize: 20}

	visible, page, err := repo.ListByUserID(ctx, user.ID, params, service.APIKeyListFilters{ExcludeInternal: true})
	require.NoError(t, err)
	require.Len(t, visible, 1)
	require.Equal(t, "我的钥匙", visible[0].Name)
	require.EqualValues(t, 1, page.Total, "分页总数也要一起排除，不能列表 1 条、总数显示 2")

	all, _, err := repo.ListByUserID(ctx, user.ID, params, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Len(t, all, 2)
}
