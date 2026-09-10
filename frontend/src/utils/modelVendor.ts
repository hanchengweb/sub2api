/**
 * 模型 → 供应商的判定，全站唯一一份。
 *
 * 抽出来是因为它已经在两处各写了一遍（厂商标识组件、在线使用页的分组下拉），
 * 再加第三处必然漂移——一旦规则不一致，就会出现「豆包分组里挂着 Google 标」
 * 这种自相矛盾的界面。
 *
 * 判定依据是**模型名**而不是 platform 字段：线上组 4 是 composite 路由，
 * platform 一律是 openai/anthropic，跟真正的模型厂商对不上。
 */

export interface ModelVendor {
  key: string
  /** 展示名，同时用作分组标题 */
  label: string
}

export const VENDOR_DEEPSEEK: ModelVendor = { key: 'deepseek', label: 'DeepSeek' }
export const VENDOR_OPENAI: ModelVendor = { key: 'openai', label: 'OpenAI' }
export const VENDOR_DOUBAO: ModelVendor = { key: 'doubao', label: '字节豆包 Doubao' }
export const VENDOR_XAI: ModelVendor = { key: 'xai', label: 'xAI Grok' }
export const VENDOR_GEMINI: ModelVendor = { key: 'gemini', label: 'Google Gemini' }
export const VENDOR_NANO_BANANA: ModelVendor = { key: 'nanobanana', label: 'Google Nano Banana' }
export const VENDOR_FLUX: ModelVendor = { key: 'flux', label: 'Black Forest Labs' }
export const VENDOR_VIDU: ModelVendor = { key: 'vidu', label: '智谱清影 Vidu' }
export const VENDOR_QWEN: ModelVendor = { key: 'qwen', label: '阿里云通义 Qwen' }
export const VENDOR_UNKNOWN: ModelVendor = { key: 'generic', label: '其他' }

/**
 * 判定顺序有讲究：
 *   - nano banana 必须排在 gemini 之前——它同属 Google 但有自己的品牌色，
 *     先判 gemini 会把它整个吞掉；
 *   - gpt/dall 放最后——doubao 曾借名 gpt-image-1 过生图接口的闸门，
 *     先判 seedream 才不会把它误判成 OpenAI。
 */
export function resolveModelVendor(model: string): ModelVendor {
  const n = model.toLowerCase().trim()
  if (!n) return VENDOR_UNKNOWN
  if (n.includes('deepseek')) return VENDOR_DEEPSEEK
  if (n.includes('doubao') || n.includes('seedream')) return VENDOR_DOUBAO
  if (n.includes('grok')) return VENDOR_XAI
  if (n.includes('nano_banana') || n.includes('nano-banana')) return VENDOR_NANO_BANANA
  if (n.includes('gemini')) return VENDOR_GEMINI
  if (n.startsWith('flux')) return VENDOR_FLUX
  if (n.startsWith('vidu')) return VENDOR_VIDU
  if (n.startsWith('qwen')) return VENDOR_QWEN
  if (n.includes('gpt') || n.includes('dall')) return VENDOR_OPENAI
  return VENDOR_UNKNOWN
}

export type ModelKind = 'text' | 'image' | 'video'

/**
 * 判定模型类型。
 *
 * 视频按模型名判而不是按 billing_mode：线上视频模型的 billing_mode 配的是
 * image（它走图片接口那条闸门），只看计费模式会把视频混进生图。
 */
export function resolveModelKind(model: string, billingMode?: string | null): ModelKind {
  if (/video/i.test(model)) return 'video'
  return billingMode && billingMode !== 'token' ? 'image' : 'text'
}
