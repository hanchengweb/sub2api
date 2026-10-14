# Digital teachers (Simli or LiveAvatar FULL)

## Simli rollout

Set `DIGITAL_TEACHER_PROVIDER=simli` and `SIMLI_API_KEY_FILE=/app/data/simli-api-key`
using a server-only 0600 secret file. Keep the old LiveAvatar credential for
reconciling old sessions. Migration 240 adds provider identity without rewriting
old sessions. All existing account/group gates and bounded minute grants remain.

The desktop receives a WindHub capability with
`apiUrl=wss://windhub.online/v1/digital-teachers/sdk`. The Simli SDK connects to
`/compose/webrtc/livekit?session_token=...`; WindHub atomically permits one
upstream WebSocket, relays PCM/signaling, and closes it on stop, revoked access
or deadline. The supplier token stays encrypted in WindHub. LiveKit room media
travels directly to the desktop. Install `nginx-digital-teacher-simli.conf` in the
WindHub TLS server to suppress capability query strings from access logs.

Each supplier token uses the WindHub session UUID as `sessionAggregator`.
Exactly one complete matching history record, observed twice, settles minutes
as `sessionTotalTime / 60`. Missing/ambiguous histories retain the hold. Simli
history is usage evidence, not a supplier dollar invoice; `provider_credits`
stays null. Simli has no free sandbox path. Model questions use the existing
personal model route and its separate accounting. Chinese TTS/ASR run locally.

Existing teachers retain their lesson settings and use the public Tina face
until saved or assigned a Simli face. LiveAvatar likeness IDs are not portable.
Create an authorized face in Simli and bind it to the selected account; only
that owner's assignments appear. This edition uses a local Chinese voice.

WindHub owns the provider credential, account-scoped teacher configuration,
short-lived SDK capabilities, minute grants, reservations and settlement.
The desktop uses its existing personal WindHub API key from the main process.
Organization service accounts require explicit minutes, but the tenant platform
desktop organization bridge is not part of this release.

## Configuration and rollout

- Mount a server-only secret file and set `LIVEAVATAR_API_KEY_FILE` to its
  container path. `LIVEAVATAR_API_KEY` is a fallback. Never commit either value.
- The existing stable `JWT_SECRET` must contain at least 32 bytes; domain-separated
  keys protect supplier session tokens and sign SDK capabilities. Do not rotate
  it while unsettled sessions exist.
- Migration `239_digital_teacher_sessions.sql` only adds tables and indexes.
  Existing model routes are unchanged. Plans default to disabled.
- `/admin/digital-teachers` configures the group plan, grants minutes, assigns
  private avatars/voices and stops/reconciles pending sessions. Admin auth,
  compliance checks and audit middleware remain active.
- Start with an explicitly selected account, price 0, a small minute grant and
  a one-minute session maximum. Zero-price FULL and sandbox access require an
  explicit grant; an enabled group alone does not open free supplier access.
- Grant request IDs are idempotent; reuse the original ID after an uncertain
  response. Plan updates require the current version.
- For rollback, retain the previous Compose and image. Disable the plan and
  reconcile active sessions before reverting the image. Keep the additive tables
  and receipts; dropping them destroys the accounting recovery trail.

## Owner and SDK contracts

Owner routes accept the existing WindHub key and return the usual `code: 0`
envelope under `/v1/digital-teachers`:

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/settings` | Provider availability, plan, minutes and saved teachers |
| GET | `/catalog` | Public assets plus only this account's assigned private assets |
| PUT | `/teachers` | Save a teacher using authorized avatar and voice IDs |
| POST | `/sessions` | Reserve the quote and create an idempotent session ticket |
| GET | `/sessions` | This API key's recent sessions and pending recovery |
| GET | `/sessions/:id` | Account/key-bound state and usage |
| POST | `/sessions/:id/stop` | Stop and reconcile; pending settlement is explicit |

The SDK 0.0.19 receives a WindHub capability and
`apiUrl=https://windhub.online/v1/digital-teachers/sdk`. Only
`POST /v1/sessions/start`, `/stop` and `/keep-alive` exist under that URL, using
the supplier-compatible `code: 1000` envelope. Supplier session JWTs remain
encrypted on the server; only the client room token reaches the SDK. Start is
dispatched at most once. Exactly these SDK paths accept the desktop's null
origin; owner/admin endpoints do not.

## Accounting and recovery

- One unsettled session per WindHub user. Identity, group membership,
  subscription, expiry and API key limits are rechecked at reservation/start.
- Before FULL starts, reserve the maximum quote from balance/frozen balance,
  API key quota/rate windows and, when configured, account minute grants.
- FULL minutes = supplier `credits_consumed / 2`. Minutes use six decimals;
  account money uses eight decimals. Sandbox must report zero supplier credits.
- Charge no more than the reservation. Supplier overruns stay on the receipt
  and are marked for review; WindHub absorbs the excess.
- Two matching terminal supplier receipts at least five seconds apart, with an
  end time at least 15 seconds old, trigger one atomic settlement/refund. This
  is WindHub's settlement policy, not a supplier financial-finality guarantee.
- A 10-second worker retries unresolved sessions and enforces local deadlines.
  Browser disconnects/timeouts cannot manufacture a refund. Never dispatch a
  second start after an unknown first result.
- `/admin/digital-teachers/sessions/:id/reconcile` requests stop/reconciliation.
  Keep unknown receipts pending; do not manually erase holds merely because the
  user closed the page.
- Session/grant tables retain the audit trail. This release does not integrate
  digital-teacher usage into generic model token usage or the tenant ledger.

## Custom teachers

Use the LiveAvatar console to create a consented teacher avatar from supported
photo/video material, then bind its ID to the account in WindHub. Custom voice
IDs can be bound separately. This release does not purchase avatar slots or
claim an in-app avatar-training API. Public avatars allow immediate testing;
teacher name, subject, teaching prompt, opening and voice are configurable.

## Validation

Focused Go tests include the entire SQL migration chain on an isolated loopback
PostgreSQL database, concurrency, pre-dispatch failures, unknown starts, grants,
account isolation, rate-window refunds, credential protection and exact CORS.
Set `WINDHUB_DIGITAL_TEACHER_TEST=1` only for the dedicated test cluster on 55439.
The real-supplier opt-in test is `TestLiveAvatarSandbox`; its room start/stop and
settlement do not by themselves verify WebRTC picture, sound or microphone.
Frontend checks use `pnpm --dir frontend run build` and scoped browser checks.
