import { describe, expect, it } from 'vitest'
import { yuanHint } from '../types'

// 输入框存的是积分，上游价目表是人民币口径。「1.33」看不出是多少钱，
// 「≈¥0.0133」才能直接跟上游对账，也让填错数量级当场露馅。
describe('yuanHint', () => {
  it('小额保留有效数字，不被截成 0.01', () => {
    // 1.33 积分 = ¥0.0133（gpt-image-2-vip 1K·低 的真实上游成本）
    expect(yuanHint(1.33)).toBe('≈¥0.0133')
    expect(yuanHint(2.98)).toBe('≈¥0.0298')
  })

  it('不足一元 3 位有效数字，一元以上两位小数', () => {
    // 0.4725 的浮点表示是 0.47249…，toPrecision(3) 得 0.472；提示带「≈」，末位差无妨
    expect(yuanHint(47.25)).toBe('≈¥0.472')
    expect(yuanHint(300)).toBe('≈¥3.00')
  })

  it('空值和 0 不出提示', () => {
    expect(yuanHint(null)).toBe('')
    expect(yuanHint(undefined)).toBe('')
    expect(yuanHint(0)).toBe('')
    expect(yuanHint('')).toBe('')
  })

  it('输入框传来的是字符串，也要能算', () => {
    // input 的 value 永远是 string，忘了转会得到 NaN
    expect(yuanHint('1.33')).toBe('≈¥0.0133')
    expect(yuanHint('abc')).toBe('')
  })
})
