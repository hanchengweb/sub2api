# Personal collaboration rooms

Personal WindHub accounts can explicitly create cloud rooms, invite other personal
accounts, add multiple named AI agents, exchange messages and files, and manage a
shared project's tasks. This feature does not expose organization users/projects,
desktop workspaces, private drives, private conversations, or private memories.

## Delivery and activation

The implementation starts from fork/main `a9177efe3`. Migration
`246_desktop_collaboration.sql` is additive. The HTTP routes and four bounded
worker pollers are **disabled by default**. Set
`WINDHUB_PERSONAL_COLLABORATION_ENABLED=1` only in an explicitly verified deployment
after the migration, gateway model policy/billing, and two-account tests pass.
Disabled routes return HTTP 503 with `COLLABORATION_UNAVAILABLE`.
The server also keeps the feature disabled if its schema readiness check fails.

The owning release task records the verified existing image and rollback target
outside this public source document. Deployment identity and acceptance must be
read back from the target environment before enabling the feature.

No production deployment, real accounts, real model calls, or balance changes
were performed for this work. A synthetic gateway is not real model/billing
acceptance. Roll back by disabling the switch or restoring the previous server
image; preserve the additive tables and their data. Do not drop the tables as an
automatic rollback action. Any interrupted running jobs become `outcome_unknown`
after six minutes when a worker next polls; they are never silently requeued.

## Identity, execution, and durability

- Every route uses the existing API-key authentication middleware and the personal
  account/group boundary already used by personal drive. Disabled users/keys,
  expired keys, organization/service identities, quota keys and restricted groups
  remain rejected. Exact registered collaboration routes bypass model-balance
  enforcement so human messages remain usable with zero balance.
- The sender's authenticated user ID and API-key ID are stored with each AI run.
  The client cannot specify a payer. The worker reloads the key by ID, checks its
  owner and validity, and rechecks membership and agent activation.
- AI execution creates a fresh internal Gin request to the fixed
  `/v1/chat/completions` route with that key and request ID `collab:<run UUID>`.
  Full gateway authentication, model authorization, concurrency and billing still
  apply. The internal request does not inherit cookies or identity headers. Keys
  with IP restrictions that reject this server-internal request fail closed.
- Human message insertion, stable per-room sequence, structured mentions, one
  run per mentioned agent, and idempotency result all commit in one transaction.
  A plain-text `@name`, including one in an AI response, schedules nothing.
- PostgreSQL room locks serialize membership checks and mutations. Per-user
  transaction locks plus request fingerprints prevent concurrent request replay
  from creating duplicate messages/runs. Replays recheck current membership.
- Four fixed pollers claim queued rows with `FOR UPDATE SKIP LOCKED`. Each run
  has a four-minute request timeout. Gateway timeout, interrupted execution,
  unusable results, or ambiguous server errors are not automatically retried.
  This does **not** assert upstream exactly-once execution.
- AI reply and terminal status commit together. A revoked member or removed agent
  is checked again before publication. Output stopped by `finish_reason=length`
  remains visible with an explicit incomplete notice and `RESPONSE_TRUNCATED`.
- Model choices conservatively include known text-chat families and exclude
  image/video/audio/embedding families because the existing `/v1/models` catalog
  lacks a universal endpoint-capability field. This guard is not a substitute for
  the requester's current gateway model permissions.

## HTTP contract

All JSON operations return the existing `{code:0,message:"success",data:...}`
envelope, including DELETE (`data:{}`). IDs are UUID strings, while user IDs are
decimal strings. Every write needs a UUID `clientRequestId`; DELETE may use the
`Idempotency-Key` header instead. Reusing one ID with changed input returns 409.

Base path: `/v1/desktop/collaboration`.

| Method and suffix | Payload/result |
| --- | --- |
| GET `/status` | `{user:{id,name},models:[{id,name}],billing:'requester',cloudExecution:true}` |
| GET `/rooms` | `{items:[room]}` |
| POST `/rooms` | `{name,projectName?,clientRequestId}` → room |
| GET `/rooms/:room_id` | `{room,members,agents,project?}` |
| POST `/rooms/:room_id/invites` | `{clientRequestId}` → `{id,token,expiresAt}` |
| DELETE `/rooms/:room_id/invites/:item_id` | Revoke invite (owner only) |
| POST `/join` | `{token,clientRequestId}` → room; requires explicit recipient action |
| DELETE `/rooms/:room_id/members/:item_id` | Remove member (owner only); cannot remove owner |
| POST `/rooms/:room_id/agents` | `{name,instructions,model,clientRequestId}` → agent (owner only) |
| DELETE `/rooms/:room_id/agents/:item_id` | Deactivate agent (owner only) |
| GET `/rooms/:room_id/messages?after=0&limit=100` | `{items,hasMore,cursor}`; ascending sequence |
| POST `/rooms/:room_id/messages` | `{text,mentionIds?,attachmentIds?,clientRequestId}` → `{message,runs}` |
| GET `/rooms/:room_id/runs` | `{items:[run]}` |
| GET `/rooms/:room_id/tasks` | `{items:[task]}` |
| POST `/rooms/:room_id/tasks` | `{title,status?,assigneeId?,clientRequestId}` → task |
| PATCH `/rooms/:room_id/tasks/:item_id` | `{title?,status?,assigneeId?,revision,clientRequestId}` → task; stale revision returns 409 |
| POST `/rooms/:room_id/assets` | Multipart `file` and `clientRequestId` → asset directly |
| GET `/rooms/:room_id/assets` | `{items:[asset]}` |
| GET `/rooms/:room_id/assets/:item_id/content` | Authorized attachment download, `no-store`, `nosniff`, sandbox CSP |

