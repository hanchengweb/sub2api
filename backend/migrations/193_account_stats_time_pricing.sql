-- 成本侧的时段定价。
--
-- 售价侧（channel_model_pricing.time_pricing）已有同名列，这里给成本侧补上，
-- 两边共用同一份 JSON 结构与语义：配的价格是「高峰价」，窗口之外乘折扣系数。
--
-- 为什么成本也要分时段：DeepSeek 官方直连按时段计费（高峰为北京时间周一至周五
-- 9:00-12:00、14:00-18:00，空闲价是高峰价的一半）。成本按恒定值记账时，
-- 高峰低估、空闲高估，利润报表两个方向都不准。
--
-- 空值 = 不启用，行为与之前完全一致。
ALTER TABLE channel_account_stats_model_pricing
  ADD COLUMN IF NOT EXISTS time_pricing JSONB;
