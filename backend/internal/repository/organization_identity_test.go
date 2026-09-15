package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOrganizationIdentityPersistence(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := context.Background()
	create := func(email, org, env string) error {
		return repo.Create(ctx, &service.User{
			Email: email, PasswordHash: "test-hash", Role: service.RoleUser,
			Status: service.StatusActive, AccountType: service.AccountTypeOrganizationService,
			OrganizationIssuer: "wemoreai-ops", OrganizationID: org, OrganizationEnvironment: env,
		})
	}
	require.NoError(t, create("a@example.test", "org-a", "test"))
	u, err := repo.GetByEmail(ctx, "a@example.test")
	require.NoError(t, err)
	require.Equal(t, "org-a", u.OrganizationID)
	require.Equal(t, "wemoreai-ops", u.OrganizationIssuer)
	require.Equal(t, "test", u.OrganizationEnvironment)
	require.False(t, u.CanLogin())
	require.Error(t, create("duplicate@example.test", "org-a", "test"))
	require.NoError(t, create("b@example.test", "org-b", "test"))
	require.NoError(t, create("production@example.test", "org-a", "production"))
	// Database constraints also reject malformed service identities and promotion.
	require.Error(t, create("invalid@example.test", "", "test"))
	err = client.User.UpdateOneID(u.ID).SetRole(service.RoleAdmin).Exec(ctx)
	require.Error(t, err)
	err = client.User.UpdateOneID(u.ID).SetDeletedAt(time.Now()).Exec(ctx)
	require.NoError(t, err)
	require.Error(t, create("reassigned@example.test", "org-a", "test"))
	// Existing personal identities retain the shared empty binding defaults.
	for _, email := range []string{"human-a@example.test", "human-b@example.test"} {
		err := repo.Create(ctx, &service.User{Email: email, PasswordHash: "hash", Role: service.RoleUser, Status: service.StatusActive})
		require.NoError(t, err)
	}
}
