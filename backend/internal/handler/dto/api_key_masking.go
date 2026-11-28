package dto

import "github.com/Wei-Shaw/sub2api/internal/service"

// APIKeyFromServiceForOwner 供会内嵌他人 key 的接口使用：只有确认 owner 是这把 key 的个人账号属主时才返回明文。
// 组织服务账号的 key 一律脱敏；属主缺失、对不上或账号类型没有加载时同样脱敏。
func APIKeyFromServiceForOwner(k *service.APIKey, owner *service.User) *APIKey {
	out := APIKeyFromService(k)
	if out == nil {
		return nil
	}
	if owner == nil || owner.ID != k.UserID || owner.AccountType != service.AccountTypePersonal {
		out.Key = maskAPIKey(out.Key)
	}
	return out
}

// maskAPIKey 只保留首尾几位。规则与前端 utils/maskApiKey.ts 一致，前端对结果再脱敏一次不会变化；
// 4 位以内整体隐藏。
func maskAPIKey(key string) string {
	switch {
	case key == "":
		return ""
	case len(key) <= 4:
		return "***"
	case len(key) <= 12:
		return key[:4] + "***"
	default:
		return key[:6] + "..." + key[len(key)-4:]
	}
}
