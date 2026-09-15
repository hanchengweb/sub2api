# Organization service identities

Development source: `hanchengweb/sub2api`, branch
`codex/organization-service-accounts`, based on `main` at `57edf77f1`.
The tenant repository branch `codex/legacy-consolidation-20260806` is retired
and is not this application's development branch.

## Identity boundary

An organization service account is an existing WindHub user with
`account_type=organization_service` and immutable organization metadata.
It retains the existing balance, API key, group, limits and usage machinery.
Its role remains `user`; it cannot become an administrator or obtain an
interactive access token. Human users retain `account_type=personal`.

The identity key is `(organization_issuer, organization_environment,
organization_id)`. The ID is the operations system's immutable ID, not a
display name, school name, request header or tenant slug. Issuers must be
chosen consistently by the operator. Test and production have separate
identities. Soft deletion does not release the identity for reassignment.

The organization contract/member ledger remains in WeMoreAI. WindHub's
balance and group pricing represent upstream service billing and must not
be interpreted as WeMoreAI standard Tokens.

## Create and inspect

Use the existing authenticated administrator API:

```http
POST /api/v1/admin/users
Idempotency-Key: <stable-operation-id>
Content-Type: application/json

{
  "email": "organization-service@example.test",
  "username": "Acceptance organization",
  "account_type": "organization_service",
  "organization_issuer": "wemoreai-ops",
  "organization_id": "<platform-organization-uuid>",
  "organization_environment": "test",
  "balance": 0,
  "concurrency": 1,
  "allowed_groups": [123]
}
```

The group ID is an example, not a verified production group. Do not send a
password. The server generates an undisclosed random password and refuses
interactive token issuance regardless of password/OAuth/refresh entry point.
The JWT middleware also rejects a service identity, even with a valid JWT.
Ordinary signup cannot select this type.

The admin user response includes the four identity fields. They are not
included in the ordinary user DTO. The existing admin update endpoint can
manage status, group permissions and limits; it cannot change the identity,
set a service-account password or promote it to administrator. Service
creation does not apply the normal signup balance or default subscriptions.

Same-key retries use the existing idempotency coordinator. The database
unique constraint prevents duplicate identities even with different keys.
If the first transaction committed but its HTTP/idempotency receipt was
lost, the caller must inspect the existing identity instead of creating a
different account. Automatic identity lookup/recovery is a subsequent API
step; uniqueness alone is not an end-to-end delivery receipt.

## Migration and rollout

Migration `199_organization_service_identity.sql` adds defaulted columns to
existing users, validates the service-account shape and reserves the unique
organization binding. Apply through the normal migration runner before
starting the new application. There is no production migration in this work.

After any service identity exists, do not roll back to an older binary that
ignores `account_type` without first disabling those service identities and
revoking their API keys. Restoring human login behavior would break the
security boundary. Preserve the identity and billing history.

## Remaining integration gates

- Restricted operations-system authentication, identity lookup and ambiguous
  retry recovery; the current API uses existing WindHub administrator auth.
- Managed key issuance/rotation and key-to-group validation without exposing
  credentials to tenant browsers or desktop clients.
- Operations-side organization binding and signed tenant delivery.
- Tenant Web/desktop/Harness credential resolution with no global-key fallback.
- Model entitlement intersection and real usage/cost reconciliation.
- Admin UI account-type display/filtering and guided service-account creation.

This identity foundation is not a completed commercial allocation flow.
No production identities, keys, balances or school allocations are changed.

## Validation

Focused checks passed with Go 1.26.5:

```sh
go generate ./ent
go test -tags=unit ./internal/service ./internal/server/middleware ./internal/handler/admin ./internal/repository -run 'TestOrganization|TestJWTAuth|TestAuthService|TestAdminService_(CreateUser|UpdateUser)|TestExecuteAdminIdempotent|TestUserRepository' -count=1
```

The repository test uses isolated SQLite with the Ent-generated CHECK and
unique index, and verifies identity round-trip, duplicate refusal, separate
organizations/environments, promotion refusal and binding reservation after
soft deletion. The HTTP test verifies same-key replay, changed-payload
conflict and missing idempotency store/key refusal. Existing human auth and
user creation/update regressions are included.

Production PostgreSQL migration and concurrency checks remain pending;
Docker was unavailable locally. Do not treat SQLite as proof of a successful
PostgreSQL rollout or of real WindHub model usage.
