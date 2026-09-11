-- 在线使用的会话搬到服务端，支持换设备 / 重新登录后继续看历史。
--
-- 在此之前会话只存浏览器 localStorage：换台电脑连会话列表都是空的。
-- 196 那次把生成结果的字节落了盘，解决了「隔天图没了」；这次解决「换设备什么都没有」。
--
-- id 沿用前端生成的值（uuid 或 时间戳-随机串）而不是自增主键：
-- 前端本来就要在本地先建好会话再发请求，用服务端 id 就得等一次往返，
-- 而且上传失败时本地那条会话会没有 id。客户端生成 id 让同步变成幂等 upsert。
-- 客户端能控制 id，所以必须按 (user_id, id) 隔离——只按 id 查会让人读到别人的会话。
--
-- 消息整条随会话一起覆盖写（见仓储的 ReplaceMessages），不做逐条增量：
-- 流式对话每秒要改好几次内容，逐条 diff 的复杂度远超收益，
-- 而单会话消息量有上限（前端 200 条），整体覆盖的代价可以接受。

CREATE TABLE IF NOT EXISTS playground_conversations (
    id         VARCHAR(64)  NOT NULL,
    user_id    BIGINT       NOT NULL,
    title      VARCHAR(200) NOT NULL DEFAULT '',
    mode       VARCHAR(10)  NOT NULL DEFAULT 'chat',
    model      VARCHAR(200) NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    CONSTRAINT playground_conversations_mode_check CHECK (mode IN ('chat', 'image', 'video'))
);

CREATE INDEX IF NOT EXISTS idx_playground_conversations_user_updated
    ON playground_conversations (user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS playground_messages (
    id              VARCHAR(64) NOT NULL,
    conversation_id VARCHAR(64) NOT NULL,
    user_id         BIGINT      NOT NULL,
    -- 位置显式存一列：靠 created_at 排序在同一毫秒内建的两条消息会顺序不定，
    -- 而「提问在回复前面」是这个界面最基本的正确性。
    position        INTEGER     NOT NULL DEFAULT 0,
    role            VARCHAR(10) NOT NULL,
    content         TEXT        NOT NULL DEFAULT '',
    -- 媒体地址存数组：一次生图可以出多张，而且转存后地址会从上游换成本站的
    media_urls      JSONB       NOT NULL DEFAULT '[]'::jsonb,
    media_kind      VARCHAR(10),
    task_id         VARCHAR(200),
    error           TEXT,
    needs_top_up    BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, conversation_id, id),
    CONSTRAINT playground_messages_role_check CHECK (role IN ('user', 'assistant', 'system'))
);

CREATE INDEX IF NOT EXISTS idx_playground_messages_conversation
    ON playground_messages (user_id, conversation_id, position);
