# Organization media quantity contracts

WindHub is the authority for image counts and video seconds. These quantities are
independent of money and standard Tokens; they do not replace existing billing.
CRM addresses an organization by `(issuer, environment, organization_id)`. Tenant
applications read only through that organization's service API key.

## API

- Admin GET `/api/v1/admin/organization-services/:issuer/:environment/:organization_id/media-quota`
- Admin POST same path + `/grants`, `Idempotency-Key` equals JSON `request_id`.
- Service-key GET `/v1/organization/media-quota?page=1`, 50 records per page.
- Grants contain `expected_version`, `contract_ref`, `reason`, `images`,
  `video_seconds`, `effective_at`, `expires_at`. Dates have time zones.

The first grant enables enforcement for the whole organization. Ungranted media
kinds then have zero quota. Existing unconfigured organizations and personal
accounts retain their prior policy. Grants add capacity without resetting usage.
Expired unused quantities cannot fund new tasks. Active grants are consumed by
earliest expiry; late results settle their original allocations.

Generation requires `X-Client-Request-ID` and bounded non-streaming JSON at the
standard image generation/edit or video generation endpoints (including async
image endpoints and their direct aliases). Images use integer `n` (1–10); videos
use a known model's fixed duration. Automatic duration, auto multi-image modes,
batch generation, video edit/extension and native image tools on chat endpoints
are rejected for configured organizations. WebSocket requests are rejected because
later frames could enable unaccounted image tools; HTTP text chat is unchanged.

## Ledger and recovery

All mutations serialize on the organization user row. A reservation, selected
contract lots and their counters commit before supplier dispatch. A retry with
the same identity and body replays the original task, never submits again. Changed
parameters conflict. Gateway failover cannot create a second generation attempt
against the same reservation when the first result is uncertain.

Confirmed output settles once; confirmed rejection/failure releases once. Partial
image output consumes its actual count. Video receipts use returned duration when
present; otherwise the accepted fixed duration is recorded as `fixed_duration`,
not measured file duration. Verified failure webhooks and authenticated polling
can settle the same task safely. Uncertain outcomes keep the reservation, including
after client disconnect or gateway restart. No automatic timeout refund.

A supplier output exceeding the bounded request marks the organization for review
and blocks further generation. It retains the reservation and observed output for
operator investigation; there is no automatic balance reset or invented refund.
Reconciliation of such supplier contract violations remains a manual operations
procedure. No generation content, credential or customer email is stored here.

Migration 238 is additive; application rollback retains its tables and records.
Never drop quota data during rollback. Production grants are not created by tests.

## Verification

`go test ./internal/handler ./internal/handler/admin ./internal/server/routes`
and `go test ./internal/service -run TestMediaQuotaVideoUnits`.
Repository tests require explicit `MEDIA_QUOTA_TEST_DSN` pointing to a disposable
`media_quota_test` database on 127.0.0.1. They create/drop only their own schema.
They cover 20 parallel attempts at the last image/5 seconds, lost grant receipts,
identity isolation, partial output, duplicate settlement, unknown submission,
expiry across contracts and supplier overproduction.
