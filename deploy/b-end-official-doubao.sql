-- B 端（组织）出图改走火山方舟官方的豆包 5.0 Pro，1.5K 档按 59 积分收。C 端不受影响。
--
-- 前置：运行中的镜像已包含组织改道代码（settings.organization_model_overrides，
--       见 backend/internal/service/organization_model_override.go）。
-- 可重放：每一步都是幂等的。
--
-- 分两段执行：
--   第一段（路由 + 价格）执行完，等渠道定价缓存过期（channelCacheTTL = 10 分钟），
--   再执行第二段（改道表，写入即生效）。顺序反了的话，改道生效后的头几张图
--   还读不到 1.5K 档，会落到这一行的默认价 89。
--
-- 回滚（一分钟内生效，组织请求回到和 C 端相同的 toAPI 路由）：
--   DELETE FROM settings WHERE key = 'organization_model_overrides';

-- ============ 第一段：路由 + 价格 ============
BEGIN;

-- 1) 官方路由改成同名直通。
--    原来是 v-doubao-seedream-5-0-pro → gpt-image-1，再由账号 2 把 gpt-image-1 映射到官方模型。
--    问题有两个：计费和日志记成 gpt-image-1（一个 GPT 模型名）；分组 4 里只要再有一个账号
--    认 gpt-image-1，B 端的图就可能被派到 GPT 出图账号上。账号 2 本身就映射了
--    v-doubao-seedream-5-0-pro → doubao-seedream-5-0-pro-260628，不需要借道。
UPDATE composite_model_routes
   SET upstream_model = 'v-doubao-seedream-5-0-pro', updated_at = now()
 WHERE group_id = 4
   AND public_model = 'v-doubao-seedream-5-0-pro'
   AND deleted_at IS NULL
   AND upstream_model <> 'v-doubao-seedream-5-0-pro';

-- 2) 官方定价行加 1.5K 档。火山官方文档：1.5K 与 1K 同价。
--    没有这一档时 1.5K 请求按「档位名 → token 区间 → 行默认价」兜底，会被收 89。
INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, tier_label, per_request_price, sort_order)
SELECT p.id, 0, '1.5K', 59, 1
  FROM channel_model_pricing p
 WHERE p.channel_id = 1
   AND p.models @> '["v-doubao-seedream-5-0-pro"]'::jsonb
   AND NOT EXISTS (
         SELECT 1 FROM channel_pricing_intervals i
          WHERE i.pricing_id = p.id AND i.tier_label = '1.5K');

--    后台展示顺序：1K、1.5K、2K、4K（只影响列表顺序，不影响按档位名匹配）。
UPDATE channel_pricing_intervals i
   SET sort_order = CASE i.tier_label
                      WHEN '1K'   THEN 0
                      WHEN '1.5K' THEN 1
                      WHEN '2K'   THEN 2
                      WHEN '4K'   THEN 3
                      ELSE i.sort_order
                    END,
       updated_at = now()
  FROM channel_model_pricing p
 WHERE i.pricing_id = p.id
   AND p.channel_id = 1
   AND p.models @> '["v-doubao-seedream-5-0-pro"]'::jsonb;

COMMIT;

-- ============ 第二段：改道表（至少等 10 分钟再执行） ============
--    组织身份的请求：doubao-seedream-5-0-pro → v-doubao-seedream-5-0-pro，size 1.5K → 1536x1536。
--    换成像素是为了计费稳定：官方的 1.5K 是档位写法，宽高比由模型按提示词决定，
--    竖图会回 1344x1792 这类尺寸，按最长边分档会被当成 2K；传像素就原样出 1536x1536。
INSERT INTO settings (key, value, updated_at)
VALUES ('organization_model_overrides',
        '{"doubao-seedream-5-0-pro": {"model": "v-doubao-seedream-5-0-pro", "sizes": {"1.5K": "1536x1536"}}}',
        now())
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
