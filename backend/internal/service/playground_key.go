package service

import (
	"context"
	"strconv"
	"strings"
)

// PlaygroundKeyName 网页端「在线使用」自动建的密钥名。
//
// 和侧边栏的页面名一致，用户在密钥列表里一眼能认出它是从哪来的。
const PlaygroundKeyName = "在线使用"

// SettingPlaygroundKeyGroupID 「在线使用」自动建钥匙时绑定的分组。
//
// 键名还是 signup_trial_key_group_id，是历史原因：它原本是注册送「试用密钥」
// 的分组配置，线上配的正是 C 端分组。注册送钥匙那套已经删了，但网页端的钥匙
// 仍然要落在同一个分组里——分组决定了模型列表和价格，换了分组网页端就变了样。
// 改键名得同时改线上数据，不值当。
const SettingPlaygroundKeyGroupID = "signup_trial_key_group_id"

// PlaygroundKeyGroupID 读「在线使用」钥匙的分组；未配置或配错都返回 nil。
func (s *SettingService) PlaygroundKeyGroupID(ctx context.Context) *int64 {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingPlaygroundKeyGroupID)
	if err != nil {
		return nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}
