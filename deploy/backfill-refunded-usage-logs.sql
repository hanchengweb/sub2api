-- 一次性回填：把 195_usage_logs_refund.sql 上线**之前**已发生的退款补记到 usage_logs。
--
-- 新代码只对之后的退款生效（靠 media_task_charges.request_id 关联），
-- 历史记录那一列是 NULL，只能按「同用户 + 同密钥 + 同金额 + 创建时间相差 60 秒内」配对。
-- 2026-09-10 在生产上核对过：11 笔已退款扣费里，7 笔各自唯一命中一行
-- （candidates 全为 1，无歧义），另 4 笔压根没有 usage_logs 行——那是当天早些时候
-- 一个已修复的回归（提前 return 跳过了写日志）留下的，退款本身正常，报表也没虚增。
--
-- 幂等：只改 refunded_at IS NULL 的行，重复执行不会把金额减两次。
--
-- 执行前先跑 SELECT 段确认配对唯一，再跑 UPDATE。
BEGIN;

-- ===== 1. 配对预览：candidates 必须全为 1，否则不要往下执行 =====
WITH refunded AS (
  SELECT id AS charge_id, user_id, api_key_id, credits, created_at
  FROM media_task_charges
  WHERE refunded_at IS NOT NULL AND request_id IS NULL
),
paired AS (
  SELECT r.charge_id, r.credits, u.id AS log_id, u.model, u.total_cost,
         count(*) OVER (PARTITION BY r.charge_id) AS candidates_per_charge,
         count(*) OVER (PARTITION BY u.id)        AS charges_per_log
  FROM refunded r
  JOIN usage_logs u
    ON u.user_id = r.user_id
   AND u.api_key_id = r.api_key_id
   AND abs(extract(epoch FROM (u.created_at - r.created_at))) <= 60
   AND abs(u.total_cost - r.credits) < 0.0001
  WHERE u.refunded_at IS NULL
)
SELECT charge_id, log_id, model, credits, total_cost,
       candidates_per_charge, charges_per_log
FROM paired ORDER BY charge_id;

-- ===== 2. 回填 =====
-- 与运行时同一口径：冲平收入列、成本列归零、记下退款额。
-- 只回填一对一的配对（两侧 count 都为 1），有歧义的宁可留着人工看。
WITH refunded AS (
  SELECT id AS charge_id, user_id, api_key_id, credits, created_at
  FROM media_task_charges
  WHERE refunded_at IS NOT NULL AND request_id IS NULL
),
paired AS (
  SELECT r.charge_id, r.credits, u.id AS log_id,
         count(*) OVER (PARTITION BY r.charge_id) AS candidates_per_charge,
         count(*) OVER (PARTITION BY u.id)        AS charges_per_log
  FROM refunded r
  JOIN usage_logs u
    ON u.user_id = r.user_id
   AND u.api_key_id = r.api_key_id
   AND abs(extract(epoch FROM (u.created_at - r.created_at))) <= 60
   AND abs(u.total_cost - r.credits) < 0.0001
  WHERE u.refunded_at IS NULL
),
unique_pairs AS (
  SELECT log_id, credits FROM paired
  WHERE candidates_per_charge = 1 AND charges_per_log = 1
)
UPDATE usage_logs u
SET refunded_at = NOW(),
    refunded_credits = p.credits,
    total_cost = GREATEST(u.total_cost - p.credits, 0),
    actual_cost = GREATEST(u.actual_cost - p.credits, 0),
    account_stats_cost = 0
FROM unique_pairs p
WHERE u.id = p.log_id AND u.refunded_at IS NULL;

COMMIT;
