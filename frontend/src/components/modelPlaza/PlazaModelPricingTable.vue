<template>
  <div class="plaza-pricing-table space-y-8">
    <section v-for="sec in sections" :key="sec.kind">
      <div class="pz-section-heading">
        <span class="pz-title">{{ sec.label }}</span>
        <span class="pz-section-unit">{{ sec.unitHint }}</span>
        <span class="ml-auto text-xs text-gray-500">{{ t('modelPlaza.table.modelCount', { n: sec.models.length }) }}</span>
      </div>
      <p v-if="sec.timeNote" class="mb-4 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ sec.timeNote }}</p>
      <div class="pz-models">
        <article v-for="m in sec.models" :key="m.name" class="pz-model">
          <header class="pz-model-header">
            <ModelBrandMark :model="m.name" size="md" />
            <div class="min-w-0 flex-1">
              <h3 data-testid="model-name" class="break-words text-sm font-semibold text-gray-900 dark:text-gray-100">{{ m.name }}</h3>
              <div class="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                <span>{{ resolveModelVendor(m.name).label }}</span>
              </div>
            </div>
            <div class="pz-model-links">
                <button type="button" class="pz-model-action" :title="t('common.copy')" :aria-label="`${t('common.copy')} ${m.name}`" @click="copyToClipboard(m.name)"><Icon name="copy" size="sm" /></button>
                <a :href="`/playground?model=${encodeURIComponent(m.name)}&mode=${sec.kind === 'text' ? 'chat' : sec.kind}`" class="pz-try" :aria-label="`${t('modelGallery.tryIt')} ${m.name}`">{{ t('modelGallery.tryIt') }}<Icon name="arrowRight" size="sm" /></a>
            </div>
          </header>
          <div class="pz-table-frame">
            <table v-if="sec.kind === 'text'" class="pz-text-table" :class="{ 'min-w-[700px]': showOfficial, 'min-w-[480px]': !showOfficial && (m.time_pricing || tokenIntervals(m).length) }">
              <caption class="sr-only">{{ m.name }} · {{ sec.unitHint }}</caption>
              <thead><tr>
                <th class="px-3 py-2 text-right font-medium">{{ t('modelPlaza.table.input') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('modelPlaza.table.output') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('modelPlaza.table.cacheWriteFull') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('modelPlaza.table.cacheReadFull') }}</th>
                <template v-if="showOfficial">
                  <th
                    colspan="3"
                    class="border-l border-gray-100 px-3 py-2 text-right font-medium dark:border-dark-700/60"
                  >
                    {{ t('modelPlaza.table.officialPrice') }}
                    <span class="normal-case font-normal">{{ t('modelPlaza.table.unitPerMillion') }}</span>
                  </th>
                </template>
              </tr></thead>
              <tbody><tr>
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs font-semibold text-gray-900 dark:text-gray-50">
                  <template v-if="tokenIntervals(m).length">
                    <div v-for="(iv, i) in tokenIntervals(m)" :key="i" class="whitespace-nowrap leading-5">
                      <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ tierLabel(iv) }}</span>
                      {{ paidPerTenThousand(iv.input_price) }}
                    </div>
                  </template>
                  <PeakOffPeakPrice v-else-if="m.time_pricing" :model="m" :value="m.pricing?.input_price" :render="paidPerTenThousand" />
                  <template v-else>{{ paidPerTenThousand(m.pricing?.input_price) }}</template>
                </td>
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs font-semibold text-gray-900 dark:text-gray-50">
                  <template v-if="tokenIntervals(m).length">
                    <div v-for="(iv, i) in tokenIntervals(m)" :key="i" class="whitespace-nowrap leading-5">
                      <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ tierLabel(iv) }}</span>
                      {{ paidPerTenThousand(iv.output_price) }}
                    </div>
                  </template>
                  <PeakOffPeakPrice v-else-if="m.time_pricing" :model="m" :value="m.pricing?.output_price" :render="paidPerTenThousand" />
                  <template v-else>{{ paidPerTenThousand(m.pricing?.output_price) }}</template>
                </td>
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs text-gray-700 dark:text-gray-300">
                  <PeakOffPeakPrice v-if="m.time_pricing" :model="m" :value="m.pricing?.cache_write_price" :render="paidPerTenThousand" />
                  <template v-else>{{ paidPerTenThousand(m.pricing?.cache_write_price) }}</template>
                </td>
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs text-gray-700 dark:text-gray-300">
                  <PeakOffPeakPrice v-if="m.time_pricing" :model="m" :value="m.pricing?.cache_read_price" :render="paidPerTenThousand" />
                  <template v-else>{{ paidPerTenThousand(m.pricing?.cache_read_price) }}</template>
                </td>
                <template v-if="showOfficial">
                  <td class="border-l border-gray-100 px-3 py-2.5 text-right align-middle font-mono text-xs text-gray-500 dark:border-dark-700/60 dark:text-dark-400">
                    {{ official(m.official_pricing?.input_price) }}
                  </td>
                  <td class="px-3 py-2.5 text-right align-middle font-mono text-xs text-gray-500 dark:text-dark-400">
                    {{ official(m.official_pricing?.output_price) }}
                  </td>
                  <td class="px-3 py-2.5 text-right align-middle font-mono text-xs text-gray-500 dark:text-dark-400">
                    <template v-if="m.official_pricing && hasOfficialCache(m.official_pricing)">
                      {{ official(m.official_pricing.cache_write_price)
                      }}<template v-if="m.official_pricing.cache_write_1h_price != null"
                        ><span class="font-sans text-gray-400 dark:text-dark-500"> (1h </span>{{
                          official(m.official_pricing.cache_write_1h_price)
                        }}<span class="font-sans text-gray-400 dark:text-dark-500">)</span></template
                      >
                      <span class="font-sans text-gray-400 dark:text-dark-500"> / </span>{{
                        official(m.official_pricing.cache_read_price)
                      }}
                    </template>
                    <span v-else>-</span>
                  </td>
                </template>
              </tr></tbody>
            </table>
            <table v-else class="pz-media-table">
              <caption class="sr-only">{{ m.name }} · {{ sec.unitHint }}</caption>
              <thead><tr><th>{{ t('modelPlaza.table.specification') }}</th><th>{{ t('modelPlaza.table.unitPrice') }}</th><th>{{ t('modelPlaza.table.billingUnit') }}</th></tr></thead>
              <tbody v-if="videoTiers(m).length">
                <tr v-for="tier in videoTiers(m)" :key="tier.label">
                  <td>{{ tier.label || '—' }}</td><td class="pz-price">{{ tier.price }}</td><td class="pz-unit">{{ t('modelPlaza.section.unitPerSecond') }}</td>
                </tr>
              </tbody>
              <tbody v-else-if="requestIntervals(m).length">
                <tr v-for="(iv, i) in requestIntervals(m)" :key="i">
                  <td>{{ tierLabel(iv) }}</td><td class="pz-price">{{ paidRequestPrice(iv.per_request_price) }}</td><td class="pz-unit">{{ perUnitSuffix(m) }}</td>
                </tr>
              </tbody>
              <tbody v-else>
                <tr><td>{{ sec.label }}</td><td class="pz-price">{{ m.pricing?.per_request_price != null ? paidRequestPrice(m.pricing.per_request_price) : '-' }}</td><td class="pz-unit">{{ perUnitSuffix(m) }}</td></tr>
              </tbody>
            </table>
          </div>
        </article>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatScaled } from '@/utils/pricing'
