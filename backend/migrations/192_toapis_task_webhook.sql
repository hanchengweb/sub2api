-- 上游异步任务 Webhook 所需的两张支撑结构。
--
-- 背景：失败任务的退款此前只能靠客户端轮询状态接口触发，而生产环境该接口的历史
-- 调用次数是 0——机制建好了但从不会被触发。toAPI 支持任务完成/失败的 Webhook
-- 推送，接上之后失败即退，不再依赖客户端行为。

-- 1) 扣费记录补 task_id：Webhook 只带上游 task_id，而 task_key 是
--    hash(user_id, api_key_id, task_id)，无法从 task_id 反推。
ALTER TABLE media_task_charges ADD COLUMN IF NOT EXISTS task_id VARCHAR(200);

-- 上游 task_id 全局唯一（tsk_img_/tsk_vid_ + ULID），可作唯一索引。
-- 建成部分索引：历史行的 task_id 为 NULL，不参与唯一性约束。
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_task_charges_task_id
    ON media_task_charges (task_id) WHERE task_id IS NOT NULL;

-- 2) Webhook 事件幂等表。
--
-- toAPI 是「至少一次」投递：同一事件重试时 id 不变，失败后按
-- 10s/30s/2m/10m/1h/6h/24h 重投。必须先持久化 event id 再执行退款，
-- 否则一次重投就是一次重复退款。
CREATE TABLE IF NOT EXISTS webhook_events (
    id           BIGSERIAL PRIMARY KEY,
    provider     VARCHAR(50)  NOT NULL,
    event_id     VARCHAR(200) NOT NULL,
    event_type   VARCHAR(100) NOT NULL,
    task_id      VARCHAR(200),
    processed_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- 幂等键：同一 provider 下 event_id 唯一。插入冲突即代表重复投递，直接返回 2xx。
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_events_provider_event
    ON webhook_events (provider, event_id);

CREATE INDEX IF NOT EXISTS idx_webhook_events_created_at
    ON webhook_events (created_at);
