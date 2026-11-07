# Official voice and 3D tasks (test candidate)

This slice adds prepaid organization media allowances. `audio_characters` counts
Unicode code points of trimmed input (including internal punctuation/spaces).
`model3d_tasks` counts fixed standard-profile Tripo jobs. These packages do not
deduct standard Tokens. The platform pays the supplier from its official account;
`text_words` and `credits_consumed` are separate cost receipts. An unconfigured
organization cannot generate, and a configured organization without a grant for
the requested kind has zero allowance. Existing image/video and Edge `speech-v1`
contracts are preserved, including the digest of historical grant requests.

## Configuration and authorization

The feature is off unless `WXM_OFFICIAL_MEDIA_ENABLED=true`. Do not enable it in
production as part of this test-only change. Use new test-only supplier accounts
and organization service keys; never copy production credentials or databases.

The organization's group must be active and explicitly publish the exact public
model in enabled `models_list_config.models`. The group must be OpenAI or Composite.
A Composite group additionally needs an enabled exact route for endpoint `audio`
or `model3d`, target `openai`, and the exact native model listed below. `any`, chat,
unknown endpoints, prefix routes and automatic detectors cannot authorize this path.

Bind an API-key account in that group with all of:

| Public model | `extra.media_provider` | Exact `credentials.base_url` | Explicit model mapping value |
|---|---|---|---|
| `seed-tts-2.0` | `volcengine_speech` | `https://openspeech.bytedance.com` | `seed-tts-2.0` |
| `tripo-v3.1-standard` | `tripo` | `https://openapi.tripo3d.com/v3` | `v3.1-20260211` |

Initialize these provider fields through the existing authenticated admin account
API (`POST /api/v1/admin/accounts` or `PUT /api/v1/admin/accounts/:id`). Use
`platform: openai`, `type: apikey`, and the organization's group ID. The account
editor preserves this provider metadata and explicit mappings. Composite route
editing and preview both expose the audio and 3D endpoints.

Keep the supplier credential in the server's existing account secret storage as
`credentials.api_key`. Speech uses `X-Api-Key` and resource `seed-tts-2.0`; it never
reuses an Ark bearer. Tripo uses bearer authentication. A reseller host, wildcard
mapping, missing credential or unauthorized group stops before dispatch. There is
one selected account per submission and no fallback to ToAPIs, chat or Edge TTS.

## API and durable behavior

- `GET /v1/media/models`: the two public models, actual route availability,
  `error_code` when unavailable, approved voice/scene/emotion choices and text-only
  3D capability. Availability does not replace the quota check.
- `POST /v1/audio/generations`, `POST /v1/model3d/generations`: require a stable UUID
  in `X-Client-Request-ID`. Audio accepts model/input/voice/scene/emotion/instruction/
  speed/response_format; 3D accepts only model/prompt in this first slice.
- `GET /v1/audio/:id`, `GET /v1/model3d/:id`: owner-bound task status and usage.
- The same paths plus `/content`: authenticated private MP3 or self-contained GLB.

Idempotency belongs to `(organization, kind, client request ID)`. Rotating the
organization service key cannot create another generation or hide old output.
The original key ID is audit metadata. A changed normalized payload conflicts.
Task reads check organization and kind before any provider request. Polling stays
on the original group and supplier account after a key/group change.

Migration 247 extends the quantity constraints and adds durable task metadata,
provider receipts and private bytes in PostgreSQL. Input text, credentials and
signed supplier URLs are not stored. Audio is limited to 16 MiB; GLB to 128 MiB.
The GLB validator checks binary version/length/chunks, embedded buffers/images,
and rejects external URI/URL fields, including in extensions. Downloads allow only
the exact `cdn.tripo3d.ai` and `openapi.cdn.tripo3d.com` hosts, pin DNS-verified public IPs for TLS, reject
redirects, and send no supplier authorization to storage. Any newly observed CDN
host requires official evidence and a reviewed allowlist update.

The second host was verified on 2026-11-07 from the authenticated official task
query for an existing successful standard generation. Its signed `output.model_url`
used `openapi.cdn.tripo3d.com`; rejecting that host left the completed provider
task in `model3d_content_unavailable`. This addition does not permit wildcard
subdomains or redirects. Recover affected tasks by querying their existing task
ID; never submit another generation to retrieve an already completed result.

