-- 按供应商建 toAPI 账号，形态与既有账号 3/4/5/6 完全一致：
-- 同一个上游 toapis.cn/v1、同一把密钥，各自独立并发池与优先级。
-- api_key 从账号 3 用子查询复制，全程不落到脚本里，也不打印。
-- 幂等：按 name 判重，可重复执行。
BEGIN;

-- ToAPI Black Forest Labs（4 个模型）
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI Black Forest Labs', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         'model_mapping', '{"flux-2-flex": "flux-2-flex", "flux-2-pro": "flux-2-pro", "flux-kontext-max": "flux-kontext-max", "flux-kontext-pro": "flux-kontext-pro"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 6, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI Black Forest Labs' AND deleted_at IS NULL);
INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 6, NOW() FROM accounts a
WHERE a.name = 'ToAPI Black Forest Labs' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

-- ToAPI Google Gemini（7 个模型）
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI Google Gemini', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         'model_mapping', '{"gemini-2.5-flash-image-official": "gemini-2.5-flash-image-official", "gemini-2.5-flash-image-preview": "gemini-2.5-flash-image-preview", "gemini-3-pro-image-official": "gemini-3-pro-image-official", "gemini-3-pro-image-preview": "gemini-3-pro-image-preview", "gemini-3.1-flash-image-official": "gemini-3.1-flash-image-official", "gemini-3.1-flash-image-preview": "gemini-3.1-flash-image-preview", "gemini-3.1-flash-lite-image-official": "gemini-3.1-flash-lite-image-official"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 7, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI Google Gemini' AND deleted_at IS NULL);
INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 7, NOW() FROM accounts a
WHERE a.name = 'ToAPI Google Gemini' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

-- ToAPI Google Nano Banana（3 个模型）
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI Google Nano Banana', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         'model_mapping', '{"nano_banana": "nano_banana", "nano_banana_2": "nano_banana_2", "nano_banana_pro": "nano_banana_pro"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 8, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI Google Nano Banana' AND deleted_at IS NULL);
INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 8, NOW() FROM accounts a
WHERE a.name = 'ToAPI Google Nano Banana' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

-- ToAPI Doubao Seedream（4 个模型）
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI Doubao Seedream', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         'model_mapping', '{"doubao-seedream-4-0": "doubao-seedream-4-0", "doubao-seedream-4-5": "doubao-seedream-4-5", "doubao-seedream-5-0": "doubao-seedream-5-0", "doubao-seedream-5-0-pro": "doubao-seedream-5-0-pro"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 9, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI Doubao Seedream' AND deleted_at IS NULL);
INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 9, NOW() FROM accounts a
WHERE a.name = 'ToAPI Doubao Seedream' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

-- ToAPI Vidu（4 个模型）
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI Vidu', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         'model_mapping', '{"vidu-image": "vidu-image", "viduq2-fast": "viduq2-fast", "viduq2-pro": "viduq2-pro", "viduq3-fast": "viduq3-fast"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 10, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI Vidu' AND deleted_at IS NULL);
INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 10, NOW() FROM accounts a
WHERE a.name = 'ToAPI Vidu' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

-- ToAPI Qwen（2 个模型）
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI Qwen', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         'model_mapping', '{"qwen-image-3.0": "qwen-image-3.0", "qwen-image-3.0-pro": "qwen-image-3.0-pro"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 11, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI Qwen' AND deleted_at IS NULL);
INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 11, NOW() FROM accounts a
WHERE a.name = 'ToAPI Qwen' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

COMMIT;