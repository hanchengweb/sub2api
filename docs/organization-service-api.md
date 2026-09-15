# Organization service management API

All routes require existing admin authentication and audit middleware. Identity is the exact tuple `(issuer, environment, organization_id)` and `account_type=organization_service`; environments are test or production.

Base: `/api/v1/admin/organization-services/:issuer/:environment/:organization_id`

- GET base: inspect the service identity.
- POST `/keys`: require Idempotency-Key and `{email,name,group_id,key}`. The operations server generates and encrypts the custom key before calling; responses contain only identity/key/group IDs and status. A disabled account is not implicitly restored. Retries recover an already-created identity or matching key; wrong owner/group/status is rejected.
- PUT `/status`: active or disabled, using existing user service/cache invalidation.
- DELETE `/keys/:key_id`: ownership checked using existing APIKeyService.
- GET `/usage`: user_id is always overwritten with the resolved organization account. Supports key and exact request_id filters. Nested API key and user objects are removed from this route's usage DTO.

The key request body is omitted from audit storage. Existing key issuance, group entitlement and quota rules remain authoritative; provisioning does not grant a prepaid balance or convert a platform contract into gateway credit.

Existing service-account login restrictions and the 234 single-transaction CHECK migration remain unchanged. Migration timing is lock_timeout=2s per lock wait and statement_timeout=15s, not a total 2-second outage guarantee.

Tests cover exact identity lookup, wrong organization/environment rejection, forced usage scope, malformed key response redaction and existing admin/audit paths. Actual multi-service provisioning, gateway billing and rotation recovery still require a staging acceptance run; this API has not been deployed.
