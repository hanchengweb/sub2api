-- 放宽 usage_logs.image_size 的取值，容纳「清晰度·质量」档位标签。
--
-- 上游按「清晰度 × 质量」九档收费，计费档位标签因此从 "2K" 变成 "2K·高"
-- （见 service/image_billing_size.go 的 ImageBillingTierWithQuality）。
-- 原约束把取值写死成 1K/2K/4K/mixed，新标签一律违反约束，导致**整条 usage_log
-- 插入失败**——线上表现是生图调用返回 200、却一条用量记录都不写，扣费与成本
-- 都无从统计。是实测拿到 pq 报错才定位到这里的。
--
-- 用正则而不是继续枚举：质量档以后可能增删，枚举一次就要改一次库。
ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS usage_logs_image_billing_size_check;

ALTER TABLE usage_logs ADD CONSTRAINT usage_logs_image_billing_size_check CHECK (
  image_count <= 0
  OR billing_mode::text = 'video'
  OR COALESCE(video_count, 0) > 0
  OR (
    image_size IS NOT NULL
    AND (
      image_size::text = 'mixed'
      -- 1K / 2K / 4K，可选带「·低」「·中」「·高」后缀
      OR image_size::text ~ '^[124]K(·(低|中|高))?$'
    )
  )
) NOT VALID;