import ModelBrandMark from './ModelBrandMark.vue'
import Icon from '@/components/icons/Icon.vue'
import PeakOffPeakPrice from './PeakOffPeakPrice.vue'
import { resolveModelVendor } from '@/utils/modelVendor'
import { useClipboard } from '@/composables/useClipboard'
import { plazaSection, comparePlazaModels } from '@/utils/modelPlazaOrdering'
import { BILLING_MODE_TOKEN, BILLING_MODE_IMAGE, type BillingMode } from '@/constants/channel'
import type { PlazaModel } from '@/api/modelPlaza'
import type { UserPricingInterval } from '@/api/channels'

const props = defineProps<{
  models: PlazaModel[]
  /** 分组平台；实付分区底色随平台着色，未知平台回退品牌青。 */
  platform?: string
  /** 分组默认倍率。 */
  rateMultiplier: number
  /** 用户专属倍率；与默认不同时实付价按此计算。 */
  userRateMultiplier?: number | null
  /**
   * 是否展示官方参考价整列。默认关闭。
   *
   * 关闭原因：本站按积分计价，而官方参考价来自 LiteLLM 目录、单位是 USD/token。
   * 两者不同量纲并排展示会让用户按数字直接相比、误判加价倍数；该列还会把中转
   * 上游的成本价直接暴露给终端用户。
   *
   * 只在文本分区渲染：官方目录是按 token 的，生图/视频分区没有对应口径。
   */
  showOfficialPricing?: boolean
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()


const PER_MILLION = 1_000_000
/**
 * 实付价按「每 1 万 token」展示。
 *
 * 按 1M 展示时 DeepSeek 是 600/1800 积分，数字大到扎眼，和生图那边「几十积分一张」
 * 完全不在一个量级上，容易让人以为文本贵得离谱。换成 1 万后是 6.00/18.00 积分，
 * 与生图可比。**只改展示口径，实际扣费一分不动。**
 *
 * 官方参考价列仍按 $/1M：那是 LiteLLM 目录的行业惯例，换算成 1 万会变成
 * $0.00003 这种没法读的数。两列表头各自标了单位，且默认只渲染实付列。
 */
const PER_TEN_THOUSAND = 10_000
const MIN_DECIMALS = 2

const effectiveRate = computed(() => props.userRateMultiplier ?? props.rateMultiplier)

/** 官方参考价列开关，默认关闭；语义见 props.showOfficialPricing。 */
const showOfficial = computed(() => props.showOfficialPricing === true)

/**
 * 官方参考价不乘倍率，且单位是 USD/token（来自 LiteLLM 目录），
 * 与实付的积分不同量纲，必须显式按美元格式化。
 */
function official(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value, PER_MILLION, MIN_DECIMALS, 'usd')
}

