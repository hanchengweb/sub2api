-- 异步媒体任务失败退款：把退款回写到 usage_logs。
--
-- 现状（2026-09-10 实测）：任务失败时余额确实原额退回了（media_task_charges
-- 标记 refunded_at + users.balance 回补），但 usage_logs 那一行 total_cost /
-- actual_cost 仍是全额，也没有任何冲抵记录（billing_usage_entries 走的是另一条
-- 订阅结算路径，这里根本不写）。后果有两个：
--   1. 用户在「使用日志」里看到一笔自己实际没付的扣费；
--   2. 平台收入 / 毛利报表按 total_cost 汇总，虚高。
--
-- 做法：退款时把这一行的收入列直接冲平，并把退款额记进 refunded_credits。
-- 为什么不保留 total_cost 原值、让报表去减：全仓有 25 处以上
-- `SUM(total_cost)` 聚合（dashboard / stats / 账号统计…），逐个改必然漏掉几处，
-- 而漏掉的那几处会继续虚报。在写入侧冲平，所有既有查询自动正确。
-- 审计不受损：原始扣费额 = total_cost + refunded_credits，可精确还原。
--
-- account_stats_cost（我们付给上游的成本）一并归零：上游对失败任务不计费
-- ——2026-09-10 实测两笔失败任务，上游 used_balance 分文未动。不归零的话，
-- 毛利报表会在这些行上显示负毛利。
--
-- media_task_charges.request_id 是关联键：退款发生在查询接口，那时只有上游
-- task_id；而 usage_logs 不存 task_id，只有 request_id。绑定扣费额时顺手把
-- request_id 一起记下，退款时才回得去。

ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMPTZ;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS refunded_credits NUMERIC(20, 8);

ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS request_id VARCHAR(64);

-- 只索引已退款的行：退款是少数派，部分索引比全表索引小得多，
-- 而「列出已退款记录」正是唯一需要走索引的查询。
CREATE INDEX IF NOT EXISTS idx_usage_logs_refunded_at
    ON usage_logs (refunded_at)
    WHERE refunded_at IS NOT NULL;
