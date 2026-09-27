//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlaygroundKeyGroupID(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want int64 // 0 表示期望返回 nil
	}{
		{"线上配置", "4", 4},
		{"两侧空白", " 4 ", 4},
		{"未配置", "", 0},
		{"非数字", "abc", 0},
		{"零", "0", 0},
		{"负数", "-3", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newStubSettingRepo()
			if tc.raw != "" {
				require.NoError(t, repo.Set(context.Background(), SettingPlaygroundKeyGroupID, tc.raw))
			}
			svc := &SettingService{settingRepo: repo}
			got := svc.PlaygroundKeyGroupID(context.Background())
			if tc.want == 0 {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tc.want, *got)
		})
	}
}

// 分组配置沿用线上已有的键，改键名会让网页端的钥匙悄悄换组。
func TestPlaygroundKeyGroupSettingKeepsLegacyKey(t *testing.T) {
	require.Equal(t, "signup_trial_key_group_id", SettingPlaygroundKeyGroupID)
}

// 保留名判断：撞了名，用户自己的钥匙会从列表里消失，所以新建和改名都要拒绝。
func TestIsReservedAPIKeyName(t *testing.T) {
	for _, name := range []string{"在线使用", " 在线使用 "} {
		require.True(t, IsReservedAPIKeyName(name), name)
	}
	for _, name := range []string{"", "在线使用1", "我的在线使用", "风合智联桌面端", "默认密钥"} {
		require.False(t, IsReservedAPIKeyName(name), name)
	}
}
