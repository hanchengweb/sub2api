package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// This capability can only add balance to the key's existing personal account.
// It grants no user session, order history, refund or account-management rights.
func ValidateDesktopRechargeKey(key *APIKey) error {
	if key == nil || key.User == nil || !key.User.IsActive() || !key.IsActive() || key.IsExpired() {
		return infraerrors.Forbidden("RECHARGE_CONNECTION_INVALID", "个人连接已失效，请更新桌面连接后重试")
	}
	if !key.User.CanLogin() || key.Quota > 0 || key.HasRateLimits() || (key.Group != nil && key.Group.IsSubscriptionType()) {
		return infraerrors.Forbidden("RECHARGE_PERSONAL_ONLY", "此连接使用组织或受限额度，不能为它充值个人余额；请使用个人余额连接")
	}
	return nil
}

func (s *PaymentService) DesktopRechargeCheckout(ctx context.Context, key *APIKey) (map[string]any, error) {
	user, err := s.userRepo.GetByID(ctx, key.UserID)
	if err != nil {
		return nil, err
	}
	if !user.IsActive() || !user.CanLogin() {
		return nil, ErrServiceAccountLogin
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, err
	}
	limits, err := s.configService.GetAvailableMethodLimits(ctx)
	if err != nil {
		return nil, err
	}
	methods := map[string]MethodLimits{}
	for name, method := range limits.Methods {
		if NormalizeVisibleMethod(name) == payment.TypeWxpay && (method.Currency == "" || method.Currency == "CNY") {
			methods[name] = method
		}
	}
	// A stable account reference and masked address make the credit destination
	// visible without exposing the complete email or other profile information.
	address := strings.SplitN(user.Email, "@", 2)
	label := "个人账户"
	if len(address) == 2 && len([]rune(address[0])) > 0 {
		label = string([]rune(address[0])[:1]) + "***@" + address[1]
	}
	return map[string]any{
		"account": map[string]any{"reference": fmt.Sprintf("WH-%d", user.ID), "label": label, "balance": user.Balance, "connection": key.Name},
		"methods": methods, "balance_disabled": !cfg.Enabled || cfg.BalanceDisabled,
		"recharge_fee_rate": cfg.RechargeFeeRate, "balance_recharge_multiplier": cfg.BalanceRechargeMultiplier,
	}, nil
}

// requestID is a client-generated UUID, durably recorded before the provider
// call. Recovery never creates an order and cannot access another key's orders.
func (s *PaymentService) desktopRechargeOrder(ctx context.Context, key *APIKey, requestID string) (*dbent.PaymentOrder, error) {
	order, err := s.entClient.PaymentOrder.Query().Where(
		paymentorder.UserIDEQ(key.UserID), paymentorder.OrderTypeEQ(payment.OrderTypeBalance),
		func(q *sql.Selector) {
			q.Where(sqljson.ValueEQ(paymentorder.FieldProviderSnapshot, strconv.FormatInt(key.ID, 10), sqljson.Path("desktop_api_key_id")))
		},
		func(q *sql.Selector) {
			q.Where(sqljson.ValueEQ(paymentorder.FieldProviderSnapshot, requestID, sqljson.Path("desktop_request_id")))
		},
	).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, nil
	}
	return order, err
}

func desktopRechargeResult(o *dbent.PaymentOrder) map[string]any {
	if o == nil {
		return map[string]any{"found": false}
	}
	result := map[string]any{"found": true, "order_id": o.ID, "status": o.Status, "amount": o.Amount, "pay_amount": o.PayAmount,
		"out_trade_no": o.OutTradeNo, "expires_at": o.ExpiresAt, "currency": "CNY"}
	if o.Status == OrderStatusPending {
		result["qr_code"] = psStringValue(o.QrCode)
		result["pay_url"] = psStringValue(o.PayURL)
	}
	return result
}

func (s *PaymentService) ReadDesktopRecharge(ctx context.Context, key *APIKey, requestID string, cancel bool) (map[string]any, error) {
	order, err := s.desktopRechargeOrder(ctx, key, requestID)
	if err != nil || order == nil {
		return desktopRechargeResult(nil), err
	}
	if cancel {
		if _, err = s.CancelOrder(ctx, order.ID, key.UserID); err != nil {
			return nil, err
		}
	} else if order.Status == OrderStatusPending || order.Status == OrderStatusExpired {
		s.reconcilePaid(ctx, order)
	}
	order, err = s.GetOrder(ctx, order.ID, key.UserID)
	if err != nil {
		return nil, err
	}
	return desktopRechargeResult(order), nil
}

type DesktopRechargeInput struct {
	Amount           float64 `json:"amount"`
	AccountReference string  `json:"account_reference"`
	FeeRate          float64 `json:"fee_rate"`
	Multiplier       float64 `json:"multiplier"`
}

func (s *PaymentService) CreateDesktopRecharge(ctx context.Context, key *APIKey, requestID string, input DesktopRechargeInput, clientIP string) (map[string]any, error) {
	if input.AccountReference != fmt.Sprintf("WH-%d", key.UserID) {
		return nil, infraerrors.Conflict("RECHARGE_ACCOUNT_CHANGED", "充值账户已变化，请刷新后重新确认")
	}
	if math.IsNaN(input.Amount) || math.IsInf(input.Amount, 0) || input.Amount <= 0 || input.Amount > 1000000 || math.Abs(input.Amount*100-math.Round(input.Amount*100)) > 0.000001 {
		return nil, infraerrors.BadRequest("INVALID_AMOUNT", "请输入有效充值金额，最多两位小数")
	}
	existing, err := s.desktopRechargeOrder(ctx, key, requestID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if psSnapshotStringValue(existing.ProviderSnapshot["desktop_amount"]) != fmt.Sprintf("%.2f", input.Amount) {
			return nil, ErrIdempotencyKeyConflict
		}
		return desktopRechargeResult(existing), nil
	}
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.RechargeFeeRate != input.FeeRate || cfg.BalanceRechargeMultiplier != input.Multiplier {
		return nil, infraerrors.Conflict("RECHARGE_PRICE_CHANGED", "充值费率已变化，请刷新后重新确认")
	}
	currency, err := s.configService.ValidateMethodCurrencyConsistency(ctx, payment.TypeWxpay)
	if err != nil {
		return nil, err
	}
	if currency != "CNY" {
		return nil, infraerrors.BadRequest("RECHARGE_CURRENCY", "此通道不支持人民币充值")
	}
	_, err = s.CreateOrder(ctx, CreateOrderRequest{
		UserID: key.UserID, Amount: input.Amount, PaymentType: payment.TypeWxpay, OrderType: payment.OrderTypeBalance,
		IsMobile: false, ClientIP: clientIP, SrcHost: "windhub.online", PaymentSource: "hosted_redirect",
		ReturnURL: "https://windhub.online/payment/result", DesktopAPIKeyID: key.ID, DesktopRequestID: requestID,
	})
	if err != nil {
		return nil, err
	}
	order, err := s.desktopRechargeOrder(ctx, key, requestID)
	if err != nil {
		return nil, err
	}
	return desktopRechargeResult(order), nil
}
