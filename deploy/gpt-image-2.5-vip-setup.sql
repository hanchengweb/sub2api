-- GPT-Image-2.5 VIP 接入：gpt-image-2.5-flare-vip / gpt-image-2.5-sunburst-vip
--
-- 与普通版最大的不同：上游按实际 token 后付费（quota_type=0，输入 $4/M、输出 $24/M），
-- 官方**没有**公布每张固定价，而且异步任务的查询响应不返回 usage
-- （完成响应只有 completed_at/created_at/expires_at/id/model/object/progress/result/status），
-- 所以我们无法按 token 计费——那样会一路扣 0。
--
-- 每张成本改用实测：生成一张图前后各调一次上游 /v1/user/balance，取 used_balance 差额。
-- 方法可信度校验——低质量 1024x1024 实测 $0.004792，官方文档同参数示例算得 $0.004812，吻合。
--
-- 实测表（USD/张，2026-09-10）：
--            1024x1024   2048x2048   3840x2160
--   low       0.004792    0.009592    (该格生成失败，未测到)
--   medium    0.010600    0.021472    0.020824
--   high      0.042208    0.085696    0.080152
--
-- 成本随**最长边**而非像素数增长（3840x2160 的像素是 2048x2048 的两倍，费用反而略低），
-- 说明上游输出 token 有上限。因此 4K 一律取 max(4K实测, 2K实测)：
-- 4K 不该比 2K 便宜，宁可多收不能漏收。
-- 汇率 7.0 与全站口径一致；售价 = 成本 + 29 积分/张。
--
-- quality 只配 低/中/高 三档。上游还有 xhigh/max，但 NormalizeImageQuality 归一不了它们，
-- validateOpenAIImagesQuality 会直接拦下——这是**故意**的：这两档上游更贵，
-- 若映射成「高」按 88.99 收，高分辨率下就是倒贴。
--
-- 幂等：按 name / 模型名判重，可重复执行。
BEGIN;

-- ===== 1. 账号（独立并发池；密钥从账号 3 子查询复制，不落明文） =====
INSERT INTO accounts (name, platform, type, credentials, extra, concurrency, priority,
                      status, schedulable, auto_pause_on_expired, rate_multiplier, quota_dimension,
                      created_at, updated_at)
SELECT 'ToAPI GPT Image 2.5 VIP', 'openai', 'apikey',
       jsonb_build_object(
         'api_key', (SELECT credentials->>'api_key' FROM accounts WHERE id = 3),
         'base_url', 'https://toapis.cn/v1',
         'pool_mode', true,
         'pool_mode_retry_count', 1,
         'model_mapping', '{"gpt-image-2.5-flare-vip": "gpt-image-2.5-flare-vip", "gpt-image-2.5-sunburst-vip": "gpt-image-2.5-sunburst-vip"}'::jsonb
       ),
       '{"openai_responses_supported": true, "upstream_billing_probe_enabled": false}'::jsonb,
       10, 13, 'active', true, true, 1.0, 'global', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM accounts WHERE name = 'ToAPI GPT Image 2.5 VIP' AND deleted_at IS NULL);

INSERT INTO account_groups (account_id, group_id, priority, created_at)
SELECT a.id, 4, 13, NOW() FROM accounts a
WHERE a.name = 'ToAPI GPT Image 2.5 VIP' AND a.deleted_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id = a.id AND ag.group_id = 4);

-- ===== 2. 路由 =====
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gpt-image-2.5-flare-vip', 'exact', 'openai', 'gpt-image-2.5-flare-vip', 'any', 1, true,
       'toAPI GPT-Image-2.5 Flare VIP，每张成本实测（清晰度 x 质量九档）', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gpt-image-2.5-flare-vip' AND deleted_at IS NULL);

INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gpt-image-2.5-sunburst-vip', 'exact', 'openai', 'gpt-image-2.5-sunburst-vip', 'any', 1, true,
       'toAPI GPT-Image-2.5 Sunburst VIP，每张成本实测（清晰度 x 质量九档）', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gpt-image-2.5-sunburst-vip' AND deleted_at IS NULL);

-- ===== 3. 售价：兜底取最高档 88.99（档位识别失败时宁可按最贵档收） =====
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, 'openai', '["gpt-image-2.5-flare-vip"]'::jsonb, 'image', 88.99, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-flare-vip"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-flare-vip"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_model_pricing, (VALUES
  ('1K·低',32.35,0),('1K·中',36.42,1),('1K·高',58.55,2),
  ('2K·低',35.71,3),('2K·中',44.03,4),('2K·高',88.99,5),
  ('4K·低',35.71,6),('4K·中',44.03,7),('4K·高',88.99,8)
) AS t(label,price,ord)
WHERE channel_id=1 AND models @> '["gpt-image-2.5-flare-vip"]'::jsonb;

INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, 'openai', '["gpt-image-2.5-sunburst-vip"]'::jsonb, 'image', 88.99, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-sunburst-vip"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gpt-image-2.5-sunburst-vip"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_model_pricing, (VALUES
  ('1K·低',32.35,0),('1K·中',36.42,1),('1K·高',58.55,2),
  ('2K·低',35.71,3),('2K·中',44.03,4),('2K·高',88.99,5),
  ('4K·低',35.71,6),('4K·中',44.03,7),('4K·高',88.99,8)
) AS t(label,price,ord)
WHERE channel_id=1 AND models @> '["gpt-image-2.5-sunburst-vip"]'::jsonb;

-- ===== 4. 成本：兜底取最低档 3.35 =====
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, '', '["gpt-image-2.5-flare-vip"]'::jsonb, 'image', 3.35, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-flare-vip"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-flare-vip"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_account_stats_model_pricing, (VALUES
  ('1K·低',3.35,0),('1K·中',7.42,1),('1K·高',29.55,2),
  ('2K·低',6.71,3),('2K·中',15.03,4),('2K·高',59.99,5),
  ('4K·低',6.71,6),('4K·中',15.03,7),('4K·高',59.99,8)
) AS t(label,price,ord)
WHERE rule_id=1 AND models @> '["gpt-image-2.5-flare-vip"]'::jsonb;

INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, per_request_price, created_at, updated_at)
SELECT 1, '', '["gpt-image-2.5-sunburst-vip"]'::jsonb, 'image', 3.35, NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-sunburst-vip"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2.5-sunburst-vip"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_account_stats_model_pricing, (VALUES
  ('1K·低',3.35,0),('1K·中',7.42,1),('1K·高',29.55,2),
  ('2K·低',6.71,3),('2K·中',15.03,4),('2K·高',59.99,5),
  ('4K·低',6.71,6),('4K·中',15.03,7),('4K·高',59.99,8)
) AS t(label,price,ord)
WHERE rule_id=1 AND models @> '["gpt-image-2.5-sunburst-vip"]'::jsonb;

-- ===== 5. 组 4 的 /v1/models 白名单（漏了就配好也不显示） =====
UPDATE groups SET
  models_list_config = jsonb_set(
    models_list_config, '{models}',
    (SELECT jsonb_agg(m ORDER BY m) FROM (
       SELECT jsonb_array_elements_text(models_list_config->'models') AS m
       UNION SELECT 'gpt-image-2.5-flare-vip'
       UNION SELECT 'gpt-image-2.5-sunburst-vip'
     ) s)
  ),
  updated_at = NOW()
WHERE id = 4;

COMMIT;
