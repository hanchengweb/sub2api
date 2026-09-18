-- toAPI 视频模型上架到「风合智联网页端」（分组 4）。
--
-- 成本口径：toapis.com/dashboard/pricing 的**人民币**价目表（2026-09-18）。
-- 我们做的是国内生意，跟 toAPI 按 ¥ 结算；那个 /v1/pricing 接口返回的 USD
-- 是国际站视角，不是我们的结算货币，别拿它换算。
--
-- 卖价 = 成本 + 29 积分，与图像「成本 + 29 积分/张」同一条规则，单位换成每秒。
-- ¥1 = 100 积分。
--
-- 档位按价目表「支持分辨率」列配。没配到的档位不会掉到代码默认价——
-- VideoModelPricesFor 会补成该模型已配档位里最贵的那个（见 group.go）。
--
-- ⚠️ 顺序要求：本脚本先写价、再写映射和路由、**最后**才进闸门。
-- 反过来会有一段时间模型可调用但没有价，计费侧会静默算 0 —— 白送视频。
-- （openai_gateway_usage.go 里那段注释写明了这是刻意的 fail-open。）

BEGIN;

-- 1) 按模型 × 分辨率的每秒卖价（积分/秒）
UPDATE groups SET video_model_prices = '{
  "grok-video-1.5":    {"480p": 37,    "720p": 37},
  "kling-v3":          {"720p": 71},
  "kling-v3-omni":     {"720p": 71},
  "seedance-2-fast":   {"480p": 85,    "720p": 85},
  "seedance-2":        {"480p": 74,    "720p": 119,  "1080p": 254, "4k": 529},
  "seedance-2-5":      {"480p": 89.27, "720p": 165},
  "veo3.1-fast":       {"720p": 99,    "1080p": 99,  "4k": 99},
  "MiniMax-H3":        {"480p": 109,   "720p": 109,  "1080p": 109, "4k": 109},
  "gemini_omni_flash": {"720p": 134,   "1080p": 134}
}'::jsonb
WHERE id = 4;

-- 2) 账号侧模型映射：对外名 = 上游名，但映射表里没有不等于透传，必须显式写
UPDATE accounts
SET credentials = jsonb_set(
      credentials, '{model_mapping}',
      COALESCE(credentials->'model_mapping', '{}'::jsonb) || '{
        "kling-v3":          "kling-v3",
        "kling-v3-omni":     "kling-v3-omni",
        "seedance-2":        "seedance-2",
        "seedance-2-fast":   "seedance-2-fast",
        "seedance-2-5":      "seedance-2-5",
        "veo3.1-fast":       "veo3.1-fast",
        "MiniMax-H3":        "MiniMax-H3",
        "gemini_omni_flash": "gemini_omni_flash"
      }'::jsonb)
WHERE id = 6;

-- 3) 路由：与 t-grok-video-1.5 同一条链路（target_platform=grok 走的是
--    toAPI 的 /v1/videos/generations，适配器对模型名是透传的）
INSERT INTO composite_model_routes
  (group_id, public_model, match_type, target_platform, upstream_model, endpoint, priority, enabled, notes, created_at, updated_at)
SELECT 4, m.model, 'exact', 'grok', m.model, 'any', 1, true, 'toAPI 视频模型（2026-09-18 上架）', NOW(), NOW()
FROM (VALUES
  ('kling-v3'), ('kling-v3-omni'), ('seedance-2'), ('seedance-2-fast'),
  ('seedance-2-5'), ('veo3.1-fast'), ('MiniMax-H3'), ('gemini_omni_flash')
) AS m(model)
WHERE NOT EXISTS (
  SELECT 1 FROM composite_model_routes r
  WHERE r.group_id = 4 AND r.public_model = m.model AND r.deleted_at IS NULL);

-- 4) 可见性闸门 —— 必须最后做
UPDATE groups
SET models_list_config = jsonb_set(
      COALESCE(models_list_config, '{"enabled":true}'::jsonb), '{models}',
      COALESCE(models_list_config->'models', '[]'::jsonb) || (
        SELECT COALESCE(jsonb_agg(m.model), '[]'::jsonb) FROM (VALUES
          ('kling-v3'), ('kling-v3-omni'), ('seedance-2'), ('seedance-2-fast'),
          ('seedance-2-5'), ('veo3.1-fast'), ('MiniMax-H3'), ('gemini_omni_flash')
        ) AS m(model)
        WHERE NOT COALESCE((SELECT models_list_config->'models' FROM groups WHERE id = 4), '[]'::jsonb) ? m.model))
WHERE id = 4;

COMMIT;

-- 核对
SELECT jsonb_pretty(video_model_prices) AS 卖价积分每秒 FROM groups WHERE id = 4;
SELECT u.model AS 闸门内视频模型
FROM groups g CROSS JOIN LATERAL jsonb_array_elements_text(g.models_list_config->'models') AS u(model)
WHERE g.id = 4 AND u.model ~* 'video|seedance|kling|veo|minimax|omni' ORDER BY 1;
