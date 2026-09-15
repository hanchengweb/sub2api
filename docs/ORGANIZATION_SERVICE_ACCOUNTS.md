# Organization service identities

Development source: `hanchengweb/sub2api`, branch
`codex/organization-service-accounts`, merged with production source `31f3df5fe` without rewriting published history.
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

Production target is `wxm-tenant-platform-hz-01`, domain `windhub.online`.
On 2026-09-15, direct inspection confirmed application `31f3df5fe`,
PostgreSQL 18.6, 285 migration receipts and 17 users. The earlier claim that
production ran `95595a522` / PostgreSQL 18.4 / 277 receipts was incorrect:
those observations belong to `wxm-legacy-ecs`, not the domain's current target.
`fork/main` was fast-forwarded to `31f3df5fe` and merged into this branch.

Migration `234_organization_service_identity.sql` is the sole intended new
migration. Historical files and receipts must not be rewritten. Receipts
are keyed by full filename, and checksums use SHA256(strings.TrimSpace(SQL)).
The production database contains histories from both lines, including both
196 filenames; an empty-database test cannot replace restored-copy rehearsal.

Lock strategy: one runner transaction, ordinary CHECK, SET LOCAL
lock_timeout='2s', statement_timeout='15s'. Do not split into NOT VALID and
VALIDATE in this file: the exclusive lock would still last until COMMIT.
The small observed table does not justify two-phase migration complexity.
The timeouts bound each lock wait / statement, not total outage duration.
An exclusive-lock waiter can queue later readers. Failed DDL and its receipt
must roll back together; do not repeatedly restart a blocked candidate.

### Staged rollback

Before any service identity exists, retain the additive columns and roll
back only the application after checking compatibility. Never erase billing
history or migration receipts to roll back a binary.

After service identities exist, freeze organization provisioning and admin
changes first. While the new binary is still running, disable every service
identity (`status=disabled`), revoke its API keys through the supported admin
path, and confirm cache invalidation and failed authentication before swapping
binaries. Re-read the state after rollback. Preserve organization metadata,
keys' audit history and balances; organization API traffic will be interrupted.

Old `31f3df5fe` permits administrators to both reset passwords and set status
back to active. Therefore disabling is necessary but only remains effective
while reactivation is prohibited during the rollback window. If this cannot
be enforced, do not use that old binary: retain/backport the identity guard.
An empty password hash is not a substitute, because an old administrator can
replace it. The current random password uses 32 random bytes (256 bits), not
128 bits; password secrecy is not the authorization boundary.

### Legacy disposition

`wxm-legacy-ecs` is NOT WindHub production and must never supply its release
baseline. It still runs unrelated services. Its nginx retains a windhub.online
route to the legacy Sub2API, so DNS alone does not establish that it is unused.
At 2026-09-15 inspection it had zero usage rows in the past seven days; this
is not proof of no direct/internal callers. Mark the old deployment explicitly;
retain it pending caller attribution rather than stop shared infrastructure.

### Release gate

Before production writes, verify host + public DNS/gateway routing + exact
container image identity + source commit. Rehearse the exact candidate with
the actual runner on an isolated restored Hangzhou database, with no network
access to providers, email, Redis or other production services. Preserve all
migration receipts including duplicate numeric prefixes. Verify restore,
legacy values, one new receipt, second-start replay and staged rollback.
This document is not evidence that production migration or allocation passed.

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
$env:WINDHUB_PG234_TEST='1'
go test -tags=unit ./internal/repository -run '^TestOrganizationMigrationPostgres$' -count=1 -v
```

The check applies every historical migration before 234, seeds 10,000 users
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
live availability or real WindHub model usage. Migration 234 is still an
unreleased draft; do not edit its checksum after any shared deployment.

## Hangzhou restored-copy rehearsal (2026-09-15 UTC)

A fresh pg_dump of the actual Hangzhou business database was restored with
pg_restore --exit-on-error into an isolated PostgreSQL 18.6 container using
the production PostgreSQL image ID. It had network=none, no published ports,
a 1 GiB memory limit and tmpfs for both data and dump. No application or
provider/email workers were started against the copy.

The runner executable used the unchanged migrations_runner.go implementation
(only its package declaration changed for a standalone harness), plus this
branch's embedded migrations. Results:

- All 285 original receipts restored, including both 196 filenames.
- Upgrade produced 286 receipts; only 234 was newly applied; replay succeeded.
- All original fields of all 17 users and all API key rows matched before/after.
- Restoring the dump again succeeded. Injected division-by-zero after the DDL
  left 285 receipts and no account_type column. An existing reader triggered
  lock timeout, and retry after the reader completed succeeded.
- Focused Go service/middleware/admin/repository checks passed. The target
  verifier passed against live Hangzhou; its runnable test rejects mismatched
  host, DNS, immutable image ID, source tag and gateway routing.

Run the read-only target gate with independently verified release inputs:

```sh
python deploy/test_verify_windhub_target.py
python deploy/verify-windhub-target.py --commit <source-commit> --image-id <immutable-image-id>
```

The image ID plus tag links to the previously verified release; it is not a
reproducible-build attestation. The retained legacy deployment is marked by
ENVIRONMENT-LEGACY-NOT-PRODUCTION.txt in its Compose working directory.
The isolated rehearsal container and its in-memory data were removed after
validation. Production still has 285 receipts and no service-identity columns.

Not yet validated: old-binary end-to-end admin/login/cache rollback, a new
production application artifact, and the remaining organization allocation
integration gates above. This PR is not a claim of production deployment.
