# Private speech worker and application API

Status: candidate implementation, not a production release. The existing ECS
has no GPU. A bounded CPU benchmark generated real Chinese audio but needed
16.27 seconds for 2.16 seconds of speech, and 26.41 seconds for 3.60 seconds of
speech. This is not accepted for interactive Bot use. Do not enable the desktop
change until a suitable inference deployment passes the checks below.

A bounded dynamic-INT8 trial also failed acceptance: the same two inputs took
58.40 and 56.38 seconds and both reached the 160-token limit (12.72-second
outputs). This optimization is not used in the worker.

## Boundaries

- WindHub owns authentication and the free-use budget. The worker never receives
  account keys, tenant tokens or user identifiers.
- The inference worker has no public port. `WXM_SPEECH_URL` is server configuration,
  never a client field. Keep it on a private Docker network with the gateway.
- Personal clients use their existing WindHub connection. Organization desktops
  use their own tenant gateway and its enabled, allowlisted WindHub connection.
- Other apps call WindHub from their backend with their own account key; do not
  put a shared key in a website or mobile bundle.
- Text and output audio are held in memory. Access logs are disabled in the
  worker; responses use `Cache-Control: no-store`. Client-facing errors do not
  contain engine names, stack traces, model paths or provider URLs.

## Public contract

`GET /v1/audio/voices` returns neutral IDs `warm` and `bright` when the worker is
ready. `POST /v1/audio/speech` accepts 1–200 Unicode characters, a voice ID and
`response_format: "wav"`. Optional compatibility field `model` accepts only
`speech-v1`. Output is PCM WAV, at most 4 MiB. No streaming claim is made.

```javascript
// Run in an application's backend. SPEECH_API_KEY is that app's own key.
const response = await fetch('https://windhub.online/v1/audio/speech', {
  method: 'POST',
  headers: {
    Authorization: `Bearer ${process.env.SPEECH_API_KEY}`,
    'Content-Type': 'application/json',
  },
  body: JSON.stringify({ input: '你好，欢迎来到微末智学。', voice: 'warm', response_format: 'wav' }),
  signal: request.signal,
});
// Forward the bounded audio body or show a product error. Do not blindly retry.
```

Limits are 10,000 input characters per WindHub account per UTC day, 20 attempts
per rate window, 200 characters per request and one inference at a time. A new
API key does not reset the same account's budget. Organization members using
one organization connection share its budget. Attempts reserve budget even if
the worker subsequently reports busy or fails. These limits do not charge the
model balance. Expired keys, disabled accounts and network restrictions still
apply; Redis failure blocks synthesis.

## Reproduce the candidate

Qwen3-TTS code and selected model are Apache-2.0 licensed. `model-lock.json`
pins the official 0.6B CustomVoice revision and hashes of all inference inputs.
The private mapping is Serena → warm and Vivian → bright.

```sh
python download-model.py --mirror modelscope
python download-model.py --verify-only
docker build -t wxm-speech:<source-commit> .
SPEECH_IMAGE=wxm-speech:<source-commit> docker compose config --quiet
```

`Dockerfile` and `compose.yaml` are CPU candidates with a 2-vCPU/6-GiB cap, not
a verified GPU release. The model loads in offline mode with read-only weights;
the runtime container is unprivileged. Do not start the compose candidate as a
production voice service on the current shared host. A GPU release needs a
verified CUDA build, measured readiness and the separately authorized resource.

`WXM_SPEECH_URL` unset leaves the public gateway route unavailable. Deploy from
merged main only, retain the preceding gateway image/configuration, then enable
the private worker URL after readiness. Rollback removes the URL and restores
the prior gateway and desktop release; it does not change account data.

## Verification

- `python -m unittest -v test_server`: request validation, audio response,
  capacity limit, sanitized errors and actual HTTP disconnect cancellation.
- The pinned `qwen-tts` 0.1.1 wrapper drops `stopping_criteria` before calling
  `talker.generate`. The worker therefore uses a request-scoped PyTorch forward
  hook for cancellation/deadline enforcement and removes it in `finally`.
  Validate this against the real model when updating the dependency; a mocked
  route-disconnect test alone does not cover this integration behavior.
- `go test -tags=unit ./internal/handler ./internal/server/middleware ./internal/server/routes`:
  gateway integration and identity/budget regressions.
- Deployment acceptance still requires real cloud synthesis through personal
  and organization accounts, representative paragraph latency, both voices,
  actual cancellation, quota checks, and a separate app-backend call.
- Local fixture playback and a private CPU sample are not public deployment
  acceptance. Keep engine identity and credentials out of rendered UI.

Sources: [official code](https://github.com/QwenLM/Qwen3-TTS),
[pinned model](https://huggingface.co/Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice/tree/85e237c12c027371202489a0ec509ded67b5e4b5).
