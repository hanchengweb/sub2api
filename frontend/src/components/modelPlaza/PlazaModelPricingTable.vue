<template>
  <div class="plaza-pricing-table space-y-5" :style="accentStyle">
    <section v-for="sec in sections" :key="sec.kind">
      <!-- 分区头：类型 + 计价口径。三类的口径不同（1M token / 每张 / 每秒），
           混在一张表里表头只能取最泛的说法，分开才能各自说准。 -->
      <div class="mb-1.5 flex items-baseline gap-2">
        <span class="pz-title text-[13px] font-semibold">{{ sec.label }}</span>
        <span class="text-[11px] text-gray-400 dark:text-dark-500">{{ sec.unitHint }}</span>
        <span class="ml-auto text-[11px] text-gray-400 dark:text-dark-500">
          {{ t('modelPlaza.table.modelCount', { n: sec.models.length }) }}
        </span>
      </div>

      <div class="overflow-x-auto">
        <table class="w-full border-collapse text-sm tabular-nums" :class="sec.minWidth">
          <thead>
            <tr
              class="border-b border-gray-200 text-left text-[11px] font-medium uppercase tracking-wide text-gray-400 dark:border-dark-700 dark:text-dark-500"
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
                <div class="flex items-center gap-2">
                  <ModelBrandMark :model="m.name" size="sm" />
                  <span data-testid="model-name" class="truncate font-medium text-gray-900 dark:text-white">{{ m.name }}</span>
                </div>
              </td>

              <template v-if="sec.kind === 'text'">
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs font-semibold text-gray-900 dark:text-gray-50">
                  <template v-if="tokenIntervals(m).length">
                    <div v-for="(iv, i) in tokenIntervals(m)" :key="i" class="whitespace-nowrap leading-5">
                      <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ tierLabel(iv) }}</span>
                      {{ paidPerMillion(iv.input_price) }}
                    </div>
                  </template>
                  <template v-else>{{ paidPerMillion(m.pricing?.input_price) }}</template>
                </td>
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs font-semibold text-gray-900 dark:text-gray-50">
                  <template v-if="tokenIntervals(m).length">
                    <div v-for="(iv, i) in tokenIntervals(m)" :key="i" class="whitespace-nowrap leading-5">
                      <span class="mr-1 font-sans font-normal text-gray-400 dark:text-dark-500">{{ tierLabel(iv) }}</span>
                      {{ paidPerMillion(iv.output_price) }}
                    </div>
                  </template>
                  <template v-else>{{ paidPerMillion(m.pricing?.output_price) }}</template>
                </td>
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs text-gray-700 dark:text-gray-300">
                  {{ paidPerMillion(m.pricing?.cache_write_price) }}
                </td>
                <td class="pz-cell px-3 py-2.5 text-right align-middle font-mono text-xs text-gray-700 dark:text-gray-300">
                  {{ paidPerMillion(m.pricing?.cache_read_price) }}
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
                <div v-if="videoTiers(m).length" class="flex flex-wrap items-center gap-1.5">
                  <span
                    v-for="tier in videoTiers(m)"
                    :key="tier.label"
                    class="inline-flex items-center gap-1 rounded-md bg-white/70 px-2 py-0.5 font-mono text-xs text-gray-800 ring-1 ring-black/5 dark:bg-dark-700/60 dark:text-gray-200 dark:ring-white/10"
                  >
                    <span v-if="tier.label" class="font-sans text-gray-400 dark:text-dark-500">{{ tier.label }}</span>
                    {{ tier.price }}
                  </span>
                  <span class="text-[11px] text-gray-400 dark:text-dark-500">{{ t('modelPlaza.section.unitPerSecond') }}</span>
                </div>
                <div v-else-if="requestIntervals(m).length" class="flex flex-wrap items-center gap-1.5">
                  <span
                    v-for="(iv, i) in requestIntervals(m)"
                    :key="i"
                    class="inline-flex items-center gap-1 rounded-md bg-white/70 px-2 py-0.5 font-mono text-xs text-gray-800 ring-1 ring-black/5 dark:bg-dark-700/60 dark:text-gray-200 dark:ring-white/10"
                  >
                    <span class="font-sans text-gray-400 dark:text-dark-500">{{ tierLabel(iv) }}</span>
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
import { platformAccentColor } from '@/utils/platformColors'
import ModelBrandMark from './ModelBrandMark.vue'
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

const accentStyle = computed(() => ({ '--plaza-accent': platformAccentColor(props.platform ?? '') }))

const PER_MILLION = 1_000_000
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

  const defs: Array<Omit<Section, 'models'>> = [
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
  return defs.filter((d) => buckets[d.kind].length > 0).map((d) => ({ ...d, models: buckets[d.kind] }))
})

/** 实付价 = 渠道单价 × 生效倍率，按积分 / 1M token 展示。 */
function paidPerMillion(value: number | null | undefined): string {
  if (value == null) return '-'
  return formatScaled(value * effectiveRate.value, PER_MILLION, MIN_DECIMALS)
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
/* 实付分区配色统一从 --plaza-accent（平台主色）派生，新增平台无需扩展样式 */
.plaza-pricing-table {
  --pz-title: color-mix(in srgb, var(--plaza-accent) 88%, black);
  --pz-bg: color-mix(in srgb, var(--plaza-accent) 7%, transparent);
  --pz-bg-hover: color-mix(in srgb, var(--plaza-accent) 13%, transparent);
}

.dark .plaza-pricing-table {
  --pz-title: color-mix(in srgb, var(--plaza-accent) 70%, white);
  --pz-bg: color-mix(in srgb, var(--plaza-accent) 6%, transparent);
  --pz-bg-hover: color-mix(in srgb, var(--plaza-accent) 10%, transparent);
}

.pz-cell {
  background-color: var(--pz-bg);
  transition: background-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
}

tbody tr:hover .pz-cell {
  background-color: var(--pz-bg-hover);
}

.pz-title {
  /* color-mix 不可用的老浏览器回退为平台原色 */
  color: var(--plaza-accent);
  color: var(--pz-title);
}
</style>
