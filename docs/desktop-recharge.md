# Desktop personal recharge

The desktop's saved WindHub personal connection can replenish its existing account without a second email/password login. This is a narrow additional API-key capability, not a user JWT exchange. The confirmation screen identifies the destination with a stable account reference and masked address, since even an unrestricted key can have been shared.

Routes under `/v1/desktop/recharge`:

- `GET /checkout`: current balance, masked destination, WeChat methods and current website fee/multiplier/limits.
- `POST /orders`: requires a UUID v4 `Idempotency-Key` and `{amount, account_reference, fee_rate, multiplier}`. Destination is derived from the authenticated key. Only WeChat balance replenishment is available.
- `GET /orders/:request_id`: recover only the matching personal account and API key's desktop order.
- `POST /orders/:request_id/cancel`: cancel only that same order through existing payment reconciliation.

Existing key, user, group and IP authentication remains in force. Only the balance gate is bypassed on these exact registered routes. Expired, disabled, organization-service, quota/rate-limited and subscription keys are rejected. There are no profile, key management, order history, subscription purchase or refund routes.

Creation uses the existing database-backed idempotency coordinator (required, fail closed) and records key/request/amount attribution in the order's existing provider snapshot before contacting the provider. An existing attributed order is recovered after restart or coordinator record expiry, never recreated. An ambiguous provider response leaves the desktop order pending for existing signed-query/webhook reconciliation. No schema migration or payment-provider configuration change.

Validation: `go test -p 2 -tags unit ./internal/... -count=1` passed against this change. Tests include zero balance, disabled/expired/restricted/service accounts, wrong key/account/request recovery, changed amount/destination, missing credentials, nonexistent management/refund routes and an unavailable idempotency coordinator. No real payment/order was created for acceptance.

References reviewed: [OWASP authorization](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html), [WeChat Native callback verification](https://pay.wechatpay.cn/doc/v3/merchant/4012791882). Existing settlement and provider signature checks are reused.
