# CompShare MiniMax H3 video

The production `MiniMax-H3` video model uses the dedicated model API at `https://cp.compshare.cn`, not the CompShare cloud-resource API or the historical ToAPI protocol. Select it with a Grok API-key account whose base URL is exactly that HTTPS origin. Keep only its exact model mapping on the dedicated account and remove that one mapping from the historical ToAPI account.

Official contract: <https://www.compshare.cn/docs/minimax-h3/api/minimax-h3-video-api>.
Official supplier billing: <https://www.compshare.cn/docs/minimax-h3/billing>.

Generation uses `POST /v1/videos/generations`; task status and authenticated content use the existing video task endpoints. The adapter translates to the supplier's `/minimax/v2/video_generation` and `/minimax/v2/query/video_generation/{id}` API. Provider credentials and signed result URLs stay on the server.

Supported output tiers are 480p, 768p, 1080p, 2K and 4K; duration is any integer from 4 to 30 seconds, default 5. The adapter sends the supplier's exact uppercase resolution values. Aspect ratios are adaptive, 21:9, 16:9, 4:3, 1:1, 3:4 and 9:16. Image references and HTTPS audio references have no supplier surcharge. Paid prompt optimization, Skills, video references and callbacks are rejected until their complete contracts are implemented.

Configure all five `groups.video_model_prices['MiniMax-H3']` prices before enabling the new account. Prices are WindHub credits per output second. Convert the actual paid supplier-package cost into CNY before applying the existing margin and credit-conversion policy; supplier points are not WindHub credits.

H3 uses the existing balance and usage-billing system. It does not consume or require an organization media-quota grant. Other models retain their existing organization quota rules.

The request reservation stores its owner, API key, account, payload identity and price snapshot with the unified debit. A caller-supplied `Idempotency-Key` identifies one request. A repeated key with different content is rejected; an uncertain submission is never automatically recreated. Preserve the returned original request/task identity for recovery.

Terminal settlement uses delivered resolution and output seconds. A high-resolution request delivered as 768p receives one difference refund; failed, cancelled or blocked tasks receive one full refund. The authenticated content proxy accepts only the verified supplier result hostname and disables redirects. Additive settlement schema can remain unused after an application rollback; retain the previous image and H3 routing/price snapshot for rollback.

As with existing video refunds, an independently configured `user_platform_quotas` Grok spending limit conservatively retains gross reserved usage after a refund. Balance, API-key quota, account quota and usage-log amounts settle to the net delivered price. No media-quota grant is created or increased.
