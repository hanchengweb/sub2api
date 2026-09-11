import { describe, expect, it } from 'vitest'
import { resolveModelVersion } from '../modelDisplayName'

describe('模型版本号', () => {
  it('两个 deepseek 别名都标 V4.1（实测均解析到上游 deepseek-flash）', () => {
    expect(resolveModelVersion('deepseek-v4-flash')).toBe('DeepSeek-V4.1-Flash')
    expect(resolveModelVersion('deepseek-v4-flash-vision-exp')).toContain('V4.1')
  })

  it('没收录的模型返回空串，不回落成模型 ID', () => {
    // 回落成 ID 的话，副标题会跟主标题一模一样，纯噪音
    expect(resolveModelVersion('gpt-image-2.5-flare')).toBe('')
    expect(resolveModelVersion('unknown-model')).toBe('')
    expect(resolveModelVersion('')).toBe('')
  })

  it('两侧空白不影响匹配', () => {
    expect(resolveModelVersion('  deepseek-v4-flash  ')).toBe('DeepSeek-V4.1-Flash')
  })
})
