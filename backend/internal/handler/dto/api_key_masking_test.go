package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAdminDTOsMaskOrganizationServiceKeys(t *testing.T) {
	const secret = "orgkey0123456789abcdefghijklWXYZ"
	user := func(accountType string) *service.User {
		return &service.User{ID: 7, AccountType: accountType,
			APIKeys: []service.APIKey{{ID: 1, UserID: 7, Key: secret}}}
	}

	// 管理接口返回的用户对象内嵌 key。
	require.Equal(t, "orgkey...WXYZ", UserFromServiceAdmin(user(service.AccountTypeOrganizationService)).APIKeys[0].Key)
	require.Equal(t, secret, UserFromServiceAdmin(user(service.AccountTypePersonal)).APIKeys[0].Key)

	log := func(owner *service.User) *service.UsageLog {
		return &service.UsageLog{UserID: 7, User: owner, APIKey: &service.APIKey{ID: 1, UserID: 7, Key: secret}}
	}
	for name, tc := range map[string]struct {
		owner *service.User
		want  string
	}{
		"organization":     {user(service.AccountTypeOrganizationService), "orgkey...WXYZ"},
		"personal":         {user(service.AccountTypePersonal), secret},
		"unknown owner":    {nil, "orgkey...WXYZ"},
		"unloaded type":    {user(""), "orgkey...WXYZ"},
		"owner of another": {&service.User{ID: 8, AccountType: service.AccountTypePersonal}, "orgkey...WXYZ"},
	} {
		source := log(tc.owner)
		require.Equal(t, tc.want, UsageLogFromServiceAdmin(source).APIKey.Key, name)
		require.Equal(t, secret, source.APIKey.Key, "%s: source model must stay unchanged", name)
	}

	// 用户自己的接口不变。
	require.Equal(t, secret, UsageLogFromService(log(user(service.AccountTypePersonal))).APIKey.Key)
	require.Equal(t, secret, APIKeyFromService(&service.APIKey{ID: 1, UserID: 7, Key: secret}).Key)
}

func TestMaskedAPIKeyMatchesFrontendRule(t *testing.T) {
	// 与 frontend/src/utils/maskApiKey.ts 同一规则，前端再脱敏一次结果不变；4 位以内整体隐藏。
	for key, want := range map[string]string{
		"":              "",
		"abcd":          "***",
		"abcdefgh":      "abcd***",
		"abcdefghijkl":  "abcd***",
		"abcdefghijklm": "abcdef...jklm",
		"abcdef...jklm": "abcdef...jklm",
		"abcd***":       "abcd***",
	} {
		owner := &service.User{ID: 7, AccountType: service.AccountTypeOrganizationService,
			APIKeys: []service.APIKey{{ID: 1, UserID: 7, Key: key}}}
		require.Equal(t, want, UserFromServiceAdmin(owner).APIKeys[0].Key, "key %q", key)
	}
}
