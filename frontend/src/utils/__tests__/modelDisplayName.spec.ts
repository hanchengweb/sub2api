import { describe, expect, it } from 'vitest'
import { resolveModelVersion, resolveModelLabel } from '../modelDisplayName'

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

  // 主标题走 label：用户要看的是「这是哪一版」，deepseek-v4-flash 只写 ID
  // 会被读成还停在 V4。
  it('主标题用核实过的版本名', () => {
    expect(resolveModelLabel('deepseek-v4-flash')).toBe('DeepSeek-V4.1-Flash')
    expect(resolveModelLabel('deepseek-v4-flash-vision-exp')).toBe('DeepSeek-V4.1-Flash（视觉实验版）')
  })

  // 没收录的模型不能显示空标题——回落到 ID
  it('没收录的模型回落到模型 ID', () => {
    expect(resolveModelLabel('gpt-image-2')).toBe('gpt-image-2')
    expect(resolveModelLabel('  doubao-seedream-5-0-pro  ')).toBe('doubao-seedream-5-0-pro')
  })
})
