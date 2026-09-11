import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// 渠道定价编辑器里的单位标签必须是积分，不能是 $。
//
// 这套代码是从上游项目继承来的，价格列原本是美元口径；我们全站按积分计价
// （1 元 = 100 积分）。标成 $ 会让人按美元填——把 29.33 积分填成 0.29，
// 而且这种错不会报错，只会静默少收钱。2026-09-11 线上就差点因此填错。
describe('渠道定价编辑器的单位标签', () => {
  const files = ['IntervalRow.vue', 'PricingEntryCard.vue']

  it.each(files)('%s 里不出现美元符号单位', file => {
    const src = readFileSync(resolve(__dirname, '..', file), 'utf-8')
    // 只查作为单位出现的 $：$/M、$/MTok、独立的 >$<
    expect(src).not.toMatch(/\$\/M/)
    expect(src).not.toMatch(/>\s*\$\s*</)
  })

  it('单位走 common.creditUnit，不写死中文', () => {
    for (const file of files) {
      const src = readFileSync(resolve(__dirname, '..', file), 'utf-8')
      expect(src).toContain("t('common.creditUnit')")
    }
  })
})
