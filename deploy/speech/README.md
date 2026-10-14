# Private speech adapter and application API

The existing cloud host runs a small online speech adapter. No GPU, downloaded
model weights or new paid API key is needed. It uses the existing Edge online
speech path with `edge-tts==7.3.1`; the old 7.2.8 connection returned HTTP 403.
This is an online dependency, not offline inference or a guaranteed free SLA.
The client library is LGPL-3.0; retain its installed license notices.

## Boundaries

- WindHub owns account authentication and free-use limits. The private adapter
  never receives account keys, tenant tokens or user identifiers.
- No public worker port. `WXM_SPEECH_URL` is server configuration. Attach only
  the gateway to the internal speech network; a separate egress network lets
  the worker contact the online speech service over TLS.
- Personal desktops use their existing WindHub connection. Organization
  desktops use their own tenant gateway and its enabled WindHub connection.
- Other apps call WindHub from their backend with their own account key. Do
  not put a shared credential into a website or mobile bundle.
- Text and audio remain in memory in this adapter, and text is sent to the
  online speech provider. Access logs are disabled, responses are `no-store`,
  and client errors omit engine names, model paths and upstream details.

## Public contract

`GET /v1/audio/voices` returns `warm` (温柔女声) and `bright` (清朗男声).
`POST /v1/audio/speech` accepts 1–200 Unicode characters, a voice ID, and
`response_format: "mp3"`. Optional `model` accepts only `speech-v1`.
The response is `audio/mpeg`, at most 4 MiB. The adapter collects each short
sentence before returning it; this endpoint does not claim streaming playback.

```javascript
// Application backend; this app owns SPEECH_API_KEY.
const response = await fetch('https://windhub.online/v1/audio/speech', {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.SPEECH_API_KEY}`,
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({ input: '你好，欢迎来到微末智学。', voice: 'warm', response_format: 'mp3' }),
  signal: request.signal,
});
// Forward the bounded audio response; propagate cancellation and handle errors.
```

Limits: 10,000 input characters per WindHub account per UTC day, 20 attempts
before a 60-second idle reset, 200 characters/request, and two simultaneous
worker requests. Additional keys do not reset account limits. Members using
one organization connection share that connection's budget. Attempts reserve
budget even if synthesis fails or is busy. Speech does not debit model balance;
expired keys, disabled accounts and network restrictions still apply. Redis
failure blocks synthesis.

## Deployment and rollback

Build only merged main, using `docker build -t wxm-speech:<source-sha> deploy/speech`.
`compose.yaml` defines an unprivileged, read-only, 0.5-CPU / 256-MiB service,
no published port, bounded logs, and independent internal/egress networks.
It needs no volume or model download. Preserve the gateway's existing networks
when attaching the private speech network and setting
`WXM_SPEECH_URL=http://speech:8093` in its production Compose definition.

Unset `WXM_SPEECH_URL` leaves the routes unavailable. Keep the previous gateway
image and Compose backup. Rollback restores that definition/image and the prior
desktop release; no account schema or data is changed.

## Verification

- `python -m unittest -v test_server` checks input/audio limits, sanitized
  failures, deadline, two-request capacity and actual HTTP disconnect handling.
- `go test -tags=unit ./internal/handler ./internal/server/middleware ./internal/server/routes`
  covers gateway identity, budget and route regressions.
- Live acceptance requires both voices, normal paragraph latency, cancellation,
  authenticated personal/organization requests and original Bot playback.
- `/healthz` proves process readiness only, not upstream speech availability.
- An isolated cloud trial returned the first Xiaoxiao audio chunk in 0.929 s
  and the complete 40,608-byte MP3 in 1.409 s. This direct-adapter measurement
  is not an end-to-end desktop latency claim.

Source: [edge-tts](https://github.com/rany2/edge-tts).
