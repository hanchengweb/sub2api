-- 计费档位约束放行 1.5K。
--
-- 2026-09-18 在代码里加了 1.5K 计费档（image_billing_size.go），但这条约束的正则
-- 只认 1K/2K/4K。于是 1.5K 的图：余额照扣（扣费在写记录之前），usage_logs 却插不进去——
-- 「扣了费但没有记录」，后台看不到、对账对不上。B 端租户的出图尺寸配的正是 1.5K。
--
-- 只放宽、不收紧；NOT VALID 与原约束一致，不扫描存量数据。
-- 可重放：先 DROP IF EXISTS 再 ADD，生产上已手工执行过同样的语句也不会失败。
ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS usage_logs_image_billing_size_check;
ALTER TABLE usage_logs ADD CONSTRAINT usage_logs_image_billing_size_check CHECK (
    image_count <= 0
    OR billing_mode = 'video'
    OR COALESCE(video_count, 0) > 0
    OR (
        image_size IS NOT NULL
        AND (
            image_size = 'mixed'
            OR image_size ~ '^(1|1\.5|2|4)K(·(低|中|高))?$'
        )
    )
) NOT VALID;
