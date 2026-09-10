-- 生图模型全量配置：27 个模型 / 47 条价目
-- 售价 = 上游成本 + 29 积分（每张）。成本取 toAPI 价目表。
-- 由 deploy/image-price-plan.json 生成，勿手改；改价请改那个文件后重新生成。
-- 无档位维度的模型，价格写进 channel_model_pricing.per_request_price，
-- **不要**造一个标签叫「默认」的档位：计费链路只按
-- 「清晰度·质量」→「清晰度」→ per_request_price 三级查找，不认识「默认」，
-- 会一路落到 NULL、扣费 0。线上实测 qwen-image-3.0 扣 0 才发现，23 个模型都中招。
-- 修复补丁见 deploy/fix-default-tier.sql。
BEGIN;

-- Black Forest Labs / flux-2-flex
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'flux-2-flex', 'exact', 'openai', 'flux-2-flex', 'any', 1, true, 'Black Forest Labs', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='flux-2-flex' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["flux-2-flex"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-2-flex"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-2-flex"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 78.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["flux-2-flex"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["flux-2-flex"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-2-flex"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-2-flex"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 49.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["flux-2-flex"]'::jsonb;

-- Black Forest Labs / flux-2-pro
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'flux-2-pro', 'exact', 'openai', 'flux-2-pro', 'any', 1, true, 'Black Forest Labs', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='flux-2-pro' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["flux-2-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-2-pro"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-2-pro"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 46.5, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["flux-2-pro"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["flux-2-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-2-pro"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-2-pro"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 17.5, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["flux-2-pro"]'::jsonb;

-- Black Forest Labs / flux-kontext-max
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'flux-kontext-max', 'exact', 'openai', 'flux-kontext-max', 'any', 1, true, 'Black Forest Labs', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='flux-kontext-max' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["flux-kontext-max"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-kontext-max"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-kontext-max"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 57.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["flux-kontext-max"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["flux-kontext-max"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-kontext-max"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-kontext-max"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 28.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["flux-kontext-max"]'::jsonb;

-- Black Forest Labs / flux-kontext-pro
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'flux-kontext-pro', 'exact', 'openai', 'flux-kontext-pro', 'any', 1, true, 'Black Forest Labs', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='flux-kontext-pro' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["flux-kontext-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-kontext-pro"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["flux-kontext-pro"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 43.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["flux-kontext-pro"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["flux-kontext-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-kontext-pro"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["flux-kontext-pro"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 14.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["flux-kontext-pro"]'::jsonb;

-- Google Gemini / gemini-2.5-flash-image-official
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gemini-2.5-flash-image-official', 'exact', 'openai', 'gemini-2.5-flash-image-official', 'any', 1, true, 'Google Gemini', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gemini-2.5-flash-image-official' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gemini-2.5-flash-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-2.5-flash-image-official"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-2.5-flash-image-official"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 63.16, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["gemini-2.5-flash-image-official"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gemini-2.5-flash-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-2.5-flash-image-official"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-2.5-flash-image-official"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 34.16, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gemini-2.5-flash-image-official"]'::jsonb;

-- Google Gemini / gemini-2.5-flash-image-preview
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gemini-2.5-flash-image-preview', 'exact', 'openai', 'gemini-2.5-flash-image-preview', 'any', 1, true, 'Google Gemini', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gemini-2.5-flash-image-preview' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gemini-2.5-flash-image-preview"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-2.5-flash-image-preview"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-2.5-flash-image-preview"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 37.4, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["gemini-2.5-flash-image-preview"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gemini-2.5-flash-image-preview"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-2.5-flash-image-preview"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-2.5-flash-image-preview"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 8.4, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gemini-2.5-flash-image-preview"]'::jsonb;

-- Google Gemini / gemini-3-pro-image-official
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gemini-3-pro-image-official', 'exact', 'openai', 'gemini-3-pro-image-official', 'any', 1, true, 'Google Gemini', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gemini-3-pro-image-official' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gemini-3-pro-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3-pro-image-official"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3-pro-image-official"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 104.04, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["gemini-3-pro-image-official"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gemini-3-pro-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3-pro-image-official"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3-pro-image-official"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 75.04, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gemini-3-pro-image-official"]'::jsonb;

-- Google Gemini / gemini-3-pro-image-preview
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gemini-3-pro-image-preview', 'exact', 'openai', 'gemini-3-pro-image-preview', 'any', 1, true, 'Google Gemini', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gemini-3-pro-image-preview' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gemini-3-pro-image-preview"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3-pro-image-preview"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3-pro-image-preview"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 71.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["gemini-3-pro-image-preview"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gemini-3-pro-image-preview"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3-pro-image-preview"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3-pro-image-preview"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 42.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gemini-3-pro-image-preview"]'::jsonb;

-- Google Gemini / gemini-3.1-flash-image-official
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gemini-3.1-flash-image-official', 'exact', 'openai', 'gemini-3.1-flash-image-official', 'any', 1, true, 'Google Gemini', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gemini-3.1-flash-image-official' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gemini-3.1-flash-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3.1-flash-image-official"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3.1-flash-image-official"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 66.52, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["gemini-3.1-flash-image-official"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gemini-3.1-flash-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3.1-flash-image-official"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3.1-flash-image-official"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 37.52, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gemini-3.1-flash-image-official"]'::jsonb;

-- Google Gemini / gemini-3.1-flash-image-preview
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gemini-3.1-flash-image-preview', 'exact', 'openai', 'gemini-3.1-flash-image-preview', 'any', 1, true, 'Google Gemini', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gemini-3.1-flash-image-preview' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gemini-3.1-flash-image-preview"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3.1-flash-image-preview"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3.1-flash-image-preview"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 50.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["gemini-3.1-flash-image-preview"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gemini-3.1-flash-image-preview"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3.1-flash-image-preview"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3.1-flash-image-preview"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 21.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gemini-3.1-flash-image-preview"]'::jsonb;

-- Google Gemini / gemini-3.1-flash-lite-image-official
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'gemini-3.1-flash-lite-image-official', 'exact', 'openai', 'gemini-3.1-flash-lite-image-official', 'any', 1, true, 'Google Gemini', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='gemini-3.1-flash-lite-image-official' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gemini-3.1-flash-lite-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3.1-flash-lite-image-official"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["gemini-3.1-flash-lite-image-official"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 48.04, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["gemini-3.1-flash-lite-image-official"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gemini-3.1-flash-lite-image-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3.1-flash-lite-image-official"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gemini-3.1-flash-lite-image-official"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 19.04, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gemini-3.1-flash-lite-image-official"]'::jsonb;

-- Google Nano Banana / nano_banana
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'nano_banana', 'exact', 'openai', 'nano_banana', 'any', 1, true, 'Google Nano Banana', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='nano_banana' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["nano_banana"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["nano_banana"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["nano_banana"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 37.4, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["nano_banana"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["nano_banana"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["nano_banana"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["nano_banana"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 8.4, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["nano_banana"]'::jsonb;

-- Google Nano Banana / nano_banana_2
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'nano_banana_2', 'exact', 'openai', 'nano_banana_2', 'any', 1, true, 'Google Nano Banana', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='nano_banana_2' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["nano_banana_2"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["nano_banana_2"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["nano_banana_2"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K', 50.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["nano_banana_2"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K', 57.0, 1, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["nano_banana_2"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K', 71.0, 2, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["nano_banana_2"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["nano_banana_2"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["nano_banana_2"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["nano_banana_2"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K', 21.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["nano_banana_2"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K', 28.0, 1, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["nano_banana_2"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K', 42.0, 2, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["nano_banana_2"]'::jsonb;

-- Google Nano Banana / nano_banana_pro
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'nano_banana_pro', 'exact', 'openai', 'nano_banana_pro', 'any', 1, true, 'Google Nano Banana', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='nano_banana_pro' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["nano_banana_pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["nano_banana_pro"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["nano_banana_pro"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 71.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["nano_banana_pro"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["nano_banana_pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["nano_banana_pro"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["nano_banana_pro"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 42.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["nano_banana_pro"]'::jsonb;

INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gpt-image-2-official", "t-gpt-image-2-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·低', 31.69, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·中', 52.63, 1, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·高', 123.5, 2, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·低', 34.92, 3, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·中', 81.16, 4, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·高', 238.0, 5, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·低', 36.36, 6, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·中', 95.16, 7, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·高', 294.0, 8, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gpt-image-2-official"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·低', 2.69, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·中', 23.63, 1, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·高', 94.5, 2, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·低', 5.92, 3, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·中', 52.16, 4, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·高', 209.0, 5, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·低', 7.36, 6, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·中', 66.16, 7, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·高', 265.0, 8, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-official"]'::jsonb;

INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gpt-image-2-vip", "t-gpt-image-2-vip"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·低', 30.33, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·中', 40.83, 1, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·高', 76.25, 2, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·低', 31.98, 3, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·中', 55.08, 4, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·高', 133.0, 5, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·低', 32.68, 6, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·中', 62.08, 7, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·高', 161.0, 8, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gpt-image-2-vip"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·低', 1.33, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·中', 11.83, 1, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K·高', 47.25, 2, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·低', 2.98, 3, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·中', 26.08, 4, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K·高', 104.0, 5, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·低', 3.68, 6, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·中', 33.08, 7, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K·高', 132.0, 8, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2-vip"]'::jsonb;

INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["gpt-image-2", "t-gpt-image-2"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["t-gpt-image-2"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["t-gpt-image-2"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K', 39.5, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K', 43.0, 1, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2"]'::jsonb;
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K', 46.5, 2, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["t-gpt-image-2"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["gpt-image-2"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["gpt-image-2"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '1K', 10.5, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '2K', 14.0, 1, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2"]'::jsonb;
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '4K', 17.5, 2, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["gpt-image-2"]'::jsonb;

-- 字节豆包 Doubao / doubao-seedream-4-0
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'doubao-seedream-4-0', 'exact', 'openai', 'doubao-seedream-4-0', 'any', 1, true, '字节豆包 Doubao', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='doubao-seedream-4-0' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["doubao-seedream-4-0"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-4-0"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-4-0"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 48.95, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["doubao-seedream-4-0"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["doubao-seedream-4-0"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-4-0"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-4-0"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 19.95, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["doubao-seedream-4-0"]'::jsonb;

-- 字节豆包 Doubao / doubao-seedream-4-5
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'doubao-seedream-4-5', 'exact', 'openai', 'doubao-seedream-4-5', 'any', 1, true, '字节豆包 Doubao', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='doubao-seedream-4-5' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["doubao-seedream-4-5"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-4-5"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-4-5"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 53.99, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["doubao-seedream-4-5"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["doubao-seedream-4-5"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-4-5"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-4-5"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 24.99, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["doubao-seedream-4-5"]'::jsonb;

-- 字节豆包 Doubao / doubao-seedream-5-0
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'doubao-seedream-5-0', 'exact', 'openai', 'doubao-seedream-5-0', 'any', 1, true, '字节豆包 Doubao', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='doubao-seedream-5-0' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["doubao-seedream-5-0"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-5-0"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-5-0"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 51.05, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["doubao-seedream-5-0"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["doubao-seedream-5-0"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-5-0"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-5-0"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 22.05, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["doubao-seedream-5-0"]'::jsonb;

-- 字节豆包 Doubao / doubao-seedream-5-0-pro
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'doubao-seedream-5-0-pro', 'exact', 'openai', 'doubao-seedream-5-0-pro', 'any', 1, true, '字节豆包 Doubao', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='doubao-seedream-5-0-pro' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["doubao-seedream-5-0-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-5-0-pro"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["doubao-seedream-5-0-pro"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 59.0, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["doubao-seedream-5-0-pro"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["doubao-seedream-5-0-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-5-0-pro"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["doubao-seedream-5-0-pro"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 30.0, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["doubao-seedream-5-0-pro"]'::jsonb;

-- 智谱清影 Vidu / vidu-image
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'vidu-image', 'exact', 'openai', 'vidu-image', 'any', 1, true, '智谱清影 Vidu', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='vidu-image' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["vidu-image"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["vidu-image"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["vidu-image"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 75.87, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["vidu-image"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["vidu-image"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["vidu-image"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["vidu-image"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 46.87, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["vidu-image"]'::jsonb;

-- 智谱清影 Vidu / viduq2-fast
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'viduq2-fast', 'exact', 'openai', 'viduq2-fast', 'any', 1, true, '智谱清影 Vidu', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='viduq2-fast' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["viduq2-fast"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["viduq2-fast"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["viduq2-fast"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 50.09, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["viduq2-fast"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["viduq2-fast"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["viduq2-fast"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["viduq2-fast"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 21.09, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["viduq2-fast"]'::jsonb;

-- 智谱清影 Vidu / viduq2-pro
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'viduq2-pro', 'exact', 'openai', 'viduq2-pro', 'any', 1, true, '智谱清影 Vidu', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='viduq2-pro' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["viduq2-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["viduq2-pro"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["viduq2-pro"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 99.31, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["viduq2-pro"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["viduq2-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["viduq2-pro"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["viduq2-pro"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 70.31, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["viduq2-pro"]'::jsonb;

-- 智谱清影 Vidu / viduq3-fast
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'viduq3-fast', 'exact', 'openai', 'viduq3-fast', 'any', 1, true, '智谱清影 Vidu', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='viduq3-fast' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["viduq3-fast"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["viduq3-fast"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["viduq3-fast"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 64.16, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["viduq3-fast"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["viduq3-fast"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["viduq3-fast"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["viduq3-fast"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 35.16, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["viduq3-fast"]'::jsonb;

-- 阿里云通义 Qwen / qwen-image-3.0
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'qwen-image-3.0', 'exact', 'openai', 'qwen-image-3.0', 'any', 1, true, '阿里云通义 Qwen', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='qwen-image-3.0' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["qwen-image-3.0"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["qwen-image-3.0"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["qwen-image-3.0"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 43.7, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["qwen-image-3.0"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["qwen-image-3.0"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["qwen-image-3.0"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["qwen-image-3.0"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 14.7, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["qwen-image-3.0"]'::jsonb;

-- 阿里云通义 Qwen / qwen-image-3.0-pro
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, 'qwen-image-3.0-pro', 'exact', 'openai', 'qwen-image-3.0-pro', 'any', 1, true, '阿里云通义 Qwen', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM composite_model_routes
  WHERE group_id=4 AND public_model='qwen-image-3.0-pro' AND deleted_at IS NULL);
INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, 'openai', '["qwen-image-3.0-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_model_pricing WHERE channel_id=1 AND models @> '["qwen-image-3.0-pro"]'::jsonb);
DELETE FROM channel_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_model_pricing WHERE channel_id=1 AND models @> '["qwen-image-3.0-pro"]'::jsonb);
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 48.6, 0, NOW(), NOW() FROM channel_model_pricing
WHERE channel_id=1 AND models @> '["qwen-image-3.0-pro"]'::jsonb;
INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, billing_mode, created_at, updated_at)
SELECT 1, '', '["qwen-image-3.0-pro"]'::jsonb, 'image', NOW(), NOW()
WHERE NOT EXISTS (SELECT 1 FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["qwen-image-3.0-pro"]'::jsonb);
DELETE FROM channel_account_stats_pricing_intervals WHERE pricing_id IN
  (SELECT id FROM channel_account_stats_model_pricing WHERE rule_id=1 AND models @> '["qwen-image-3.0-pro"]'::jsonb);
INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT id, 0, NULL, '默认', 19.6, 0, NOW(), NOW() FROM channel_account_stats_model_pricing
WHERE rule_id=1 AND models @> '["qwen-image-3.0-pro"]'::jsonb;

UPDATE groups SET models_list_config = jsonb_build_object(
  'enabled', true, 'models', '["deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "doubao-seedream-4-0", "doubao-seedream-4-5", "doubao-seedream-5-0", "doubao-seedream-5-0-pro", "flux-2-flex", "flux-2-pro", "flux-kontext-max", "flux-kontext-pro", "gemini-2.5-flash-image-official", "gemini-2.5-flash-image-preview", "gemini-3-pro-image-official", "gemini-3-pro-image-preview", "gemini-3.1-flash-image-official", "gemini-3.1-flash-image-preview", "gemini-3.1-flash-lite-image-official", "nano_banana", "nano_banana_2", "nano_banana_pro", "qwen-image-3.0", "qwen-image-3.0-pro", "t-gpt-image-2", "t-gpt-image-2-official", "t-gpt-image-2-vip", "t-grok-video-1.5", "vidu-image", "viduq2-fast", "viduq2-pro", "viduq3-fast"]'::jsonb),
  updated_at = NOW() WHERE id=4;
COMMIT;