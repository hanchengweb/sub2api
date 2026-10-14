# Digital teacher photos and natural voices

The authenticated digital teacher gateway offers 12 adult presets and 6 Mandarin
voices. Existing Simli `windows-zh-cn` settings read as `warm`; teacher identity,
subject, instructions and avatar remain intact. Speech uses the existing private
online speech adapter and its per-user character/rate limits, without Windows TTS
fallback. This is not voice cloning or an independent speech SLA.

`POST /v1/digital-teachers/avatars` accepts multipart `image`, `name`, UUID
`request_id` and `consent=true`. JPG/PNG/WebP must be under 5 MiB, at least 512x512,
at most 8192 per edge and 24 million pixels. Only an enabled account can create;
free plans also require the explicit minute grant. Limits are 3 attempts/24h,
30 retained jobs/account and one known in-flight generation across this gateway.
No subscription, face-slot purchase or provider billing setting is changed.

Migration 241 stores owner, request digest, state and a private <=256px thumbnail.
Original uploads are forwarded to Simli `/faces/trinity?gsVersion=GSA_1.0` with a
unique `wxm-<job-id>` provider name; original bytes are not persisted by WindHub.
Provider credentials never reach the renderer. GET `/avatars` lists only the
caller’s jobs; GET `/avatars/:id` reconciles only that owner's exact face ID.
Completed jobs bind themselves to the owner and appear in `/catalog`.

Retries of the same request reuse the existing job, including after timeout.
Unknown create outcomes never repeat the upstream POST; reconciliation can only
adopt a face whose name exactly matches this job. Unknown outcomes require review
if the supplier does not expose that name. Status queries match `generations[]`
by face ID, rather than adopting the account's latest generation. Background
reconciliation continues after the desktop closes. Failed jobs show an explicit
reason. Slot/plan refusals are not reported as completed avatars.

Official contract checked 2026-10-14: https://api.simli.ai/openapi.yaml and
https://docs.simli.com/api-reference/preset-faces . Compose `apiVersion=v2` has no
resolution option. A generated Trinity face must be tested in real media before
claiming improved picture quality. Creation response schema is unspecified;
only a validated `face_id` or `id` is accepted, otherwise outcome stays unknown.

Validation: dedicated loopback PostgreSQL 55439 tests admission, consent, invalid
images, replay/digest conflict, owner isolation, foreign latest-status rejection,
automatic assignment and no retry of unknown mutations. Speech handler and worker
tests remain the baseline for the shared audio API. Do not run live photo/media
tests without an authorized image and bounded account use.
