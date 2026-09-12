-- 成为代理（合作申请）。
--
-- 此前「成为代理」页是纯前端：表单只在本地拼一段文案让用户自己复制走，
-- 提交按钮不发任何请求——申请进不到平台，没人知道有人申请过。
--
-- direction 是三个合作方向之一，与前端 AgencyView 的 directions 一一对应：
--   channel     渠道代理
--   integration 技术集成
--   delivery    交付伙伴
-- 用字符串而不是枚举：以后加方向只改前端和这里的 CHECK，不动表结构。
--
-- 不做唯一约束：同一个人可以就不同方向分别申请，也可能第一次没谈成再来一次。
-- 刷单由服务层限流（每人待处理上限），不是靠约束挡。

CREATE TABLE IF NOT EXISTS agency_applications (
    id           BIGSERIAL PRIMARY KEY,
    user_id      BIGINT       NOT NULL,
    direction    VARCHAR(20)  NOT NULL,
    contact_name VARCHAR(80)  NOT NULL,
    email        VARCHAR(254) NOT NULL,
    company      VARCHAR(120) NOT NULL DEFAULT '',
    scenario     TEXT         NOT NULL,
    -- pending 待处理 / contacted 已联系 / accepted 已通过 / rejected 未通过
    status       VARCHAR(20)  NOT NULL DEFAULT 'pending',
    -- 平台侧的处理备注，只有管理员看得到
    admin_note   TEXT         NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT agency_applications_direction_check
        CHECK (direction IN ('channel', 'integration', 'delivery')),
    CONSTRAINT agency_applications_status_check
        CHECK (status IN ('pending', 'contacted', 'accepted', 'rejected'))
);

-- 用户看自己的申请、管理员按时间倒序翻列表，两条路径各一个索引。
CREATE INDEX IF NOT EXISTS idx_agency_applications_user
    ON agency_applications (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agency_applications_status
    ON agency_applications (status, created_at DESC);
