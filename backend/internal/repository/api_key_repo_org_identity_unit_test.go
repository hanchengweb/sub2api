package repository

import (
	"context"
	"testing"

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
