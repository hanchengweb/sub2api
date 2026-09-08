/**
 * 格式化工具函数
 * 参考 CRS 项目的 format.js 实现
 */

import { i18n, getLocale } from '@/i18n'
import { DEFAULT_PAYMENT_CURRENCY, currencySymbol, normalizePaymentCurrency } from '@/components/payment/currency'

/**
 * 格式化相对时间
 * @param date 日期字符串或 Date 对象
 * @returns 相对时间字符串，如 "5m ago", "2h ago", "3d ago"
 */
export function formatRelativeTime(date: string | Date | null | undefined): string {
  if (!date) return i18n.global.t('common.time.never')

  const now = new Date()
  const past = new Date(date)
  const diffMs = now.getTime() - past.getTime()

  // 处理未来时间或无效日期
  if (diffMs < 0 || isNaN(diffMs)) return i18n.global.t('common.time.never')

  const diffSecs = Math.floor(diffMs / 1000)
  const diffMins = Math.floor(diffSecs / 60)
  const diffHours = Math.floor(diffMins / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffDays > 0) return i18n.global.t('common.time.daysAgo', { n: diffDays })
  if (diffHours > 0) return i18n.global.t('common.time.hoursAgo', { n: diffHours })
  if (diffMins > 0) return i18n.global.t('common.time.minutesAgo', { n: diffMins })
  return i18n.global.t('common.time.justNow')
}

/**
 * 格式化数字（支持 K/M/B 单位）
 * @param num 数字
 * @returns 格式化后的字符串，如 "1.2K", "3.5M"
 */
export function formatNumber(num: number | null | undefined): string {
  if (num === null || num === undefined) return '0'

  const locale = getLocale()
  const absNum = Math.abs(num)

  // Use Intl.NumberFormat for compact notation if supported and needed
  // Note: Compact notation in 'zh' uses '万/亿', which is appropriate for Chinese
  const formatter = new Intl.NumberFormat(locale, {
    notation: absNum >= 10000 ? 'compact' : 'standard',
    maximumFractionDigits: 1
  })

  return formatter.format(num)
}

/**
 * 格式化货币金额
 * @param amount 金额
 * @param currency 货币代码，默认 CNY
 * @returns 格式化后的字符串，如 "￥1.25"
 */
export function formatCurrency(amount: number | null | undefined, currency: string = DEFAULT_PAYMENT_CURRENCY): string {
  const locale = getLocale()
  const normalized = normalizePaymentCurrency(currency)
  const safeAmount = amount === null || amount === undefined || !Number.isFinite(amount) ? 0 : amount

  // For very small amounts, show more decimals
  const fractionDigits = safeAmount > 0 && safeAmount < 0.01 ? 6 : 2

  if (normalized === 'CNY' || normalized === 'RMB') {
    const formattedNumber = new Intl.NumberFormat(locale, {
      minimumFractionDigits: fractionDigits,
      maximumFractionDigits: fractionDigits
    }).format(safeAmount)
    return `${currencySymbol(normalized)}${formattedNumber}`
  }

  return new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: normalized,
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits
  }).format(safeAmount)
}

/**
 * 格式化字节大小
 * @param bytes 字节数
 * @param decimals 小数位数
 * @returns 格式化后的字符串，如 "1.5 MB"
 */
export function formatBytes(bytes: number, decimals: number = 2): string {
  if (bytes === 0) return '0 Bytes'

  const k = 1024
  const dm = decimals < 0 ? 0 : decimals
  const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB']

  const i = Math.floor(Math.log(bytes) / Math.log(k))

  return parseFloat((bytes / Math.pow(k, i)).toFixed(dm)) + ' ' + sizes[i]
}

/**
 * 格式化日期
 * @param date 日期字符串或 Date 对象
 * @param options Intl.DateTimeFormatOptions
 * @param localeOverride 可选 locale 覆盖
 * @returns 格式化后的日期字符串
 */
export function formatDate(
  date: string | Date | null | undefined,
  options: Intl.DateTimeFormatOptions = {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  },
  localeOverride?: string
): string {
  if (!date) return ''

  const d = new Date(date)
  if (isNaN(d.getTime())) return ''

  const locale = localeOverride ?? getLocale()
  return new Intl.DateTimeFormat(locale, options).format(d)
}

/**
 * 格式化日期（只显示日期部分）
 * @param date 日期字符串或 Date 对象
 * @returns 格式化后的日期字符串
 */
export function formatDateOnly(date: string | Date | null | undefined): string {
  return formatDate(date, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  })
}

/**
 * 格式化日期时间（完整格式）
 * @param date 日期字符串或 Date 对象
 * @param options Intl.DateTimeFormatOptions
 * @param localeOverride 可选 locale 覆盖
 * @returns 格式化后的日期时间字符串
 */
export function formatDateTime(
  date: string | Date | null | undefined,
  options?: Intl.DateTimeFormatOptions,
  localeOverride?: string
): string {
  return formatDate(date, options, localeOverride)
}

