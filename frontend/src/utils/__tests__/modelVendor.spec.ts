import { describe, expect, it } from 'vitest'
import { resolveModelVendor, resolveModelKind } from '../modelVendor'

describe('resolveModelVendor', () => {
  it('按模型名判到正确的供应商', () => {
    const cases: Array<[string, string]> = [
      ['deepseek-v4-flash', 'DeepSeek'],
      ['t-gpt-image-2-vip', 'OpenAI'],
      ['t-grok-video-1.5', 'xAI Grok'],
      ['doubao-seedream-5-0-pro', '字节豆包 Doubao'],
      ['flux-2-pro', 'Black Forest Labs'],
      ['gemini-3-pro-image-official', 'Google Gemini'],
      ['viduq2-fast', '智谱清影 Vidu'],
      ['qwen-image-3.0', '阿里云通义 Qwen'],
      ['some-unknown-model', '其他'],
      ['', '其他'],
    ]
    for (const [model, want] of cases) {
      expect(resolveModelVendor(model).label, model).toBe(want)
    }
  })

  /** nano banana 同属 Google 但有自己的品牌色，必须排在 gemini 判定之前。 */
  it('nano banana 不被 gemini 吞掉', () => {
    expect(resolveModelVendor('nano_banana_2').label).toBe('Google Nano Banana')
    expect(resolveModelVendor('nano_banana_pro').label).toBe('Google Nano Banana')
  })

  /** 豆包曾借名 gpt-image-1 过生图接口的闸门，不能被误判成 OpenAI。 */
  it('借名的豆包仍归字节', () => {
    expect(resolveModelVendor('v-doubao-seedream-5-0-pro').label).toBe('字节豆包 Doubao')
  })
})

describe('resolveModelKind', () => {
  it('视频按模型名判，不看 billing_mode', () => {
    // 线上视频模型的 billing_mode 配的就是 image（走图片接口那条闸门）
    expect(resolveModelKind('t-grok-video-1.5', 'image')).toBe('video')
  })

  it('token 计费归文本，其余归生图', () => {
    expect(resolveModelKind('deepseek-v4-flash', 'token')).toBe('text')
    expect(resolveModelKind('t-gpt-image-2', 'image')).toBe('image')
    expect(resolveModelKind('some-model', null)).toBe('text')
  })
})