function hasOfficialCache(o: NonNullable<PlazaModel['official_pricing']>): boolean {
  return o.cache_write_price != null || o.cache_read_price != null || o.cache_write_1h_price != null
}

type SectionKind = 'text' | 'image' | 'video'

interface Section {
  kind: SectionKind
  label: string
  unitHint: string
  minWidth: string
  models: PlazaModel[]
  /** 该分区若有时段定价的模型，给一句窗口说明；否则为空串。 */
  timeNote: string
}

function billingMode(m: PlazaModel): BillingMode {
  return (m.pricing?.billing_mode || BILLING_MODE_TOKEN) as BillingMode
}

/** 只渲染有模型的分区，空分区不占版面。 */
const sections = computed<Section[]>(() => {
  const buckets: Record<SectionKind, PlazaModel[]> = { text: [], image: [], video: [] }
  for (const m of props.models) buckets[plazaSection(m)].push(m)
  // 区内排序沿用旧行为：官方输出价从高到低，无官方价的排最后，同价按名称升序。
  for (const k of Object.keys(buckets) as SectionKind[]) {
    buckets[k].sort(comparePlazaModels)
  }

  const defs: Array<Omit<Section, 'models' | 'timeNote'>> = [
    {
      kind: 'text',
      label: t('modelPlaza.section.text'),
      unitHint: t('modelPlaza.table.unitPerMillionPaid'),
      minWidth: showOfficial.value ? 'min-w-[900px]' : 'min-w-[620px]'
    },
    {
      kind: 'image',
      label: t('modelPlaza.section.image'),
      unitHint: t('modelPlaza.section.unitPerImage'),
      minWidth: 'min-w-[420px]'
    },
    {
      kind: 'video',
      label: t('modelPlaza.section.video'),
      unitHint: t('modelPlaza.section.unitPerSecond'),
      minWidth: 'min-w-[420px]'
    }
  ]
  return defs
    .filter((d) => buckets[d.kind].length > 0)
    .map((d) => ({ ...d, models: buckets[d.kind], timeNote: timeNoteFor(buckets[d.kind]) }))
})

