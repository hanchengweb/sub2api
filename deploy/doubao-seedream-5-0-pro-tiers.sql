-- doubao-seedream-5-0-pro 按清晰度分档：2K 及以上 89 积分。
--
-- 现状（2026-09-12 线上核对）：
--   卖价行 channel_model_pricing id=101 ["doubao-seedream-5-0-pro"]，扁平 59，零档位
--   成本行 channel_account_stats_model_pricing id=86，扁平 30，零档位
-- 没有档位时计费和前端都回落到 per_request_price，所以「在线使用」里
-- 1K / 2K / 4K 三档显示的都是 59.00 —— 4K 出一张也只收 1K 的钱。
--
-- 档位值本来就存在，只是配在了另一行上：id=83 的
-- ["v-doubao-seedream-5-0-pro", "gpt-image-1"] 有 1K 59 / 2K 89 / 4K 89 三档。
-- 但 v-doubao-seedream-5-0-pro 不在任何分组的 models_list_config 里，取不到，
-- 实际对外的是 doubao-seedream-5-0-pro（分组 4）。id=83 不动，它还带着 gpt-image-1。
--
-- 成本侧 2K/4K 取 60 积分（¥0.60）：按本站图像定价规则「成本 + 29 积分」倒推
-- 89 - 29 = 60。这是推断，不是上游账单实测值——deploy/image-price-plan.json 里
-- 原记录是「不分档、¥0.30 一张」。宁可把成本记高：记低会让毛利虚高。
--
-- 档位名只能用 1K / 2K / 4K，与 gpt-image-2（id=80）保持一致。
-- 绝对不能起名「默认」——计费侧不认这个标签，会按 0 计费。

BEGIN;

-- 卖价档位
DELETE FROM channel_pricing_intervals
WHERE pricing_id = (SELECT id FROM channel_model_pricing
                    WHERE channel_id = 1 AND models = '["doubao-seedream-5-0-pro"]'::jsonb);

INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT cp.id, 0, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_model_pricing cp
CROSS JOIN (VALUES ('1K', 59.0, 0), ('2K', 89.0, 1), ('4K', 89.0, 2)) AS t(label, price, ord)
WHERE cp.channel_id = 1 AND cp.models = '["doubao-seedream-5-0-pro"]'::jsonb;

-- 成本档位
DELETE FROM channel_account_stats_pricing_intervals
WHERE pricing_id = (SELECT id FROM channel_account_stats_model_pricing
                    WHERE models = '["doubao-seedream-5-0-pro"]'::jsonb);

INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, tier_label, per_request_price, sort_order, created_at, updated_at)
SELECT p.id, 0, t.label, t.price, t.ord, NOW(), NOW()
FROM channel_account_stats_model_pricing p
CROSS JOIN (VALUES ('1K', 30.0, 0), ('2K', 60.0, 1), ('4K', 60.0, 2)) AS t(label, price, ord)
WHERE p.models = '["doubao-seedream-5-0-pro"]'::jsonb;

-- 扁平价保持 1K 的值：没匹配到档位时回落到它，回落成最低档比回落成最高档安全
UPDATE channel_model_pricing SET per_request_price = 59.0, updated_at = NOW()
WHERE channel_id = 1 AND models = '["doubao-seedream-5-0-pro"]'::jsonb;
UPDATE channel_account_stats_model_pricing SET per_request_price = 30.0, updated_at = NOW()
WHERE models = '["doubao-seedream-5-0-pro"]'::jsonb;

COMMIT;

-- 核对
SELECT '卖价' AS side, i.tier_label, i.per_request_price
FROM channel_pricing_intervals i
JOIN channel_model_pricing cp ON cp.id = i.pricing_id
WHERE cp.models = '["doubao-seedream-5-0-pro"]'::jsonb
UNION ALL
SELECT '成本', i.tier_label, i.per_request_price
FROM channel_account_stats_pricing_intervals i
JOIN channel_account_stats_model_pricing p ON p.id = i.pricing_id
WHERE p.models = '["doubao-seedream-5-0-pro"]'::jsonb
ORDER BY 1 DESC, 2;
