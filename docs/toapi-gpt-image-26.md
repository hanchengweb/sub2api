# ToAPI GPT Image 2.6 token billing

The public model IDs are `gpt-image-2.6-flare-official` and
`gpt-image-2.6-sunburst-official`. Both use the same five token rates.
Existing GPT Image 2.5 aliases and per-image prices are unchanged.

## Rates and units

WindHub stores credits per token, with 100 credits representing CNY 1.
The managed-model policy is retail CNY = upstream CNY / 0.7, rounded upward
to CNY cents per million tokens. This is a 30% gross margin before existing
customer-specific multipliers, not a 30% markup.

| Token bucket | Upstream CNY / million | Retail CNY / million | Stored credits / token |
| --- | ---: | ---: | ---: |
| Text input | 28 | 40 | 0.004 |
| Cached text input | 7 | 10 | 0.001 |
| Image input | 44.8 | 64 | 0.0064 |
| Cached image input | 11.2 | 16 | 0.0016 |
| Image output | 168 | 240 | 0.024 |

The upstream rates already include the upstream discount. Do not apply that
discount a second time. Set generic output and image output to the same rate
so an omitted output modality split cannot make image output free.

`cache_read_image_tokens` is a subset of the existing cached-input total.
`cache_read_image_cost` is a subset of the existing cache-read cost. Neither
is added a second time. Nullable image-cache pricing preserves legacy models.
An absent usage subtype remains unknown instead of silently becoming zero.

## Generation protocol

Use JSON `POST /v1/images/generations`; reference images use `image_urls`.
The upstream accepts an asynchronous `tsk_img_...` task. The gateway polls
only that original task on the configured HTTPS origin and releases the
image response after completion and validation of its final token usage.
The supplier's procurement billing object is not returned to the customer.

Example parameters: `size: "1:1"`, `resolution: "1k"`, `quality: "low"`,
`n: 1`. This integration supports static PNG, JPEG and WebP. Streaming,
multipart edits, GIF and SVG are rejected for these two IDs; SVG can invoke
an additional text model whose pricing is outside this contract.

An accepted task is never resubmitted or failed over to another account.
Unknown, interrupted or failed polling returns a non-retryable error with
the original task ID. Administrative recovery must query that ID. This
adapter does not provide durable automatic task resumption after restart.

## Verification and rollout

On 2026-11-05, one upstream sample for each model completed with 19 text
input tokens and 824 image output tokens. Each supplier bill settled at
USD 0.019852. The corresponding retail calculation is 19.852 credits;
unit tests cover exclusive token buckets, persistence and one atomic debit.
These samples are upstream checks, not a customer-account gateway debit.

Deploy from merged main, preserving the previous gateway image and compose
file. Migration 250 adds nullable pricing/usage fields and a zero-default
cost field with bounded lock and statement timeouts. Existing database and
Redis containers must retain their identities. Keep official speech/3D
feature flags disabled during this image-only release.

Create the two token price rows before publishing model/account routes.
Wait at least 610 seconds after the confirmed database commit, exceeding
the pricing cache TTL of 600 seconds. Publish only account 23, group 4,
channel 1 and the two exact image routes; verify the scheduler outbox,
Redis mapping and model-plaza prices. Rollback removes only owned new IDs
and restores this release's gateway image; do not restore customer data.

## Current official references

- [Generation protocol](https://docs.toapis.com/docs/cn/api-reference/images/gpt-image-2.6-official/generation)
- [Account pricing metadata](https://docs.toapis.com/docs/cn/api-reference/account/pricing)
- [Upstream price page](https://toapis.com/zh/dashboard/pricing)

The account pricing endpoint was checked live for both IDs, including all
216 published parameter combinations per model. Its USD rates (4, 1,
6.4, 1.6, 24 per million) agree with the CNY rates above. Procurement
credentials, private image URLs and raw supplier bodies are not retained
in this document.
