package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// SettingKeyOrganizationModelOverrides 组织（B 端）请求的模型改道表。
//
// B 端组织和 C 端用户共用同一个分组，分组这一层分不开两边的流量；可两边要走的
// 上游不一样——豆包 5.0 Pro 在 C 端走 toAPI，B 端要走火山方舟官方。
// 这张表按「请求里的模型名」把组织的请求改写成另一个公开模型名，之后的组合路由、
// 账号调度、计费、日志看到的都是改写后的名字。C 端请求（用户没有组织身份）
// 根本不会查这张表。
//
// 值是 JSON：
//
//	{"doubao-seedream-5-0-pro": {"model": "v-doubao-seedream-5-0-pro", "sizes": {"1.5K": "1536x1536"}}}
//
// sizes 可选，按请求 size 的原值（不区分大小写）改写。
//
// 没有管理界面，直接写 settings 表；进程内缓存 60s，改完最多一分钟生效。
// 删掉这一行就是回滚：一分钟内组织请求回到和 C 端相同的路由。
const SettingKeyOrganizationModelOverrides = "organization_model_overrides"

const (
	organizationModelOverridesCacheTTL  = 60 * time.Second
	organizationModelOverridesDBTimeout = 3 * time.Second
)

// OrganizationModelOverride 一条组织改道规则。
type OrganizationModelOverride struct {
	Model string            `json:"model"`
	Sizes map[string]string `json:"sizes,omitempty"`
}

// SizeFor 按请求里的 size 取改写后的值；没有对应规则时 ok=false。
func (o OrganizationModelOverride) SizeFor(size string) (string, bool) {
	size = strings.TrimSpace(size)
	if size == "" {
		return "", false
	}
	for from, to := range o.Sizes {
		to = strings.TrimSpace(to)
		if to != "" && strings.EqualFold(strings.TrimSpace(from), size) {
			return to, true
		}
	}
	return "", false
}

type cachedOrganizationModelOverrides struct {
	value     map[string]OrganizationModelOverride
	expiresAt int64
}

// OrganizationModelOverride 取某个模型的组织改道规则。
//
// 在网关热路径上调用（只有带组织身份的请求才会走到），所以读的是进程内缓存。
func (s *SettingService) OrganizationModelOverride(ctx context.Context, model string) (OrganizationModelOverride, bool) {
	model = strings.TrimSpace(model)
	if s == nil || model == "" {
		return OrganizationModelOverride{}, false
	}
	override, ok := s.organizationModelOverrides(ctx)[model]
	return override, ok
}

func (s *SettingService) organizationModelOverrides(ctx context.Context) map[string]OrganizationModelOverride {
	if cached, ok := s.organizationModelOverridesCache.Load().(*cachedOrganizationModelOverrides); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	result, _, _ := s.organizationModelOverridesSF.Do(SettingKeyOrganizationModelOverrides, func() (any, error) {
		previous, _ := s.organizationModelOverridesCache.Load().(*cachedOrganizationModelOverrides)
		if previous != nil && time.Now().UnixNano() < previous.expiresAt {
			return previous.value, nil
		}

		value, err := s.loadOrganizationModelOverrides(ctx)
		if err != nil && previous != nil {
			// 读库失败（不是「没配」）时沿用上一份：瞬时的库抖动不该让组织请求
			// 在这一分钟里悄悄改回 C 端的上游。
			value = previous.value
		}
		s.organizationModelOverridesCache.Store(&cachedOrganizationModelOverrides{
			value:     value,
			expiresAt: time.Now().Add(organizationModelOverridesCacheTTL).UnixNano(),
		})
		return value, nil
	})
	value, _ := result.(map[string]OrganizationModelOverride)
	return value
}

// loadOrganizationModelOverrides 读库并解析。设置不存在返回空表且 err=nil。
func (s *SettingService) loadOrganizationModelOverrides(ctx context.Context) (map[string]OrganizationModelOverride, error) {
	if s.settingRepo == nil {
		return nil, nil
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), organizationModelOverridesDBTimeout)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyOrganizationModelOverrides)
	if errors.Is(err, ErrSettingNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseOrganizationModelOverrides(raw), nil
}

// parseOrganizationModelOverrides 解析改道表；JSON 写坏时返回空表。
//
// 写坏宁可不改道（照常走 C 端的路，照样能出图），也不能把组织请求改成一个
// 不存在的模型名打挂。目标为空、或者和原模型同名的条目直接丢掉。
func parseOrganizationModelOverrides(raw string) map[string]OrganizationModelOverride {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var parsed map[string]OrganizationModelOverride
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	out := make(map[string]OrganizationModelOverride, len(parsed))
	for from, override := range parsed {
		from = strings.TrimSpace(from)
		override.Model = strings.TrimSpace(override.Model)
		if from == "" || override.Model == "" || override.Model == from {
			continue
		}
		out[from] = override
	}
	return out
}
