# Private voice assets (test candidate)

The trusted Tenant service authenticates with its existing organization service
API key and supplies `X-WindHub-Actor`, a stable opaque UUID derived server-side
from the authenticated tenant and member. Renderer headers and provider speaker
IDs are not trusted identities. Assets and new media task status/content are
scoped to organization plus actor. An omitted actor preserves only legacy tasks;
it cannot read actor-scoped tasks or manage assets.

Endpoints under `/v1`:

- `GET /voice-assets/catalog`, `GET /voice-assets`, `GET /voice-assets/:id`.
- `POST /voice-assets`: `{kind:"official",name,voice}`; `voice` is one of the
  473 reviewed official presets. The created UUID pins its provider account.
- `PATCH /voice-assets/:id`: `{name?,enabled?}`; `DELETE` returns `{deleted:true}`.
- `POST /voice-assets/clone`: multipart `name`, `audio`, optional `demo_text`,
  and `consent=true`, plus a stable UUID `X-Client-Request-ID`.
- `POST /voice-assets/:id/refresh`: query only the original allocated speaker.

Public assets have `id`, `kind`, `name`, `model`, `enabled`, `status`,
`can_synthesize`, timestamps and an optional safe error. Official assets include
`source_voice`; cloned assets never expose their speaker/account/key or preview
URL. Status is `ready`, `training`, `failed`, or `indeterminate`.

`POST /audio/generations` accepts `voice_asset_id` instead of `voice`. The caller
must explicitly select the asset's model: `seed-tts-2.0` for official assets or
`seed-icl-2.0` for clones. Group model publication and exact account mapping are
required separately for both models. The private clone speaker is resolved only
after ownership, enabled/ready state and current model authorization pass. Both
models retain the existing audio-character reservation and settlement; they do
not consume a second token balance. Request IDs are independently scoped by
actor, including quota claims.

Cloning requires an administrator to configure **already prepaid, untrained**
`S_...` IDs in the selected official Speech account's
`extra.voice_clone_speaker_ids` array. Its existing provider must be
`volcengine_speech` at `https://openspeech.bytedance.com`, with an exact
`seed-icl-2.0` mapping. An empty/exhausted pool fails closed. No provider purchase,
postpaid `custom_speaker_id`, automatic training retry, or slot reuse is supported.
The slot claim and attempt are durable before provider calls. A read-only
`get_voice` check must first confirm status 0; already trained or unverified slots
cannot be overwritten or adopted as a new user's voice. A crash during this
check stays blocked. Deletion disables the local asset and retains its slot.

Samples are limited to 8 MiB and WAV/MP3/M4A, actually decoded by the runtime
`ffmpeg` dependency through pipes with only the `pipe` protocol enabled. Two
concurrent decoders, 10-second execution timeout, bounded PCM output and 1–60
second duration protect resources. The supplier receives a fresh mono 16 kHz
PCM WAV; the original sample never goes to a temporary file, task table or log.
Missing decoder or invalid audio fails before training. UI guidance is 10–30s.

Official reference endpoints:

- [Voice training](https://docs.volcengine.com/docs/doubao-voice/tone-training-http?lang=zh)
- [Voice query](https://docs.volcengine.com/docs/doubao-voice/tone-query-http?lang=zh)
- [Speech SSE](https://docs.volcengine.com/docs/doubao-voice/http-chunked-sse-unidirectional-streaming-v3?lang=zh)

Clone synthesis uses resource `seed-icl-2.0` and
`req_params.model=seed-tts-2.0-expressive`; scene/emotion instructions remain a
JSON-string `additions.context_texts`. Training and synthesis use the same pinned
account. Provider query status 2/4 is ready only with `model_type=5`.

Validation uses fake providers for training/query/synthesis and real local
in-memory WAV/MP3/M4A decoding. PostgreSQL concurrency/isolation tests require the
explicit disposable `MEDIA_QUOTA_TEST_DSN` (127.0.0.1, database
`media_quota_test`); each test creates/removes a fresh schema. These tests must
not use the application database. No real clone is accepted without a user sample
and a verified prepaid slot. This document does not authorize production release.
