import type { PlazaModel } from '@/api/modelPlaza'
import { BILLING_MODE_TOKEN } from '@/constants/channel'

export function plazaSection(model: PlazaModel): 'text' | 'image' | 'video' {
  // 配了视频每秒价就是视频模型 —— 这是数据，不是猜名字。
  // kling-v3 / seedance-2 / MiniMax-H3 名字里都没有 video，只按名字判会把它们
  // 分到文本区去（它们没有渠道定价行，billing_mode 也是空的）。
  if (model.video_pricing) return 'video'
  if (/video/i.test(model.name)) return 'video'
  return (model.pricing?.billing_mode || BILLING_MODE_TOKEN) === BILLING_MODE_TOKEN ? 'text' : 'image'
}

// Keep the displayed section and official-price ordering stable across page boundaries.
export function comparePlazaModels(a: PlazaModel, b: PlazaModel): number {
  const order = { text: 0, image: 1, video: 2 }
  const section = order[plazaSection(a)] - order[plazaSection(b)]
  if (section) return section
  const pa = a.official_pricing?.output_price ?? null
  const pb = b.official_pricing?.output_price ?? null
  if (pa != null && pb != null && pa !== pb) return pb - pa
  if (pa != null && pb == null) return -1
  if (pa == null && pb != null) return 1
  return a.name.localeCompare(b.name)
}
