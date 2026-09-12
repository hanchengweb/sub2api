-- 更正 doubao-seedream-5-0-pro 的成本档位：三档都是 30 积分（¥0.30）。
--
-- 2026-09-12 上游价目表（韩诚提供截图）：doubao-seedream-5-0-pro ¥0.30 一口价，
-- 支持 1K/2K/4K 但不分档收费。表头写明「分档模型已按 1K/2K/4K 逐档列出」，
-- 这个模型只列了一行 —— 就是不分档。
--
-- 上一版（doubao-seedream-5-0-pro-tiers.sql）把 2K/4K 成本记成 60，是按
-- 「售价 − 29 积分加成」倒推的，错了：2K/4K 的 89 是我们自己按清晰度加价，
-- 不是上游成本更高。成本记高会让毛利虚低，进而误导后续定价决策。
--
-- 售价不动，仍是 1K 59 / 2K 89 / 4K 89。

BEGIN;

UPDATE channel_account_stats_pricing_intervals
SET per_request_price = 30.0, updated_at = NOW()
WHERE pricing_id = (SELECT id FROM channel_account_stats_model_pricing
                    WHERE models = '["doubao-seedream-5-0-pro"]'::jsonb);

COMMIT;

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
