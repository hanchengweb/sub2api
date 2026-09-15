# Organization service management API

All routes require existing admin authentication and audit middleware. Identity is the exact tuple `(issuer, environment, organization_id)` and `account_type=organization_service`; environments are test or production.

## Isolated integration acceptance (2026-09-15)

Commit `631ae3d85` was exercised against real operations/tenant APIs and separate disposable databases. Two synthetic organizations received distinct identities and keys. Idempotent replay, interrupted delivery recovery, rotation followed by old-key revocation, disable/resume, environment isolation and organization-scoped usage filtering passed. Each organization made one real DeepSeek request (36 input, 24 output tokens); `client:wxm:<request_id>` and Key ownership matched the tenant ledger, and replay created no additional upstream usage. Fixtures pre-created synthetic member/contract/desktop sessions; this does not prove customer onboarding, DSH/Temporal or production deployment.

Cost discrepancy: the two gateway rows total `0.00002352 USD`, while current official peak rates imply `0.00007920 USD`. The embedded Flash fallback remains `$0.14/$0.28` per million input/output tokens; the official pricing page now maps legacy Flash names to V4.1 Flash at peak `$0.3/$1.2`, with half-price off-peak periods. Preserve this discrepancy as a release decision; a gateway cost row is not a supplier invoice. No production prices or balances were changed. All disposable containers, volumes, network and credentials were removed.

Base: `/api/v1/admin/organization-services/:issuer/:environment/:organization_id`

- GET base: inspect the service identity.
- POST `/keys`: require Idempotency-Key and `{email,name,group_id,key}`. The operations server generates and encrypts the custom key before calling; responses contain only identity/key/group IDs and status. A disabled account is not implicitly restored. Retries recover an already-created identity or matching key; wrong owner/group/status is rejected.
- PUT `/status`: active or disabled, using existing user service/cache invalidation.
- DELETE `/keys/:key_id`: ownership checked using existing APIKeyService.
- GET `/usage`: user_id is always overwritten with the resolved organization account. Supports key and exact request_id filters. Nested API key and user objects are removed from this route's usage DTO.

The key request body is omitted from audit storage. Existing key issuance, group entitlement and quota rules remain authoritative; provisioning does not grant a prepaid balance or convert a platform contract into gateway credit.

Existing service-account login restrictions and the 234 single-transaction CHECK migration remain unchanged. Migration timing is lock_timeout=2s per lock wait and statement_timeout=15s, not a total 2-second outage guarantee.

Tests cover exact identity lookup, wrong organization/environment rejection, forced usage scope, malformed key response redaction and existing admin/audit paths. Actual multi-service provisioning, gateway billing and rotation recovery still require a staging acceptance run; this API has not been deployed.
