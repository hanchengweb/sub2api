# Simli official full conversation evaluation

The allowlisted desktop uses Simli `/auto/start/configurable`, Daily media and
RTVI 1.4, with provider-managed ElevenLabs speech and LLM. No separate ElevenLabs
secret is required by the verified Aria / multilingual v2 / GPT-4o mini session.
The catalog includes 21 official presets, account-bound faces and the current
public ElevenLabs voices. Public previews are English samples, not Mandarin tests.

Set `DIGITAL_TEACHER_SIMLI_STUDIO_USERS` to explicitly authorized user IDs only.
Existing enabled group, active personal key, group access and an explicit teacher
grant are still required. The evaluation is bounded to 60s/session, 5 starts/24h
and one unclosed session/account. It does not debit balances or minute grants.
Migration 242 adds a separate evaluation table, encrypted Daily room storage and
the avatar generation discriminator. Stop clears the stored room immediately;
closed records are never replayable. An unknown create is never resubmitted.

The Auto session ID cannot be exactly correlated with the observed supplier
history (which reports shared `primary` / `gsavatars` values). Transcript lookup
does not supply usage. Do not guess timestamps or reuse Compose settlement.
Closing an evaluation record after its conservative deadline is admission cleanup,
not a supplier stop receipt or bill. The renderer sends RTVI `disconnect-bot`;
the supplier's requested 60s maximum also bounds a crashed client. Commercial
Auto billing remains disabled until an exact receipt contract is available.

`generation=legacy` uploads to `/faces/legacy?characterVersion=1.5`; Trinity stays
the default for older clients. Status reconciliation retains owner and face-ID
matching. Legacy is documented but deprecated. Simli also says Auto development
has stopped. This route is an explicit effect evaluation, not a new SLA. No plan
purchase, quota upgrade or overage switch is performed.

Live Legacy verification accepted a 304 KB JPEG at the same 1254x1254 dimensions
after rejecting a 2 MB PNG with HTTP 400 `error parsing the body`. The gateway
therefore encodes validated Legacy photos as JPEG, preserving dimensions and
trying quality 95 then 85 under a conservative 900 KiB transport budget. This
budget is an interoperability choice, not a claimed documented supplier limit.
Oversized results stop before submission with a clear recovery message.
Legacy returns `character_uid`, which is its face ID; status polling uses that
exact ID. The verified original fictional teacher is processing. Generation
completion and its actual video appearance remain unverified. No duplicate
generation was submitted after the successful response.

Deploy from merged `fork/main`: build the existing embedded frontend, cross-build
the gateway, and replace only the gateway image. Preserve speech, PostgreSQL,
Redis and nginx. Back up compose and the database; verify the prior image and no
active teacher session first. Rollback restores compose and the previous image;
migration 242 is additive and compatible with the previous binary.

Validation: isolated PostgreSQL gateway tests cover grants, active-key replay,
cross-owner access, daily limits, encryption, no money/minute debit and timeout
recovery. Desktop tests cover route selection, no local speech dependency, unknown
results, cancellation and room restrictions. Real media must additionally verify
the product CSP, Chinese voice, RTVI input, interruption and disconnect. A fake
microphone or public avatar is not acceptance of physical input or custom likeness.

Official contracts checked 2026-10-14: https://api.simli.ai/openapi.yaml,
https://docs.simli.com/api-reference/preset-faces and
https://docs.pipecat.ai/client/rtvi-standard .
