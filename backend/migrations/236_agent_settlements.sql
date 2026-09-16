-- 代理返现结算批次。
--
-- 口径（韩诚 2026-09-16 定）：客户走零售价、扣自己的余额，平台按客户的
-- 实际消费给代理返现：
--   文本（billing_mode = token 或空）：消费额 × (1 - text_discount)
--   多模态（per_request / image / video）：计费单元数 × 每单元让利
--
-- 为什么按消费而不是按充值：两套规则是按模型分的，充值额不分模型。
-- 生产上 34 个在售模型里 31 个是图像/视频，按充值额一刀切返 20% 的话，
-- 贵模型（gpt-image-2-official 4K·高 ￥2.94/次，本该只让 3.4%）上平台
-- 每笔少赚 16.6%，而那正是高客单价场景。
--
-- 为什么不复用 user_affiliate_ledger：那张表是按单笔充值订单计提的
-- （source_order_id 外键指向 payment_orders），记不下结算周期、
-- 覆盖区间和分类明细。返现余额仍然落到 user_affiliates.aff_quota，
-- 这样代理的「转余额」直接复用现成功能。

CREATE TABLE IF NOT EXISTS agent_settlements (
    id            BIGSERIAL PRIMARY KEY,
    agent_user_id BIGINT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,

    -- 覆盖的 usage_logs 区间，左开右闭 (from, to]。
    --
    -- 用自增 id 而不是时间窗做游标：按时间切会漏单——一条请求的
    -- created_at 落在窗口内、但写库晚于结算跑的时刻，下次按时间又跳过了它。
    -- id 单调递增，"上次结算到哪"是个确定的位置。
    from_usage_log_id BIGINT NOT NULL,
    to_usage_log_id   BIGINT NOT NULL,
    period_start      TIMESTAMPTZ NOT NULL,
    period_end        TIMESTAMPTZ NOT NULL,

    -- 分类明细。返现总额对不上时，要能看出是文本还是多模态那边算错了。
    text_cost_credits         NUMERIC(20,8) NOT NULL DEFAULT 0,
    text_rebate_credits       NUMERIC(20,8) NOT NULL DEFAULT 0,
    -- 多模态计费单元数：按 image_count 而不是调用次数。
    -- 批发价那边让利是减在单价上的（一次生成 4 张图就让 4 毛），
    -- 结算按调用次数算的话，两套口径在多图请求上会对不上。
    multimodal_units          BIGINT        NOT NULL DEFAULT 0,
    multimodal_rebate_credits NUMERIC(20,8) NOT NULL DEFAULT 0,
    total_rebate_credits      NUMERIC(20,8) NOT NULL DEFAULT 0,

    -- 结算时生效的规则快照。
    -- 方案随时可改，改完之后历史结算必须仍然解释得清当时按什么算的——
    -- 只存方案 id 的话，改一次方案，所有历史账目的依据就都变了。
    text_discount            NUMERIC(6,4)  NOT NULL,
    multimodal_deduction_cny NUMERIC(12,4) NOT NULL,
    credits_per_cny          NUMERIC(20,8) NOT NULL,

    customer_count INT         NOT NULL DEFAULT 0,
    status         VARCHAR(20) NOT NULL DEFAULT 'settled',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT agent_settlements_status_check
        CHECK (status IN ('settled', 'reversed')),
    CONSTRAINT agent_settlements_range_check
        CHECK (to_usage_log_id >= from_usage_log_id)
);

-- 结算时要查"这个代理上次结算到哪"，就是这条索引的用途。
CREATE INDEX IF NOT EXISTS idx_agent_settlements_agent
    ON agent_settlements (agent_user_id, to_usage_log_id DESC);

-- 同一个代理、同一个区间只能结算一次。
-- 结算任务重跑（超时重试、手工补跑）时靠它兜底，而不是靠调用方记得别重复跑。
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_settlements_unique_range
    ON agent_settlements (agent_user_id, from_usage_log_id, to_usage_log_id)
    WHERE status = 'settled';

COMMENT ON TABLE  agent_settlements IS '代理返现结算批次：按客户实际消费分类计算';
COMMENT ON COLUMN agent_settlements.to_usage_log_id IS '本批次结算到的 usage_logs.id（含），下批次从这里往后接';
COMMENT ON COLUMN agent_settlements.multimodal_units IS '多模态计费单元数（image_count 之和），与批发价按单价让利的口径对齐';
COMMENT ON COLUMN agent_settlements.text_discount IS '结算时的文本折扣快照，不是当前方案值';
