import type { PlazaModel } from '@/api/modelPlaza'
import { resolveModelKind } from './modelVendor'

export function plazaSection(model: PlazaModel): 'text' | 'image' | 'video' {
  return resolveModelKind(model.name, model.pricing?.billing_mode, Boolean(model.video_pricing))
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
