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

**Deployment blocked by source baseline divergence (2026-09-14).** Direct
read-only inspection of the production app's configured PostgreSQL target
found PostgreSQL 18.4, 277 migration receipts through
`233_group_free_openai_fast.sql`, and no organization identity columns.
The running image is tagged with commit `95595a522`. This draft instead
starts from fork/main `57edf77f`, whose migration history differs: 40
production-applied files are absent and eight unrelated files precede 199
but are not applied in production. Do not deploy this draft or apply all its
pending migrations. Reconcile the release source first and port the narrow
identity change onto the production-compatible lineage with a new migration
after its actual head. Existing production migration files must not change.

At inspection, users had 8 rows / 253952 total relation bytes; no users lock
waiters or transactions older than 30 seconds were observed in that database.
These are instantaneous observations, not an availability guarantee. Every
database probe used PostgreSQL read-only mode with a statement timeout. No
production schema, identities, balances or migration receipts were written.
The isolated local test instance was stopped at the user's request.

Migration `199_organization_service_identity.sql` adds defaulted columns to
existing users, validates the service-account shape and reserves the unique
organization binding. Apply through the normal migration runner before
starting the new application. There is no production migration in this work.

The transaction sets a 2-second lock timeout and a 15-second statement
timeout. Failure rolls back migration 199 and its receipt, allowing a later
retry. This bounds waiting; it does not make the migration zero-downtime:
ALTER TABLE takes an exclusive lock, and CHECK/index creation scans users.
Keep the previous application serving until the candidate passes readiness;
do not repeatedly restart a failing candidate against busy production.

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

On 2026-09-14, PostgreSQL 18.1 was tested in a separate loopback cluster on
port 55439, with a fresh disposable database and synthetic data only:

```powershell
$env:WINDHUB_PG199_TEST='1'
go test -tags=unit ./internal/repository -run '^TestOrganizationMigrationPostgres$' -count=1 -v
```

The check applies every historical migration before 199, seeds 10,000 users
and a linked API key, then exercises the actual migration runner. Verified:
unchanged legacy password hashes/balances/roles/statuses/deletion timestamps,
personal defaults, preserved key association, legacy inserts and balance
updates, complete DDL/receipt rollback after injected SQL failure, lock
timeout and safe retry, concurrent startup/replay, concurrent identity
uniqueness, separate organizations/environments, invalid identity/admin
refusal and reservation after soft deletion. Upgrade plus two concurrent
startup calls took approximately 0.50 seconds locally; this includes the
runner's lock retry interval and is not a production timing estimate.

Production remains unmodified. Before deployment, verify its PostgreSQL
version, migration checksums/head, table size and long-running transactions;
rehearse the exact upgrade on an isolated restored backup and verify restore
recovery. Local synthetic checks do not prove production data compatibility,
live availability or real WindHub model usage. Migration 199 is still an
unreleased draft; do not edit its checksum after any shared deployment.
