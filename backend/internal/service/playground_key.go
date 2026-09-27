package service

import (
	"context"
	"strconv"
	"strings"
)

// PlaygroundKeyName 网页端「在线使用」自动建的内部钥匙名，同时是保留名。
//
// 网页端要拿用户的某把钥匙走网关计费，所以充过值、又没建过钥匙的用户第一次使用时，
// 系统替他建这一把。它**不出现在用户的「API 密钥」列表里**：用户只该看到自己建的钥匙，
// 一把来路不明的钥匙只会让人困惑（注册送的「试用密钥」就是这么被删掉的）。
// 靠名字识别，所以用户不能把自己的钥匙起成这个名字，见 IsReservedAPIKeyName。
const PlaygroundKeyName = "在线使用"

// IsReservedAPIKeyName 是否为系统保留的钥匙名。用户侧新建、改名都要拒绝：
// 撞了名，用户自己的钥匙会从列表里消失。
func IsReservedAPIKeyName(name string) bool {
	return strings.TrimSpace(name) == PlaygroundKeyName
}

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
