-- 代理体系：代理档案 + 代理定价方案。
--
-- 背景：198 迁移建了 agency_applications（合作申请），但 status='accepted'
-- 至今只是个状态字——审核通过之后平台侧什么都不会发生，既没有代理身份，
-- 也没有对应的价格。这两张表补上「通过之后」的落点。
--
-- 为什么不往 users.role 加第三个值：
--   role 只有 admin/user 两个值，且被 middleware/admin_only.go 当作管理员判定，
--   service/admin_user.go 的 normalizeUserRole 也硬校验只认这两个。
--   加值等于在鉴权链路上开口子。代理仍然是普通 user，身份由本表表达。
--
-- 两种代理模式（对应前端三个合作方向）：
--   affiliate 分佣   ← channel 渠道推广
--     客户按零售价付钱给平台，代理拿返利。复用既有 user_affiliates 那套。
--   reseller  转售   ← integration 技术集成 / delivery 客户交付
--     代理按批发价拿货，进专属分组，自己向客户收钱赚差价。
--
-- 这两种模式互斥：同一个代理同时吃返利和差价，平台会在同一笔钱上被削两次。

CREATE TABLE IF NOT EXISTS agent_pricing_plans (
    id   BIGSERIAL    PRIMARY KEY,
    name VARCHAR(100) NOT NULL,

    -- 文本类（channel_model_pricing.billing_mode = 'token'）：
    --   代理价 = 零售价 × text_discount。0.8 = 八折，代理有 20% 的利润空间。
    text_discount NUMERIC(6,4) NOT NULL DEFAULT 0.8000,

    -- 多模态（billing_mode 为 per_request / image / video）：
    --   代理价 = 零售价 - 每次让利金额。0.1 = 每次调用让一毛，代理赚一毛。
    --
    -- 存人民币而不是积分：运营填的是「一毛」这个业务口径。
    -- ￥1 ↔ 积分的换算率由 BALANCE_RECHARGE_MULTIPLIER 设置决定（默认 100），
    -- 换算率日后调整时方案不用跟着改，重新生成一次即可。
    multimodal_deduction_cny NUMERIC(12,4) NOT NULL DEFAULT 0.1000,

    -- 下限保护：算出来低于成本价的条目不生成，避免按方案配出倒贴价。
    -- 成本来自 channel_account_stats_model_pricing；取不到成本的条目照常生成并告警。
    enforce_cost_floor BOOLEAN NOT NULL DEFAULT TRUE,

    status     VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- 折扣必须落在 (0,1]：大于 1 就成了给代理加价，几乎一定是填错了。
    CONSTRAINT agent_pricing_plans_text_discount_check
        CHECK (text_discount > 0 AND text_discount <= 1),
    CONSTRAINT agent_pricing_plans_deduction_check
        CHECK (multimodal_deduction_cny >= 0),
    CONSTRAINT agent_pricing_plans_status_check
        CHECK (status IN ('active', 'archived'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_pricing_plans_name
    ON agent_pricing_plans (name);

COMMENT ON TABLE  agent_pricing_plans IS '代理定价方案：生成面向代理的批发价时套用的规则';
COMMENT ON COLUMN agent_pricing_plans.text_discount IS '文本类模型折扣，代理价 = 零售价 × 本值';
COMMENT ON COLUMN agent_pricing_plans.multimodal_deduction_cny IS '多模态每次让利（人民币元），代理价 = 零售价 - 本值折算的积分';
COMMENT ON COLUMN agent_pricing_plans.enforce_cost_floor IS '开启后低于成本价的条目不生成';

-- 默认方案：韩诚 2026-09-16 定的口径（文本八折 / 多模态每次让一毛）。
INSERT INTO agent_pricing_plans (name, text_discount, multimodal_deduction_cny)
VALUES ('默认代理方案', 0.8000, 0.1000)
ON CONFLICT (name) DO NOTHING;

-- 代理档案。一个用户最多一份：即使他用两个方向分别申请通过，
-- 也仍然是同一个代理身份，mode 以先通过的那次为准，之后由管理员调整。
CREATE TABLE IF NOT EXISTS agent_profiles (
    user_id        BIGINT      PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    -- 来源申请。申请被删时保留档案：代理身份已经生效，不该因为清理申请记录而消失。
    application_id BIGINT      REFERENCES agency_applications(id) ON DELETE SET NULL,
    mode           VARCHAR(20) NOT NULL,
    direction      VARCHAR(20) NOT NULL,
    status         VARCHAR(20) NOT NULL DEFAULT 'active',

    -- 转售模式专用：代理的专属分组与定价方案。分佣模式下均为 NULL。
    pricing_plan_id   BIGINT REFERENCES agent_pricing_plans(id) ON DELETE SET NULL,
    reseller_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,

    -- 平台内部备注，不对代理展示。
    note         TEXT        NOT NULL DEFAULT '',
    activated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT agent_profiles_mode_check
        CHECK (mode IN ('affiliate', 'reseller')),
    CONSTRAINT agent_profiles_direction_check
        CHECK (direction IN ('channel', 'integration', 'delivery')),
    CONSTRAINT agent_profiles_status_check
        CHECK (status IN ('active', 'suspended', 'terminated'))
);

-- 管理员按状态翻代理列表。
CREATE INDEX IF NOT EXISTS idx_agent_profiles_status
    ON agent_profiles (status, created_at DESC);

-- 改定价方案时要找出受影响的代理，这条索引是那次查询用的。
CREATE INDEX IF NOT EXISTS idx_agent_profiles_pricing_plan
    ON agent_profiles (pricing_plan_id)
    WHERE pricing_plan_id IS NOT NULL;

COMMENT ON TABLE  agent_profiles IS '代理档案：审核通过后的代理身份，不占用 users.role';
COMMENT ON COLUMN agent_profiles.mode IS 'affiliate 分佣（客户按零售价付、代理拿返利）/ reseller 转售（代理按批发价拿货赚差价）';
COMMENT ON COLUMN agent_profiles.reseller_group_id IS '转售代理的专属分组；分佣模式为 NULL';
