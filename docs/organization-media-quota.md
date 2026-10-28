# Organization media quantity contracts

WindHub is the authority for image counts and video seconds. These quantities are
independent of money and standard Tokens; they do not replace existing billing.
CRM addresses an organization by `(issuer, environment, organization_id)`. Tenant
applications read only through that organization's service API key.

## API

- Admin GET `/api/v1/admin/organization-services/:issuer/:environment/:organization_id/media-quota`
- Admin POST same path + `/grants`, `Idempotency-Key` equals JSON `request_id`.
- Service-key GET `/v1/organization/media-quota?page=1`, 50 records per page.
- Service-key GET `/v1/organization/media-quota/video-rule?model=<name>` returns the same
  `{model, quota_model, supported, rule}` as the admin video-rule endpoint, for the key's own
  group. It rewrites the name with the functions of `compositeTargetPlatformMiddleware`, so the
  answer is what the quota guard enforces. Tenants use it to offer only valid durations;
  `supported: false` means a configured organization cannot make video with the model.
- Grants contain `expected_version`, `contract_ref`, `reason`, `images`,
  `video_seconds`, `effective_at`, `expires_at`. Dates have time zones.
- Admin GET `/api/v1/admin/groups/:id/media-quota/video-rule?model=<name>` returns
  `{group_id, model, quota_model, supported, rule}`. It rewrites the name as an
  organization's video request in that group is rewritten before the quota guard
  (organization override, then the composite route) and looks up the enforced
  duration rule. CRM calls it before saving a video model for an organization that
  has a quota and before a first grant: in October 2026 four configured
  organizations had `veo3.1-fast`, which has no rule, and could make no video.

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

Seedance 2, 2 Fast and 2 Mini accept `image_with_roles` for input frames on the
video generation endpoint. This field is passed through unchanged; it does not
relax the single-video, fixed-duration, quota reservation or replay restrictions.
Seedream may set `metadata.resolution` and boolean `metadata.watermark`; the
watermark flag does not change reserved image units. Other metadata keys, including
automatic multi-image controls, remain rejected.
Organization model overrides may route the public Seedream model to Volcengine
Ark. At that provider boundary, aspect ratios and resolution become pixel sizes,
`metadata.watermark` becomes `watermark`, and `image_urls` becomes `image`.
Seedream Pro remains one image per request. The personal ToAPIs route keeps its
original request contract. Native parameter reference (checked 2026-10-11):
https://docs.volcengine.com/docs/ark/image-generation-api?lang=zh

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

### Official Ark video routing

OpenAI groups may use the video generation and owner-bound status/content
endpoints with an API-key account whose base URL is exactly the official Ark
HTTPS endpoint (`https://ark.cn-beijing.volces.com/api/v3`). The scheduler excludes
reseller accounts even if their model mappings contain the same model ID. Keep
official model IDs in the dedicated group's published catalogue and allowlist;
do not repurpose an existing shared/composite group to label it official.

The gateway translates `prompt`, optional first-frame `image`, `aspect_ratio`,
`resolution` and fixed integer `duration` to Ark's native task API. It sends an
explicit 5-second/720p default; Seedance 2.0 accepts 4–15 seconds and 2.5 accepts
4–30 seconds. Automatic duration, prompt parameter flags, native `content`, frame
counts, edits and extensions are rejected by this bounded contract. Seedance 2.5
first-frame generation uses the input image's ratio (`adaptive`).

Polling stays on the original account and API-key owner. Confirmed Ark expiry is
reported as terminal failure for the existing refund/quantity settlement flow.
Downloads use the documented Ark content-storage host, without forwarding the
upstream API key to storage. A video-model entitlement error does not quarantine
the same account's image models. Provider activation and actual generation still
need separate operational verification; listing a model alone does not prove them.

Provider contract: [create task](https://docs.volcengine.com/docs/ark/create-video-generation-task-api?lang=zh),
[query task](https://docs.volcengine.com/docs/ark/get-video-generation-task-api?lang=zh).

`go test -tags unit ./internal/service -run TestSeedanceArk` covers official host
selection, reseller exclusion, native request translation, bounded durations,
polling, refunds, owner isolation and credential-free content downloads.

`go test ./internal/handler ./internal/handler/admin ./internal/server/routes`
and `go test ./internal/service -run TestMediaQuotaVideoUnits`.
Repository tests require explicit `MEDIA_QUOTA_TEST_DSN` pointing to a disposable
`media_quota_test` database on 127.0.0.1. They create/drop only their own schema.
They cover 20 parallel attempts at the last image/5 seconds, lost grant receipts,
identity isolation, partial output, duplicate settlement, unknown submission,
expiry across contracts and supplier overproduction.
