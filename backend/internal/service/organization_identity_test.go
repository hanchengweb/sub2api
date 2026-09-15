//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOrganizationServiceIdentity(t *testing.T) {
	input := CreateUserInput{
		Email: "service@example.test", AccountType: AccountTypeOrganizationService,
		OrganizationIssuer: "wemoreai-ops", OrganizationID: "organization-a",
		OrganizationEnvironment: "test",
	}
	repo := &userRepoStub{nextID: 100}
	assigner := &defaultSubscriptionAssignerStub{}
	settings := NewSettingService(&settingRepoStub{values: map[string]string{
		SettingKeyDefaultSubscriptions: `[{"group_id":5,"validity_days":30}]`,
	}}, &config.Config{Default: config.DefaultConfig{UserBalance: 100}})
	svc := &adminServiceImpl{userRepo: repo, settingService: settings, defaultSubAssigner: assigner}
	u, err := svc.CreateUser(context.Background(), &input)
	require.NoError(t, err)
	require.Equal(t, AccountTypeOrganizationService, u.AccountType)
	require.Equal(t, "organization-a", u.OrganizationID)
	require.Equal(t, RoleUser, u.Role)
	require.Zero(t, u.Balance)
	require.Empty(t, assigner.calls)
	require.True(t, u.IsActive(), "service keys must remain eligible for gateway auth")
	require.False(t, u.CanLogin())
	require.NotEmpty(t, u.PasswordHash)
	require.NoError(t, u.SetPassword("known-password"))
	require.False(t, u.CheckPassword("known-password"))
	auth := &AuthService{}
	token, err := auth.GenerateToken(context.Background(), u)
	require.ErrorIs(t, err, ErrServiceAccountLogin)
	require.Empty(t, token)
	token, err = auth.generateAccessToken(u, "oauth-or-refresh", "")
	require.ErrorIs(t, err, ErrServiceAccountLogin)
	require.Empty(t, token)
	for _, change := range []func(*CreateUserInput){
		func(v *CreateUserInput) { v.OrganizationID = " " },
		func(v *CreateUserInput) { v.OrganizationIssuer = "" },
		func(v *CreateUserInput) { v.OrganizationEnvironment = "unknown" },
		func(v *CreateUserInput) { v.Role = RoleAdmin },
		func(v *CreateUserInput) { v.Password = "password" },
		func(v *CreateUserInput) { v.AccountType = AccountTypePersonal },
	} {
		invalid := input
		change(&invalid)
		_, err := validateOrganizationIdentity(&invalid)
		require.ErrorIs(t, err, ErrServiceAccountIdentity)
	}
	loaded := &adminServiceImpl{userRepo: &userRepoStub{user: u}}
	for _, update := range []UpdateUserInput{{Role: RoleAdmin}, {Password: "reset-password"}} {
		_, err := loaded.UpdateUser(context.Background(), u.ID, &update)
		require.ErrorIs(t, err, ErrServiceAccountIdentity)
	}
	require.True(t, (&User{}).CanLogin(), "legacy in-memory users remain personal")
	require.False(t, (&User{AccountType: "unknown"}).CanLogin())
}
