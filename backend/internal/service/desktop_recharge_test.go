package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestDesktopRechargeKeyBoundary(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	for _, test := range []struct {
		name    string
		change  func(*APIKey)
		allowed bool
	}{
		{"zero balance", func(k *APIKey) { k.User.Balance = 0 }, true},
		{"negative balance", func(k *APIKey) { k.User.Balance = -1 }, true},
		{"disabled key", func(k *APIKey) { k.Status = StatusAPIKeyDisabled }, false},
		{"expired key", func(k *APIKey) { k.ExpiresAt = &past }, false},
		{"disabled user", func(k *APIKey) { k.User.Status = "disabled" }, false},
		{"service account", func(k *APIKey) { k.User.AccountType = AccountTypeOrganizationService }, false},
		{"limited key", func(k *APIKey) { k.Quota = 100 }, false},
		{"rate allowance", func(k *APIKey) { k.RateLimit1d = 100 }, false},
		{"subscription", func(k *APIKey) { k.Group = &Group{SubscriptionType: SubscriptionTypeSubscription} }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			key := &APIKey{Status: StatusActive, User: &User{Status: StatusActive}}
			test.change(key)
			require.Equal(t, test.allowed, ValidateDesktopRechargeKey(key) == nil)
		})
	}
}

func TestDesktopRechargeRecoveryIsKeyAndAccountBound(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().SetEmail("recharge@example.test").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	key := &APIKey{ID: 71, UserID: user.ID}
	requestID := "11111111-1111-4111-8111-111111111111"
	svc := &PaymentService{entClient: client}
	snapshot := buildPaymentOrderProviderSnapshot(&payment.InstanceSelection{ProviderKey: payment.TypeWxpay}, CreateOrderRequest{DesktopAPIKeyID: key.ID, DesktopRequestID: requestID, Amount: 10})
	order, err := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName("fixture").
		SetAmount(10).SetPayAmount(10).SetFeeRate(0).SetRechargeCode("test-recharge").SetOutTradeNo("recharge-bound-test").
		SetPaymentType(payment.TypeWxpay).SetPaymentTradeNo("").SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).SetClientIP("127.0.0.1").SetSrcHost("windhub.online").SetProviderSnapshot(snapshot).Save(ctx)
	require.NoError(t, err)
	result, err := svc.ReadDesktopRecharge(ctx, key, requestID, false)
	require.NoError(t, err)
	require.Equal(t, order.ID, result["order_id"])
	require.NotContains(t, result, "user_email")
	require.NotContains(t, result, "provider_snapshot")
	require.NotContains(t, result, "recharge_code")
	for _, other := range []*APIKey{{ID: 72, UserID: user.ID}, {ID: 71, UserID: user.ID + 1}} {
		result, err := svc.ReadDesktopRecharge(ctx, other, requestID, true)
		require.NoError(t, err)
		require.Equal(t, false, result["found"])
	}
	result, err = svc.ReadDesktopRecharge(ctx, key, "22222222-2222-4222-8222-222222222222", false)
	require.NoError(t, err)
	require.Equal(t, false, result["found"])
	// A provider is deliberately absent: a repeated request must recover rather than charge again.
	result, err = svc.CreateDesktopRecharge(ctx, key, requestID, DesktopRechargeInput{Amount: 10, AccountReference: fmt.Sprintf("WH-%d", user.ID)}, "")
	require.NoError(t, err)
	require.Equal(t, order.ID, result["order_id"])
	_, err = svc.CreateDesktopRecharge(ctx, key, requestID, DesktopRechargeInput{Amount: 20, AccountReference: fmt.Sprintf("WH-%d", user.ID)}, "")
	require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
	_, err = svc.CreateDesktopRecharge(ctx, key, requestID, DesktopRechargeInput{Amount: 10, AccountReference: "WH-other"}, "")
	require.Error(t, err)
}
