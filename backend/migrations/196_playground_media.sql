-- 在线使用的生成结果转存。
--
-- 为什么必须转存而不是把上游地址存进库：上游结果 **24 小时后过期**
-- （2026-09-11 实测 created_at 16:00:32 → expires_at 次日 16:00:49）。
-- 只存地址的话，过了一天连同一台电脑同一浏览器也打不开——用户会理解成
-- 「平台把我生成的图弄丢了」。所以要把字节本身落到我们自己的盘上。
--
-- stored_path 存相对 data 目录的相对路径（如 media/6/202609/ab12.png），
-- 不存绝对路径：部署目录换了、容器挂载点变了，整表就全失效。
--
-- (user_id, source_url) 唯一：同一个任务前端会反复轮询，也可能重进页面再取一次，
-- 靠这个约束保证同一张图只落一次盘，不会每轮询一次就多存一份。
--
-- 不设过期清理：按产品要求永久保留。实测 1K 图 1.45MB、8 秒 720p 视频 4.3MB，
-- 当前约 20 张/天 ≈ 10.6GB/年，盘还有 134GB，十年量级无压力。
-- 真正的保险在代码里：剩余空间低于阈值就停止转存并回退到上游地址，不把盘写死。

CREATE TABLE IF NOT EXISTS playground_media (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT       NOT NULL,
    task_id     VARCHAR(200) NOT NULL DEFAULT '',
    kind        VARCHAR(10)  NOT NULL,
    source_url  TEXT         NOT NULL,
    stored_path TEXT         NOT NULL,
    mime_type   VARCHAR(100) NOT NULL,
    size_bytes  BIGINT       NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT playground_media_kind_check CHECK (kind IN ('image', 'video')),
    CONSTRAINT playground_media_size_check CHECK (size_bytes > 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_playground_media_user_source
    ON playground_media (user_id, md5(source_url));

CREATE INDEX IF NOT EXISTS idx_playground_media_user_created
    ON playground_media (user_id, created_at DESC);