/**
 * 分区级的时段说明。
 *
 * 取该分区第一个配了时段定价的模型：同一分区里的模型通常共用一套窗口
 * （都来自同一个上游），逐模型各挂一句只会重复刷屏。
 */
function timeNoteFor(models: PlazaModel[]): string {
  const tp = models.find((m) => m.time_pricing)?.time_pricing
  if (!tp || !tp.peak_windows?.length) return ''
  const windows = tp.peak_windows.map((w) => `${formatDays(w.days)} ${w.start}-${w.end}`).join('、')
  return t('modelPlaza.time.note', {
    windows,
    timezone: tp.timezone || 'Asia/Shanghai',
    percent: Math.round((1 - tp.off_peak_multiplier) * 100),
    current: tp.is_peak_now ? t('modelPlaza.time.peak') : t('modelPlaza.time.offPeak')
  })
}

const WEEKDAY_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']

/** 把 ISO 星期数组压成可读文案：连续区间写成「周一至周五」。 */
function formatDays(days: number[] | undefined): string {
  if (!days || days.length === 0 || days.length === 7) return t('modelPlaza.time.everyday')
  const sorted = [...days].sort((a, b) => a - b)
  const name = (d: number) => t(`modelPlaza.time.${WEEKDAY_KEYS[d - 1]}`)
  const isRun = sorted.every((d, i) => i === 0 || d === sorted[i - 1] + 1)
  if (isRun && sorted.length > 2) {
    return t('modelPlaza.time.dayRange', {
      from: name(sorted[0]),
      to: name(sorted[sorted.length - 1])
    })
  }
  return sorted.map(name).join('、')
}

/** 实付价 = 渠道单价 × 生效倍率，按积分 / 1 万 token 展示。 */
function paidPerTenThousand(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value * effectiveRate.value, PER_TEN_THOUSAND, MIN_DECIMALS)
}

/** 按次 / 按图 / 按秒单价（乘生效倍率，不换算 1M）。 */
function paidRequestPrice(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value * effectiveRate.value, 1, MIN_DECIMALS)
}

function perUnitSuffix(m: PlazaModel): string {
  if (/video/i.test(m.name)) return t('modelPlaza.section.unitPerSecond')
  return billingMode(m) === BILLING_MODE_IMAGE
    ? t('modelPlaza.table.perUnitImage')
    : t('modelPlaza.table.perUnitRequest')
}

function tokenIntervals(m: PlazaModel): UserPricingInterval[] {
  return m.pricing?.intervals ?? []
}

/**
 * 视频每秒单价的展示档位。
 *
 * 三档同价时合并成一条无标签的，线上就是这种情况（480p/720p/1080p 都是 37）；
 * 三个一模一样的 chip 只会让人以为自己看错了。
 */
function videoTiers(m: PlazaModel): Array<{ label: string; price: string }> {
  const vp = m.video_pricing
  if (!vp) return []
  const entries: Array<[string, number | null]> = [
    ['480p', vp.price_per_second_480p],
    ['720p', vp.price_per_second_720p],
    ['1080p', vp.price_per_second_1080p]
  ]
  const present = entries.filter((e): e is [string, number] => e[1] != null)
  if (present.length === 0) return []

  const uniq = new Set(present.map((e) => e[1]))
  if (uniq.size === 1) {
    return [{ label: '', price: paidRequestPrice(present[0][1]) }]
  }
  return present.map(([label, price]) => ({ label, price: paidRequestPrice(price) }))
}

function requestIntervals(m: PlazaModel): UserPricingInterval[] {
  return (m.pricing?.intervals ?? []).filter((iv) => iv.per_request_price != null)
}

