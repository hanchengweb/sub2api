-- 异步媒体任务（图片 / 视频）的扣费记录，用于任务失败时原额退回。
--
-- 网关在上游返回 200（任务 pending）时就已全额扣费，任务可能之后才失败，而上游对
-- 失败任务不计费。没有这条记录就无从知道该退多少，用户会为失败的任务付钱。
--
-- 原先这份记录放在带 24h TTL 的 Redis 里，有三条泄漏路径：客户端始终不来轮询、
-- TTL 先到期、Redis 重启。落库后不再受这些影响。
--
-- task_key 是 hash(user_id, api_key_id, task_id)，与「任务→账号绑定」分属不同键空间。
-- 幂等由 refunded_at 保证：退款走
--     UPDATE ... SET refunded_at = NOW() WHERE task_key = $1 AND refunded_at IS NULL
--     RETURNING credits
-- 单条语句原子完成「取出并标记」，并发轮询只有一条能拿到金额。

CREATE TABLE IF NOT EXISTS media_task_charges (
    id          BIGSERIAL PRIMARY KEY,
    task_key    VARCHAR(128) NOT NULL,
    user_id     BIGINT NOT NULL,
    api_key_id  BIGINT NOT NULL,
    group_id    BIGINT,
    credits     DECIMAL(20,10) NOT NULL CHECK (credits > 0),
    refunded_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 唯一键：同一任务重复绑定按 upsert 处理，不产生第二条可退记录。
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_task_charges_task_key
    ON media_task_charges (task_key);

-- 清理用：只需保留任务仍可能失败的那段时间。
CREATE INDEX IF NOT EXISTS idx_media_task_charges_created_at
    ON media_task_charges (created_at);
