package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type desktopProfileUserRepo struct {
	UserRepository
	user   *User
	avatar *UserAvatar
}

func (r *desktopProfileUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	if id != r.user.ID {
		return nil, ErrUserNotFound
	}
	return r.user, nil
}

func (r *desktopProfileUserRepo) GetUserAvatar(_ context.Context, id int64) (*UserAvatar, error) {
	if id != r.user.ID {
		return nil, ErrUserNotFound
	}
	return r.avatar, nil
}

func TestDesktopCheckoutReadsCurrentDisplayIdentityOnly(t *testing.T) {
	client := newPaymentConfigServiceTestClient(t)
	repo := &desktopProfileUserRepo{user: &User{ID: 17, Username: "真实昵称", Email: "private@example.test", PasswordHash: "private-hash", Notes: "private-note", Status: StatusActive}, avatar: &UserAvatar{URL: "https://example.test/avatar.png"}}
	svc := &PaymentService{userRepo: repo, configService: &PaymentConfigService{entClient: client, settingRepo: &paymentConfigSettingRepoStub{}}}
	key := &APIKey{UserID: 17, User: &User{Username: "旧缓存昵称"}}
	for _, withAvatar := range []bool{true, false} {
		if !withAvatar {
			repo.avatar = nil
			repo.user.Username = "更新昵称"
		}
		result, err := svc.DesktopRechargeCheckout(context.Background(), key)
		require.NoError(t, err)
		account, ok := result["account"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, repo.user.Username, account["nickname"])
		if withAvatar {
			require.Equal(t, "https://example.test/avatar.png", account["avatar_url"])
		} else {
			require.Equal(t, "", account["avatar_url"])
		}
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		for _, secret := range []string{"private@example.test", "private-hash", "private-note", "旧缓存昵称"} {
			require.NotContains(t, string(encoded), secret)
		}
	}
	key.UserID = 18
	_, err := svc.DesktopRechargeCheckout(context.Background(), key)
	require.ErrorIs(t, err, ErrUserNotFound)
}

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