/**
 * 格式化日期时间（精确到分钟）
 */
export function formatDateTimeToMinute(
  date: string | Date | null | undefined,
  localeOverride?: string
): string {
  return formatDate(
    date,
    {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    },
    localeOverride
  )
}

/**
 * 格式化为 date 控件值（YYYY-MM-DD，使用本地时间）
 */
export function formatDateLocalInput(date: Date): string {
  if (isNaN(date.getTime())) return ''
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

/**
 * 格式化为 datetime-local 控件值（YYYY-MM-DDTHH:mm，使用本地时间）
 */
export function formatDateTimeLocalInput(timestampSeconds: number | null): string {
  if (!timestampSeconds) return ''
  const date = new Date(timestampSeconds * 1000)
  if (isNaN(date.getTime())) return ''
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  const hours = String(date.getHours()).padStart(2, '0')
  const minutes = String(date.getMinutes()).padStart(2, '0')
  return `${year}-${month}-${day}T${hours}:${minutes}`
}

/**
 * 解析 datetime-local 控件值为时间戳（秒，使用本地时间）
 */
export function parseDateTimeLocalInput(value: string): number | null {
  if (!value) return null
  const date = new Date(value)
  if (isNaN(date.getTime())) return null
  return Math.floor(date.getTime() / 1000)
}

/**
 * 格式化 OpenAI reasoning effort（用于使用记录展示）
 * @param effort 原始 effort（如 "low" / "medium" / "high" / "xhigh"）
 * @returns 格式化后的字符串（Low / Medium / High / Xhigh），无值返回 "-"
 */
export function formatReasoningEffort(effort: string | null | undefined): string {
  const raw = (effort ?? '').toString().trim()
  if (!raw) return '-'

  const normalized = raw.toLowerCase().replace(/[-_\s]/g, '')
  switch (normalized) {
    case 'low':
      return 'Low'
    case 'medium':
      return 'Medium'
    case 'high':
      return 'High'
    case 'xhigh':
    case 'extrahigh':
      return 'XHigh'
    case 'max':
      return 'Max'
    case 'none':
    case 'minimal':
      return '-'
    default:
      // best-effort: Title-case first letter
      return raw.length > 1 ? raw[0].toUpperCase() + raw.slice(1) : raw.toUpperCase()
  }
}

/**
 * 格式化时间（显示时分秒）
 * @param date 日期字符串或 Date 对象
 * @returns 格式化后的时间字符串
 */
export function formatTime(date: string | Date | null | undefined): string {
  return formatDate(date, {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  })
}

/**
 * 格式化数字（千分位分隔，不使用紧凑单位）
 * @param num 数字
 * @returns 格式化后的字符串，如 "12,345"
 */
export function formatNumberLocaleString(num: number): string {
  return num.toLocaleString()
}

/**
 * 把积分换算成人民币展示（管理端的成本口径）。
 *
 * 成本是我们付给上游的真金白银，不是积分——toAPI 不收我们的积分。
 * 把供应商成本挂在积分上，等于把一个既成事实（付了多少钱）绑到一个可变参数
 * （积分汇率）上：改汇率那天，所有历史成本记录会静默变错。
 *
 * @param credits 积分数
 * @param creditsPerUnit 每元对应多少积分（后端 BALANCE_RECHARGE_MULTIPLIER）
 */
export function formatCreditsAsMoney(
  credits: number | null | undefined,
  creditsPerUnit: number,
  fractionDigits: number = 2
): string {
  const safe = credits === null || credits === undefined || !Number.isFinite(credits) ? 0 : credits
  const rate = Number.isFinite(creditsPerUnit) && creditsPerUnit > 0 ? creditsPerUnit : 100
  return `￥${(safe / rate).toFixed(fractionDigits)}`
}

/**
 * 毛利率（百分比，无单位）。收入与成本同为积分时直接相除，不涉及汇率。
 * 收入为 0 时返回 null（而不是 0%）：没有收入就谈不上毛利。
 */
export function grossMarginPercent(
  revenueCredits: number | null | undefined,
  costCredits: number | null | undefined
): number | null {
  const rev = Number(revenueCredits)
  const cost = Number(costCredits)
  if (!Number.isFinite(rev) || rev <= 0 || !Number.isFinite(cost)) return null
  return ((rev - cost) / rev) * 100
}

/**
 * 格式化积分（带单位后缀）。
 *
 * 积分是平台内部的计费单位：充值 ¥1 得 100 积分
 * （后端 BALANCE_RECHARGE_MULTIPLIER）。用户余额、用量消费、模型定价存的都是积分。
 *
 * 不要给这些值加 ￥ 前缀：那会把 10073 积分（= ¥100.73）显示成 ￥10073，差 100 倍。
 * 真正的金额（充值订单、套餐价、支付页）用 formatCurrency。
 *
 * @param amount 积分数
 * @param fractionDigits 小数位，默认 2
 * @returns 如 "480.53 积分"
 */
export function formatCredits(amount: number | null | undefined, fractionDigits: number = 2): string {
  const safe = amount === null || amount === undefined || !Number.isFinite(amount) ? 0 : amount
  return `${safe.toFixed(fractionDigits)} ${i18n.global.t('common.creditUnit')}`
}

/**
 * 格式化积分（大数字缩写 K/M，带单位后缀）。
 * 用于统计卡片等空间紧张、量级可能很大的位置。
 */
export function formatCreditsCompact(amount: number | null | undefined): string {
  const safe = amount === null || amount === undefined || !Number.isFinite(amount) ? 0 : amount
  const unit = i18n.global.t('common.creditUnit')
  const abs = Math.abs(safe)
  if (abs >= 1_000_000) return `${(safe / 1_000_000).toFixed(2)}M ${unit}`
  if (abs >= 1000) return `${(safe / 1000).toFixed(2)}K ${unit}`
  return `${safe.toFixed(2)} ${unit}`
}

/**
 * 格式化金额（固定小数位，不带货币符号）
 * @param amount 金额
 * @param fractionDigits 小数位数，默认 4
 * @returns 格式化后的字符串，如 "1.2345"
 */
export function formatCostFixed(amount: number, fractionDigits: number = 4): string {
  return amount.toFixed(fractionDigits)
}

/**
 * 格式化 token 数量（>=1M 显示为 M，>=1K 显示为 K，保留 1 位小数）
 * @param tokens token 数量
 * @returns 格式化后的字符串，如 "950", "1.2K", "3.5M"
 */
export function formatTokensK(tokens: number): string {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M`
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(1)}K`
  return tokens.toString()
}

/**
 * 格式化大数字（K/M/B，保留 1 位小数）
 * @param num 数字
 * @param options allowBillions=false 时最高只显示到 M
 */
export function formatCompactNumber(
  num: number | null | undefined,
  options?: { allowBillions?: boolean }
): string {
  if (num === null || num === undefined) return '0'

  const abs = Math.abs(num)
  const allowBillions = options?.allowBillions !== false

  if (allowBillions && abs >= 1_000_000_000) return `${(num / 1_000_000_000).toFixed(1)}B`
  if (abs >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`
  if (abs >= 1_000) return `${(num / 1_000).toFixed(1)}K`
  return num.toString()
}

/**
 * 格式化倒计时（从现在到目标时间的剩余时间）
 * @param targetDate 目标日期字符串或 Date 对象
 * @returns 倒计时字符串，如 "2h 41m", "3d 5h", "15m"
 */
export function formatCountdown(targetDate: string | Date | null | undefined): string | null {
  if (!targetDate) return null

  const now = new Date()
  const target = new Date(targetDate)
  const diffMs = target.getTime() - now.getTime()

  // 如果目标时间已过或无效
  if (diffMs <= 0 || isNaN(diffMs)) return null

  const diffMins = Math.floor(diffMs / (1000 * 60))
  const diffHours = Math.floor(diffMins / 60)
  const diffDays = Math.floor(diffHours / 24)

  const remainingHours = diffHours % 24
  const remainingMins = diffMins % 60

  if (diffDays > 0) {
    // 超过1天：显示 "Xd Yh"
    return i18n.global.t('common.time.countdown.daysHours', { d: diffDays, h: remainingHours })
  }
  if (diffHours > 0) {
    // 小于1天：显示 "Xh Ym"
    return i18n.global.t('common.time.countdown.hoursMinutes', { h: diffHours, m: remainingMins })
  }
  // 小于1小时：显示 "Ym"
  return i18n.global.t('common.time.countdown.minutes', { m: diffMins })
}

/**
 * 格式化倒计时并带后缀（如 "2h 41m 后解除"）
 * @param targetDate 目标日期字符串或 Date 对象
 * @returns 完整的倒计时字符串，如 "2h 41m to lift", "2小时41分钟后解除"
 */
export function formatCountdownWithSuffix(targetDate: string | Date | null | undefined): string | null {
  const countdown = formatCountdown(targetDate)
  if (!countdown) return null
  return i18n.global.t('common.time.countdown.withSuffix', { time: countdown })
}

/**
 * 格式化为相对时间 + 具体时间组合
 * @param date 日期字符串或 Date 对象
 * @returns 组合时间字符串，如 "5 天前 · 2026-01-27 15:25"
 */
export function formatRelativeWithDateTime(date: string | Date | null | undefined): string {
  if (!date) return ''

  const relativeTime = formatRelativeTime(date)
  const dateTime = formatDateTime(date)

  // 如果是 "从未" 或空字符串，只返回相对时间
  if (!dateTime || relativeTime === i18n.global.t('common.time.never')) {
    return relativeTime
  }

  return `${relativeTime} · ${dateTime}`
}