/** 档位标签：优先管理员配的 tier_label，否则按 token 区间生成。 */
function tierLabel(iv: UserPricingInterval): string {
  if (iv.tier_label) return iv.tier_label
  const { min_tokens: min, max_tokens: max } = iv
  if (max == null) return `>${formatTokenCount(min)}`
  if (min === 0) return `≤${formatTokenCount(max)}`
  return `${formatTokenCount(min)}–${formatTokenCount(max)}`
}

function formatTokenCount(n: number): string {
  if (n >= 1_000_000) return `${trimZero(n / 1_000_000)}M`
  if (n >= 1_000) return `${trimZero(n / 1_000)}K`
  return String(n)
}

function trimZero(n: number): string {
  return String(Math.round(n * 100) / 100)
}
</script>

<style scoped>
.pz-section-heading { @apply mb-4 flex flex-wrap items-center gap-3; }
.pz-title { @apply text-base font-semibold text-gray-900 dark:text-gray-100; }
.pz-section-unit { @apply text-xs text-gray-500 dark:text-gray-400; }
.pz-models { display: grid; grid-template-columns: minmax(0, 1fr); gap: 20px; }
.pz-model { @apply min-w-0 overflow-hidden rounded-lg border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900; }
.pz-model-header { @apply flex items-center gap-3 border-b border-gray-200 bg-primary-50/40 px-5 py-4 dark:border-dark-700 dark:bg-primary-950/25; }
.pz-model-header h3 { font-size: 15px; line-height: 1.6; overflow-wrap: anywhere; }
.pz-model-links { @apply flex shrink-0 items-center gap-3; }
.pz-model-action { @apply inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-gray-400 hover:bg-gray-100 hover:text-gray-800 focus-visible:outline-primary-500 dark:hover:bg-dark-800 dark:hover:text-gray-100; }
.pz-try { @apply inline-flex h-8 shrink-0 items-center gap-2 rounded-md border border-primary-200 bg-white px-3 text-xs font-medium text-primary-700 transition-colors hover:border-primary-700 hover:bg-primary-700 hover:text-white focus-visible:outline-primary-500 dark:border-primary-800 dark:bg-primary-950 dark:text-primary-300 dark:hover:bg-primary-800 dark:hover:text-white; }
.pz-table-frame { min-width: 0; overflow-x: auto; }
.pz-table-frame table { width: 100%; border-collapse: collapse; table-layout: fixed; font-variant-numeric: tabular-nums; }
.pz-table-frame th { @apply border-b border-gray-200 bg-gray-50/80 px-5 py-3 text-right text-xs font-medium text-gray-500 dark:border-dark-700 dark:bg-dark-800/40 dark:text-gray-400; }
.pz-table-frame td { @apply border-b border-gray-100 px-5 py-5 text-right text-sm dark:border-dark-800; overflow-wrap: anywhere; }
.pz-table-frame tbody tr:nth-child(even) { @apply bg-gray-50/40 dark:bg-dark-800/15; }
.pz-table-frame tbody tr:last-child td { border-bottom: 0; }
.pz-media-table th:first-child, .pz-media-table td:first-child { text-align: left; }
.pz-media-table tbody tr:hover { @apply bg-primary-50/50 dark:bg-primary-950/30; }
.pz-table-frame .pz-cell { @apply font-sans text-sm; }
.pz-media-table th:first-child { width: 30%; }
.pz-media-table th:last-child { width: 24%; }
.pz-table-frame .pz-price { @apply text-base font-semibold text-primary-800 dark:text-primary-200; }
.pz-table-frame .pz-unit { @apply text-xs text-gray-500 dark:text-gray-400; }
@media (max-width: 639px) {
  .pz-model-header { gap: 8px; padding: 16px 12px; }
  .pz-model-header h3 { font-size: 14px; }
  .pz-model-links { gap: 4px; }
  .pz-table-frame th, .pz-table-frame td { padding-inline: 10px; }
  .pz-table-frame .pz-cell { font-size: 12px; overflow-wrap: anywhere; }
  .pz-try { padding-inline: 4px; }
}
@media (prefers-reduced-motion: reduce) { .pz-try { transition: none; } }
</style>
