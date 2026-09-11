-- 恢复 DeepSeek 的峰谷定价配置。
--
-- 2026-09-11 19:54 通过管理后台「编辑渠道」保存时被清空：那个表单不带
-- time_pricing 字段，而保存是整行覆盖，于是这一列被写成 NULL。
-- 后果是 DeepSeek 文本 24 小时按峰值计费——用户在空闲时段被多收一倍。
--
-- 配置内容取自本次清空前的线上值（当天查过并原样打印）：
-- 高峰为北京时间周一至周五 09:00-12:00 与 14:00-18:00，窗口之外乘 0.5。
-- 配的价格是高峰价，空闲价由倍率算出——两侧用同一份 JSON 结构与语义。
--
-- 成本侧一并恢复：成本记成恒定值的话，高峰低估、空闲高估，
-- 毛利报表两个方向都不准。成本峰值 300 积分/百万 token（¥3.0），
-- 空闲 150（¥1.5），与售价同步减半，两个时段毛利率保持一致。

BEGIN;

UPDATE channel_model_pricing
SET time_pricing = '{
  "timezone": "Asia/Shanghai",
  "peak_windows": [
    {"days": [1,2,3,4,5], "start": "09:00", "end": "12:00"},
    {"days": [1,2,3,4,5], "start": "14:00", "end": "18:00"}
  ],
  "off_peak_multiplier": 0.5
}'::jsonb,
    updated_at = NOW()
WHERE models::text ILIKE '%deepseek%' AND time_pricing IS NULL;

UPDATE channel_account_stats_model_pricing
SET time_pricing = '{
  "timezone": "Asia/Shanghai",
  "peak_windows": [
    {"days": [1,2,3,4,5], "start": "09:00", "end": "12:00"},
    {"days": [1,2,3,4,5], "start": "14:00", "end": "18:00"}
  ],
  "off_peak_multiplier": 0.5
}'::jsonb,
    updated_at = NOW()
WHERE models::text ILIKE '%deepseek%' AND time_pricing IS NULL;

COMMIT;
