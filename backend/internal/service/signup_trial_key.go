package service

import (
	"context"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// 注册赠送试用密钥的配置键。
const (
	SettingSignupTrialKeyEnabled = "signup_trial_key_enabled"
	SettingSignupTrialKeyQuota   = "signup_trial_key_quota"
	SettingSignupTrialKeyGroupID = "signup_trial_key_group_id"
)

const (
	// 默认试用额度（积分）。
	DefaultSignupTrialKeyQuota = 1000
	// 试用额度上限。设置项超过这个值一律按上限收敛——赠送额度直接对应真金白银的
	// 上游成本，配置写错一个零就是十倍支出。
	MaxSignupTrialKeyQuota = 3000
	// SignupTrialKeyName 试用密钥的名称，便于用户在密钥列表里认出它。
	SignupTrialKeyName = "试用密钥"
)

// SignupTrialKeyIssuer 注册后发放试用密钥所需的最小能力集。
//
// 用窄接口而不是直接依赖 *APIKeyService：AuthService 在 wire 里构造，
// 直接加构造参数会牵动 wire_gen 与全部测试调用点。
type SignupTrialKeyIssuer interface {
	Create(ctx context.Context, userID int64, req CreateAPIKeyRequest) (*APIKey, error)
	List(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error)
}

// SetSignupTrialKeyIssuer 注入试用密钥发放能力。未注入时该功能静默跳过。
func (s *AuthService) SetSignupTrialKeyIssuer(issuer SignupTrialKeyIssuer) {
	if s == nil {
		return
	}
	s.trialKeyIssuer = issuer
}

// signupTrialKeyConfig 读取试用密钥配置（注册路径用）。
func (s *AuthService) signupTrialKeyConfig(ctx context.Context) (enabled bool, quota float64, groupID *int64) {
	if s == nil {
		return false, 0, nil
	}
	return s.settingService.SignupTrialKeyConfig(ctx)
}

// SignupTrialKeyConfig 读取试用密钥配置。
//
// 关闭时返回 enabled=false；额度未配置走默认 1000，超过上限收敛到 3000。
// 提到 SettingService 上是为了让注册路径与「在线使用」补发路径共用同一份口径——
// 两处各读一遍设置，迟早会读出两套规则。
func (s *SettingService) SignupTrialKeyConfig(ctx context.Context) (enabled bool, quota float64, groupID *int64) {
	if s == nil || s.settingRepo == nil {
		return false, 0, nil
	}
	vals, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingSignupTrialKeyEnabled, SettingSignupTrialKeyQuota, SettingSignupTrialKeyGroupID,
	})
	if err != nil {
		return false, 0, nil
	}
	if strings.TrimSpace(vals[SettingSignupTrialKeyEnabled]) != "true" {
		return false, 0, nil
	}

	quota = DefaultSignupTrialKeyQuota
	if raw := strings.TrimSpace(vals[SettingSignupTrialKeyQuota]); raw != "" {
		if v, err := strconv.ParseFloat(raw, 64); err == nil && v > 0 {
			quota = v
		}
	}
	if quota > MaxSignupTrialKeyQuota {
		quota = MaxSignupTrialKeyQuota
	}

	if raw := strings.TrimSpace(vals[SettingSignupTrialKeyGroupID]); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			groupID = &v
		}
	}
	return true, quota, groupID
}

// issueSignupTrialKey 给新注册用户发一把带试用额度的密钥。
//
// 额度走 api_keys.quota，而不是给用户余额加钱：
//   - quota 由鉴权中间件强制（IsQuotaExhausted），耗尽即拦在网关入口，符合「用完即停」
//   - 与充值余额分开记，事后能分清哪些是赠送、哪些是实付，对账不会乱
//
// 全程 fail-open：发放失败只记日志，绝不阻断注册——用户注册不该因为送不出密钥而失败。
func (s *AuthService) issueSignupTrialKey(ctx context.Context, userID int64) {
	if s == nil || s.trialKeyIssuer == nil || userID <= 0 {
		return
	}
	enabled, quota, groupID := s.signupTrialKeyConfig(ctx)
	if !enabled || quota <= 0 {
		return
	}

	// 幂等：已有密钥就不再发。postAuthUserBootstrap 在 OAuth 路径上也会走到，
	// 老用户重新登录时不该再拿一份试用额度。
	existing, _, err := s.trialKeyIssuer.List(ctx, userID, pagination.PaginationParams{Page: 1, PageSize: 1}, APIKeyListFilters{})
	if err != nil {
		logger.LegacyPrintf("service.auth",
			"[Auth] trial key: list existing failed user=%d err=%v", userID, err)
		return
	}
	if len(existing) > 0 {
		return
	}

	key, err := s.trialKeyIssuer.Create(ctx, userID, CreateAPIKeyRequest{
		Name:    SignupTrialKeyName,
		GroupID: groupID,
		Quota:   quota,
	})
	if err != nil {
		logger.LegacyPrintf("service.auth",
			"[Auth] trial key: create failed user=%d quota=%.2f err=%v", userID, quota, err)
		return
	}
	logger.LegacyPrintf("service.auth",
		"[Auth] trial key issued user=%d key_id=%d quota=%.2f", userID, key.ID, quota)
}
