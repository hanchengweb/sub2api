<template>
  <div class="plaza-pricing-table space-y-8">
    <section v-for="sec in sections" :key="sec.kind">
      <!-- 分区头：类型 + 计价口径。三类的口径不同（1M token / 每张 / 每秒），
           混在一张表里表头只能取最泛的说法，分开才能各自说准。 -->
      <div class="pz-section-heading">
        <span class="pz-section-icon"><Icon :name="sec.kind === 'text' ? 'chat' : sec.kind === 'image' ? 'sparkles' : 'play'" size="sm" /></span>
        <span class="pz-title">{{ sec.label }}</span>
        <span class="text-xs text-gray-500 dark:text-gray-400">{{ sec.unitHint }}</span>
        <span class="ml-auto text-[11px] text-gray-400 dark:text-dark-500">
          {{ t('modelPlaza.table.modelCount', { n: sec.models.length }) }}
        </span>
      </div>

      <!-- 时段说明：只在该分区确有时段定价的模型时出现 -->
      <p
        v-if="sec.timeNote"
        class="mb-1.5 inline-flex items-center gap-1 rounded-md bg-gray-50 px-2 py-1 text-[11px] text-gray-500 dark:bg-dark-800/60 dark:text-dark-400"
      >
        {{ sec.timeNote }}
      </p>

      <div class="pz-table-frame">
        <table class="w-full border-collapse text-sm tabular-nums" :class="sec.minWidth" :data-kind="sec.kind">
          <thead>
            <tr
              class="border-b border-gray-200 text-left font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400"
            >
              <th class="w-[38%] px-3 py-2 font-medium">{{ t('modelPlaza.table.model') }}</th>
              <template v-if="sec.kind === 'text'">
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
              </template>
              <th v-else class="px-3 py-2 text-left font-medium">
                {{ t('modelPlaza.table.unitPrice') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="m in sec.models"
              :key="m.name"
              class="border-b border-gray-100 transition-colors last:border-b-0 hover:bg-gray-50/70 dark:border-dark-800 dark:hover:bg-dark-800/50"
            >
              <!-- 模型名。px-3 的左内边距是必需的：原来只有 pr-4，文字贴着
                   表格左边缘，外层又是 overflow-x-auto，首字母会被切掉。 -->
              <td class="px-3 py-2.5 align-middle">
                <div class="flex min-w-0 items-center gap-2">
                  <ModelBrandMark :model="m.name" size="md" />
                  <span data-testid="model-name" class="break-words font-medium text-gray-900 dark:text-white">{{ m.name }}</span>
                  <a :href="`/playground?model=${encodeURIComponent(m.name)}&mode=${sec.kind === 'text' ? 'chat' : sec.kind}`" class="pz-model-action" :aria-label="`${t('modelGallery.tryIt')} ${m.name}`" :title="t('modelGallery.tryIt')"><Icon name="arrowRight" size="sm" /></a>
                </div>
              </td>

              <template v-if="sec.kind === 'text'">
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
              </template>

              <!-- 按次 / 按图 / 按秒：档位做成 chip 并排，比堆在一列里可读 -->
              <td v-else class="pz-cell px-3 py-2.5 align-middle">
                <!-- 视频按秒计价，价格来自分组的 video_price_* 三列，不在渠道定价表里，
                     所以走单独字段；三档同价时只显示一个，避免三个一样的 chip。 -->
                <div v-if="videoTiers(m).length" class="pz-tier-list">
                  <span
                    v-for="tier in videoTiers(m)"
                    :key="tier.label"
                    class="pz-tier"
                  >
                    <span v-if="tier.label" class="pz-tier-label">{{ tier.label }}</span>
                    {{ tier.price }}
                  </span>
                  <span class="text-[11px] text-gray-400 dark:text-dark-500">{{ t('modelPlaza.section.unitPerSecond') }}</span>
                </div>
                <div v-else-if="requestIntervals(m).length" class="pz-tier-list">
                  <span
                    v-for="(iv, i) in requestIntervals(m)"
                    :key="i"
                    class="pz-tier"
                  >
                    <span class="pz-tier-label">{{ tierLabel(iv) }}</span>
                    {{ paidRequestPrice(iv.per_request_price) }}
                  </span>
                  <span class="text-[11px] text-gray-400 dark:text-dark-500">{{ perUnitSuffix(m) }}</span>
                </div>
                <template v-else-if="m.pricing?.per_request_price != null">
                  <span class="font-mono text-xs font-semibold text-gray-900 dark:text-gray-50">
                    {{ paidRequestPrice(m.pricing.per_request_price) }}
                  </span>
                  <span class="ml-1 text-[11px] text-gray-400 dark:text-dark-500">{{ perUnitSuffix(m) }}</span>
                </template>
                <span v-else class="text-gray-400 dark:text-dark-500">-</span>
              </td>
            </tr>
          </tbody>
        </table>
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

/**
 * 分区归类。
 *
 * 视频按模型名判，不按 billing_mode：线上视频模型的 billing_mode 配的是 image
 * （走的是图片接口那条闸门），只看计费模式会把它混进生图区。
 */
function sectionOf(m: PlazaModel): SectionKind {
  if (/video/i.test(m.name)) return 'video'
  return billingMode(m) === BILLING_MODE_TOKEN ? 'text' : 'image'
}

/** 只渲染有模型的分区，空分区不占版面。 */
const sections = computed<Section[]>(() => {
  const buckets: Record<SectionKind, PlazaModel[]> = { text: [], image: [], video: [] }
  for (const m of props.models) buckets[sectionOf(m)].push(m)
  // 区内排序沿用旧行为：官方输出价从高到低，无官方价的排最后，同价按名称升序。
  for (const k of Object.keys(buckets) as SectionKind[]) {
    buckets[k].sort((a, b) => {
      const pa = a.official_pricing?.output_price ?? null
      const pb = b.official_pricing?.output_price ?? null
      if (pa != null && pb != null && pa !== pb) return pb - pa
      if (pa != null && pb == null) return -1
      if (pa == null && pb != null) return 1
      return a.name.localeCompare(b.name)
    })
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
.plaza-pricing-table { color: #202427; }
.pz-section-heading { @apply mb-3 flex flex-wrap items-center gap-2.5; }
.pz-section-icon { @apply inline-flex h-8 w-8 items-center justify-center rounded-lg bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-gray-300; }
.pz-title { @apply text-sm font-semibold text-gray-900 dark:text-white; }
.pz-table-frame { @apply overflow-x-auto border-y border-gray-200 dark:border-dark-700; }
.pz-table-frame thead { @apply bg-gray-50 dark:bg-dark-800; }
.pz-table-frame th { padding-block: 12px; font-size: 12px; }
.pz-table-frame tbody td { padding-block: 16px; }
.pz-cell { font-variant-numeric: tabular-nums; }
.pz-tier-list { display: flex; flex-wrap: wrap; align-items: center; gap: 12px 28px; }
.pz-tier { @apply inline-flex min-w-24 flex-col gap-1 text-sm font-semibold text-gray-900 dark:text-gray-100; font-variant-numeric: tabular-nums; }
.pz-tier-label { @apply text-xs font-normal text-gray-500 dark:text-gray-400; }
.pz-model-action { @apply ml-auto inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-primary-500 dark:hover:bg-dark-700 dark:hover:text-white; }
@media (prefers-reduced-motion: reduce) { .pz-model-action { transition: none; } }
@media (max-width: 639px) {
  table:not([data-kind="text"]) { min-width: 0; }
  table:not([data-kind="text"]) thead { display: none; }
  table:not([data-kind="text"]) tbody,
  table:not([data-kind="text"]) tbody tr,
  table:not([data-kind="text"]) tbody td { display: block; width: 100%; }
  table:not([data-kind="text"]) tbody tr { padding-block: 16px; }
  table:not([data-kind="text"]) tbody td { padding-block: 0; }
  table:not([data-kind="text"]) tbody td + td { padding-top: 16px; }
  .pz-tier-list { gap: 12px; }
  .pz-tier { min-width: 88px; }
}
</style>
