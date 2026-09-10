-- GPT-Image-2.5 普通版接入：gpt-image-2.5-flare / gpt-image-2.5-sunburst
--
-- 上游成本（toAPI 官方文档 + 控制台定价页，2026-09-10 双向核对）：
--   1K $0.015、2K $0.020、4K $0.025 每张，两个模型同价，汇率 7.0 → ¥0.105 / ¥0.14 / ¥0.175。
--   与现网 gpt-image-2 完全同价，因此售价也完全一致（成本 + 29 积分）：39.5 / 43 / 46.5。
--
-- 只有清晰度一个维度，**没有质量档**：普通版固定 high，文档明确
-- 「这三个价格均适用于 low, medium, high, xhigh, max」。所以档位标签写 1K/2K/4K，
-- 不带「·低/·中/·高」后缀，也绝不写「默认」——计费链路按
-- 「清晰度·质量」→「清晰度」→ per_request_price 三级查找，不认识「默认」会扣 0。
--
-- 两个 VIP 通道（gpt-image-2.5-flare-vip / -sunburst-vip）**不在本次接入范围**：
-- 上游按实际 token 后付费（quota_type=0，输入 $4/M、输出 $24/M），
-- 而异步任务的查询响应只有 status / result.data，不返回 usage，
-- 按 token 计费会扣 0；官方也未公布每张固定价，无法套用「成本 + 29」。
--
-- 幂等：全部按 name / 模型名判重，可重复执行。
BEGIN;

-- ===== 1. 账号：独立并发池，形态与账号 3/4/5 一致，api_key 从账号 3 子查询复制 =====
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI GPT Image 2.5', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         -- 只登记正名，不加 t- 别名：别名会让 /v1/models 与在线使用的下拉多出一倍条目
         'model_mapping', '{"gpt-image-2.5-flare": "gpt-image-2.5-flare", "gpt-image-2.5-sunburst": "gpt-image-2.5-sunburst"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 12, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI GPT Image 2.5' AND deleted_at IS NULL);

INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 12, NOW() FROM accounts a
WHERE a.name = 'ToAPI GPT Image 2.5' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

-- ===== 2. 路由 =====
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gpt-image-2.5-flare', 'exact', 'openai', 'gpt-image-2.5-flare', 'any', 1, true,
       'toAPI GPT-Image-2.5 Flare 普通版 ¥0.105/0.14/0.175 每张（1K/2K/4K）', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gpt-image-2.5-flare' AND deleted_at IS NULL);

INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gpt-image-2.5-sunburst', 'exact', 'openai', 'gpt-image-2.5-sunburst', 'any', 1, true,
       'toAPI GPT-Image-2.5 Sunburst 普通版 ¥0.105/0.14/0.175 每张（1K/2K/4K）', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gpt-image-2.5-sunburst' AND deleted_at IS NULL);

-- ===== 3. 售价（channel 1 统一计费渠道） =====
-- per_request_price 是档位查不到时的兜底，取最高档 46.5：与现网 gpt-image-2 同一约定，
-- 宁可按最贵档收也不能漏收（线上出现过 image_size_source=default 的档位识别失败）。
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, 'openai', '["gpt-image-2.5-flare"]'::jsonb, 'image', 46.5, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-flare"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-flare"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_model_pricing, (VALUES ('1K',39.5,0),('2K',43.0,1),('4K',46.5,2)) AS t(label,price,ord)
WHERE channel_id=1 AND models @> '["gpt-image-2.5-flare"]'::jsonb;

INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, 'openai', '["gpt-image-2.5-sunburst"]'::jsonb, 'image', 46.5, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-sunburst"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-sunburst"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_model_pricing, (VALUES ('1K',39.5,0),('2K',43.0,1),('4K',46.5,2)) AS t(label,price,ord)
WHERE channel_id=1 AND models @> '["gpt-image-2.5-sunburst"]'::jsonb;

-- ===== 4. 成本（账号统计口径，rule 1） =====
-- 兜底取最低档 10.5，与现网 gpt-image-2 同一约定：成本侧宁可少算也不虚增毛利。
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, '', '["gpt-image-2.5-flare"]'::jsonb, 'image', 10.5, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-flare"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-flare"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_account_stats_model_pricing, (VALUES ('1K',10.5,0),('2K',14.0,1),('4K',17.5,2)) AS t(label,price,ord)
WHERE rule_id=1 AND models @> '["gpt-image-2.5-flare"]'::jsonb;

INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, '', '["gpt-image-2.5-sunburst"]'::jsonb, 'image', 10.5, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-sunburst"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-sunburst"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_account_stats_model_pricing, (VALUES ('1K',10.5,0),('2K',14.0,1),('4K',17.5,2)) AS t(label,price,ord)
WHERE rule_id=1 AND models @> '["gpt-image-2.5-sunburst"]'::jsonb;

COMMIT;

-- ===== 5. 组 4 的 /v1/models 白名单 =====
--
-- **这一步不能漏**：composite 组的模型列表不是 model_mapping 的并集，
-- 而是 groups.models_list_config.models 这份显式白名单（enabled=true 时生效）。
-- 只建账号和路由，模型能调通但不会出现在 /v1/models、在线使用下拉和模型广场里。
-- UNION 去重，重复执行安全。
BEGIN;
UPDATE groups SET
  models_list_config = jsonb_set(
    models_list_config, '{models}',
    (SELECT jsonb_agg(m ORDER BY m) FROM (
       SELECT jsonb_array_elements_text(models_list_config->'models') AS m
       UNION SELECT 'gpt-image-2.5-flare'
       UNION SELECT 'gpt-image-2.5-sunburst'
     ) s)
  ),
  updated_at = NOW()
WHERE id = 4;
COMMIT;
