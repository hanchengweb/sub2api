//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 只记录写入内容的假仓储，其余方法不参与本组用例。
type capturingSettingRepo struct {
	written map[string]string
}

func (r *capturingSettingRepo) Get(context.Context, string) (*Setting, error) { return nil, nil }
func (r *capturingSettingRepo) GetValue(context.Context, string) (string, error) {
	return "", nil
}
func (r *capturingSettingRepo) Set(context.Context, string, string) error { return nil }
func (r *capturingSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *capturingSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.written = settings
	return nil
}
func (r *capturingSettingRepo) GetAll(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (r *capturingSettingRepo) Delete(context.Context, string) error { return nil }

// 只带一个字段的更新，不得触碰其余支付设置。
//
// 2026-09-07 线上就是这么坏的：一个 {"balance_recharge_multiplier":100} 的 PUT 把
// payment_enabled、支付方式、最小充值额全写成空串，充值入口整个消失。原实现无条件
// 构造约 26 项的全量 map，未提供的字段被格式化成空串一并写库。
func TestUpdatePaymentConfig_PartialUpdateLeavesOtherSettingsAlone(t *testing.T) {
	repo := &capturingSettingRepo{}
	svc := NewPaymentConfigService(nil, repo, nil)

	multiplier := 100.0
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		BalanceRechargeMultiplier: &multiplier,
	}))

	require.Equal(t, map[string]string{SettingBalanceRechargeMult: "100.00"}, repo.written,
		"只提供了倍率，就只能写倍率这一项")

	for _, key := range []string{
		SettingPaymentEnabled,
		SettingEnabledPaymentTypes,
		SettingMinRechargeAmount,
		SettingMaxRechargeAmount,
		SettingBalancePayDisabled,
		SettingPaymentVisibleMethodAlipayEnabled,
		SettingPaymentVisibleMethodWxpayEnabled,
	} {
		require.NotContains(t, repo.written, key, "未提供的字段不得写入: %s", key)
	}
}

// 显式传值仍要能清空——「未提供」和「想清掉」是两回事，不能一起吞掉。
func TestUpdatePaymentConfig_ExplicitZeroStillClears(t *testing.T) {
	repo := &capturingSettingRepo{}
	svc := NewPaymentConfigService(nil, repo, nil)

	zero := 0.0
	disabled := false
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		MinAmount:    &zero,
		Enabled:      &disabled,
		EnabledTypes: []string{},
	}))

	require.Equal(t, "", repo.written[SettingMinRechargeAmount])
	require.Equal(t, "false", repo.written[SettingPaymentEnabled])
	require.Equal(t, "", repo.written[SettingEnabledPaymentTypes])
	require.Contains(t, repo.written, SettingEnabledPaymentTypes, "显式传空数组应写入空串")
}

// 开启支付要能正常写入 true，别在修复「不要误清空」时把正常路径也堵了。
func TestUpdatePaymentConfig_EnablesPayment(t *testing.T) {
	repo := &capturingSettingRepo{}
	svc := NewPaymentConfigService(nil, repo, nil)

	enabled := true
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		Enabled:      &enabled,
		EnabledTypes: []string{"alipay", "wxpay"},
	}))

	require.Equal(t, "true", repo.written[SettingPaymentEnabled])
	require.Equal(t, "alipay,wxpay", repo.written[SettingEnabledPaymentTypes])
	require.NotContains(t, repo.written, SettingBalanceRechargeMult, "没提供倍率就不该改倍率")
}
