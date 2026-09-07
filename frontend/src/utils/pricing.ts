/**
 * 价格展示的计价单位。
 *
 * - `credit`：积分。渠道自定义定价直接以积分配置（￥1 = 100 积分），
 *   实付价、渠道配置界面显示的都是这一类。
 * - `usd`：美元。仅用于 LiteLLM 官方参考价——那是 USD/token 的原始目录价，
 *   与积分不同量纲，标成积分会让读者误判加价倍数。
 */
export type PriceUnit = 'credit' | 'usd'

const UNIT_SUFFIX = ' 积分'

/**
 * 按 `scale` 放大单价并附上计价单位。
 *
 *   formatScaled(0.0003, 1_000_000)             → "300 积分"
 *   formatScaled(0.395,  1)                     → "0.395 积分"
 *   formatScaled(0.000003, 1_000_000, 2, 'usd') → "$3.00"
 *
 * `minFractionDigits` pads the result without changing longer values.
 */
export function formatScaled(
  value: number | null,
  scale: number,
  minFractionDigits = 0,
  unit: PriceUnit = 'credit'
): string {
  if (value == null) return '-'
  let s = (value * scale).toPrecision(10).replace(/\.?0+$/, '')
  if (minFractionDigits > 0 && !s.includes('e')) {
    const dot = s.indexOf('.')
    const digits = dot === -1 ? 0 : s.length - dot - 1
    if (digits < minFractionDigits) {
      s = (dot === -1 ? `${s}.` : s) + '0'.repeat(minFractionDigits - digits)
    }
  }
  return unit === 'usd' ? `$${s}` : `${s}${UNIT_SUFFIX}`
}