Invites use 256-bit random tokens, expire in 24 hours, and persist **only a token
hash**. First response displays the token; replay returns its metadata with an
empty `token` and `tokenAlreadyIssued:true`. A caller that did not retain the
first response must explicitly create a fresh invite. Tokens are never emailed
automatically. Removing a member revokes outstanding room invites, preventing
immediate re-entry via an old link. An owner can issue a new invite afterward.

`projectName` creates a new shared project scoped exactly to this room; it is not
a local path or a reference to an organization project. Tasks require that
project. `assigneeId:""` unassigns a task. Removing a member unassigns their tasks
and increments the affected task revisions.

## Limits and shared files

Rooms: 100 owned per account; 100 members; 12 active agents; 20 active invitations.
Messages: 12,000 characters; at most 6 structured mentions and 8 attachments; at
most 12 queued/running AI jobs per requester. Initial runs emit no tools and use
at most 2,048 output tokens. Context includes at most 60 messages / 24,000
characters from this room, ending at the triggering message, plus agent role
instructions. Later messages and other rooms never enter that run.
Message history pages stop at the requested item limit or about 1 MiB of actual
JSON-encoded items (including HTML escapes and attachment metadata). `hasMore`
and `cursor` preserve complete ordered catch-up below the desktop 2 MiB bound.

Files are explicit room uploads, stored in PostgreSQL bytea with metadata and a
SHA-256 hash. Limits: 10 MiB per file, 100 MiB / 1,000 files per room. The server
never resolves a client filename as a path. Downloads recheck room membership;
neither a public URL nor a private-drive reference is created.

Only `.txt`, `.md`, `.csv`, `.json`, and `.log` files that validate as UTF-8 and
contain no NUL are extracted. Only attachments to the current question enter its
AI context, as **user data**, with a combined 8,000-character budget. Every file
still contributes its name and reading status if unsupported, truncated, or
omitted due to the text budget. `message.attachments[].contextNote` contains this
same explicit status; it is persisted with the message so polling does not
repeatedly load binary content. PDF, Office, image and other binary files are
shareable/downloadable but not interpreted by this first version.

## Local verification

Use the project's Go 1.26.5 runtime. Commands run from `backend`:

```powershell
$env:GOTOOLCHAIN='local'
go test ./internal/collaboration -count=1
go test -tags unit ./internal/server/middleware -run TestDesktopCollaboration -count=1
go test ./internal/server ./internal/server/routes -run '^$' -count=1
```

PostgreSQL integration is explicit opt-in. It connects **only** to
`127.0.0.1:55479`, user `collaboration_test`, never reads an application DSN,
creates a random disposable database, applies the complete migration history,
and drops only that fixture database afterward:

```powershell
$env:WINDHUB_COLLABORATION_PG_TEST='1'
go test ./internal/collaboration -count=1 -v
```

An additional opt-in cross-runtime contract test executes the actual desktop
`collaboration.cjs` client in a Node child process against the real Gin handlers,
store and disposable PostgreSQL database. Set
`WXM_DESKTOP_COLLABORATION_CLIENT` to that source file's absolute path, then run:

```powershell
go test ./internal/collaboration -run '^TestDesktopBridgeContract$' -count=1 -v
```

Both the desktop source opt-in and the PostgreSQL opt-in are required; otherwise
the test skips. Its fetch adapter accepts only the fixed production-origin route
shape and remaps it exclusively to an ephemeral loopback server. Authentication
and upstream model responses are synthetic. The two-account test covers the
actual client serialization, envelopes, task PATCH request ID and CAS, mentions,
request replay, file upload/download, removal and invite revocation. It must not
be reported as real provider billing or production acceptance.

Verified fixtures cover outsider/cross-room access rejection, invite secrecy,
member removal, same-name agents by ID, requester key identity, future-context
exclusion, AI non-recursion, simultaneous idempotent sends, durable completion,
crash/timeout unknown state, truncated answers, four-worker parallel progress,
agent removal during execution, task CAS, HTTP envelopes, multipart upload,
authenticated file download, extraction boundaries, and zero-balance human
access versus full model-billing rejection. Real multi-device delivery, physical
desktop closure, real gateway model responses and actual ledger attribution
remain production acceptance gates.
