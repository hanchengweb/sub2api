-- 无档位维度的模型，价格要放在 per_request_price（默认按次价），不能放进档位。
--
-- 生成脚本给这类模型造了一个标签叫「默认」的档位，但计费链路只会按
-- 「清晰度·质量」→「清晰度」→ per_request_price 三级查找，压根不认识「默认」
-- 这个标签，于是一路落到 NULL、扣费 0。线上实测 qwen-image-3.0 扣 0 才发现。
BEGIN;

-- 售价侧
UPDATE channel_model_pricing p
SET per_request_price = i.per_request_price, updated_at = NOW()
FROM channel_pricing_intervals i
WHERE i.pricing_id = p.id AND i.tier_label = '默认' AND p.per_request_price IS NULL;

DELETE FROM channel_pricing_intervals WHERE tier_label = '默认';

-- 成本侧
UPDATE channel_account_stats_model_pricing p
SET per_request_price = i.per_request_price, updated_at = NOW()
FROM channel_account_stats_pricing_intervals i
WHERE i.pricing_id = p.id AND i.tier_label = '默认' AND p.per_request_price IS NULL;

DELETE FROM channel_account_stats_pricing_intervals WHERE tier_label = '默认';

COMMIT;
