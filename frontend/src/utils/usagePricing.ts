export const TOKENS_PER_MILLION = 1_000_000

interface TokenPriceFormatOptions {
  fractionDigits?: number
  emptyValue?: string
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

export function calculateTokenUnitPrice(
  cost: number | null | undefined,
  tokens: number | null | undefined
): number | null {
  if (!isFiniteNumber(cost) || !isFiniteNumber(tokens) || tokens <= 0) {
    return null
  }

  return cost / tokens
}

export function calculateTokenPricePerMillion(
  cost: number | null | undefined,
  tokens: number | null | undefined
): number | null {
  const unitPrice = calculateTokenUnitPrice(cost, tokens)
  if (unitPrice == null) {
    return null
  }

  return unitPrice * TOKENS_PER_MILLION
}

export function formatTokenPricePerMillion(
  cost: number | null | undefined,
  tokens: number | null | undefined,
  options: TokenPriceFormatOptions = {}
): string {
  const pricePerMillion = calculateTokenPricePerMillion(cost, tokens)
  if (pricePerMillion == null) {
    return options.emptyValue ?? '-'
  }

  const fractionDigits = options.fractionDigits ?? 4
  // 只返回数字，不带单位。
  //
  // 原先这里加 ￥ 前缀，但单价来自用量成本，单位是积分不是人民币（差 100 倍）。
  // 单位交给模板拼：调用方本来就要在后面接「/ 1M tokens」，让工具函数去读 i18n
  // 反而把一个纯函数变成了有全局依赖的函数（单测里会拿到未翻译的原始 key）。
  return pricePerMillion.toFixed(fractionDigits)
}