A persistent quota reservation precedes task dispatch. Concurrent duplicates share
one task. A failure before task insertion can be retried with the same ID and
reuse its reservation; random-ID generation happens before reservation. A task
with uncertain submission never automatically submits again. Execution lost at
restart becomes `indeterminate` after its lease; a known Tripo ID can be polled
using the original account. Unknown results retain reservations. Confirmed
non-billable rejection releases once; a failed Tripo task with positive credits
consumes one standard task. A failed task without a cost receipt remains uncertain.

Terminal state and output commit before quantity settlement. Authenticated GET
reconciles a missed settlement without generating again. Concurrent polls settle
the committed terminal record. HTTP 408/499, transport failures, incomplete audio,
missing terminal SSE success and unsafe/unavailable output retain the reservation.
There is no timeout refund. Explicit operator reconciliation is required for an
unrecoverable speech submission or a task lost before a provider ID was saved.

Speech scene/emotion presets combine into one documented TTS2 `context_texts`
instruction. They do not claim TTS1 numerical emotion control or guarantee voice
quality. Image-to-3D is explicitly unavailable; costly/premium profiles are rejected.

## Isolated test runtime and rollback

No permanent isolated WindHub test runtime was identified in this repository.
`deploy/docker-compose.voice-3d-test.yml` provides an independent local/test-host
stack, without creating cloud resources. It has a distinct Compose project, fresh
PostgreSQL/Redis/data volumes, loopback port 48381, and mandatory new test-only
database/admin/JWT secrets. It never loads the production compose/env file. Pin
the gateway image to a validated, merged-main digest. Record which local and CI
checks actually ran; absent CI runs are not passing checks. The feature stays off.
The example admin email is an identifier, never a credential. Generate a fresh
admin password and other test secrets in a root-readable-only environment file;
do not put their values in logs, screenshots, release receipts or source control.
Generate each value independently with `secrets.token_hex(32)` and run Compose
directly. Do not use deployment wrappers that print credentials; always set the
admin password and JWT secret explicitly to avoid auto-generated secret logging.
Gateway/PostgreSQL/Redis are capped at 512/512/128 MiB, 0.5/0.5/0.25 CPU and
128/128/64 processes, with three 10 MiB log files per service. These are baseline
disabled-feature test limits, not a capacity claim for concurrent 128 MiB assets.

First verify image/source identity, the fresh database, migration 247, absence of
production account data, and authenticated disabled-route behavior. Wire tenant/CRM
test backends to this gateway over the host's authenticated test path. Configure
only synthetic organizations/grants and test credentials. No real paid generation,
new supplier key creation or production mutation is part of this slice's tests.

Before an approved test promotion retain the previous test gateway image digest
and volume identity. Disable the media flag and roll back only the test gateway
image if needed. Keep migration 247 and all task/quota records; do not drop the
database or volumes on application rollback. Pending tasks remain held for explicit
reconciliation. Test-stack disposal is a separate action after evidence retention.

## Verification

Focused protocol tests use in-process HTTP fixtures, not supplier calls. Repository
tests require explicit `MEDIA_QUOTA_TEST_DSN` for a disposable `media_quota_test`
database at 127.0.0.1 and create/drop only their generated schemas. CI provisions a
fresh PostgreSQL service so these tests execute instead of silently skipping.

The tests cover official payload/authentication, TTS2 instructions, partial audio,
HTTP timeout uncertainty, self-contained GLB/URL/IP restrictions, group model and
endpoint authorization, missing credentials, concurrent replays/last allowance,
key rotation, restart/content persistence, wrong organization/kind, failed task
insertion retry and legacy grant digest compatibility. Fixture success does not
prove supplier entitlement, paid generation, voice quality or deployed acceptance.

Official contracts:
- https://docs.volcengine.com/docs/doubao-voice/http-chunked-sse-unidirectional-streaming-v3?lang=zh
- https://docs.volcengine.com/docs/doubao-voice/tone-list-3?lang=zh
- https://developers.tripo3d.com/zh/docs/quick-start
- https://openapi.tripo3d.com/v3
