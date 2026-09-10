<template>
  <AppLayout>
    <div class="w-full">
    <header class="mb-5">
      <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('modelGallery.title') }}</h1>
      <p class="mt-1 text-sm text-gray-500 dark:text-dark-400">{{ t('modelGallery.description') }}</p>
    </header>

    <!-- 筛选：类型 + 供应商。两排 chip，不用下拉——选项都很少，
         下拉反而多一次点击。 -->
    <div class="mb-5 space-y-2">
      <div class="flex flex-wrap items-center gap-1.5">
        <span class="mr-1 text-xs text-gray-400">{{ t('modelGallery.filterKind') }}</span>
        <button
          v-for="k in kindFilters"
          :key="k.value"
          class="filter-chip"
          :class="kind === k.value ? 'filter-chip-on' : 'filter-chip-off'"
          @click="kind = k.value"
        >
          {{ t(k.labelKey) }}
          <span class="ml-1 opacity-60">{{ countByKind(k.value) }}</span>
        </button>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <span class="mr-1 text-xs text-gray-400">{{ t('modelGallery.filterVendor') }}</span>
        <button
          class="filter-chip"
          :class="vendor === '' ? 'filter-chip-on' : 'filter-chip-off'"
          @click="vendor = ''"
        >
          {{ t('modelGallery.all') }}
        </button>
        <button
          v-for="v in vendorList"
          :key="v"
          class="filter-chip"
          :class="vendor === v ? 'filter-chip-on' : 'filter-chip-off'"
          @click="vendor = v"
        >
          {{ v }}
        </button>
      </div>
    </div>

    <p v-if="loading" class="py-16 text-center text-sm text-gray-400">{{ t('modelGallery.loading') }}</p>
    <p v-else-if="loadError" class="py-16 text-center text-sm text-red-500">{{ loadError }}</p>
    <p v-else-if="!visibleCards.length" class="py-16 text-center text-sm text-gray-400">
      {{ t('modelGallery.empty') }}
    </p>

    <!-- 卡片墙：CSS 多列做瀑布流。
         用 columns 而不是 grid：grid 的每行会被最高的卡片撑齐，留一堆空白；
         多列布局让卡片按各自高度依次填充，才是商品墙那种错落感。
         break-inside-avoid 保证卡片不被拆到两列。 -->
    <div v-else class="gallery-masonry">
      <article v-for="card in visibleCards" :key="card.name" class="gallery-card group">
        <!-- 视觉头：品牌色渐变 + 大号官方标。模型没有预览图，用品牌色块做识别面，
             一眼能认出是哪家的货。 -->
        <div class="gallery-hero" :style="heroStyle(card.name)">
          <ModelBrandMark :model="card.name" size="lg" class="!rounded-xl shadow-sm" />
          <span class="kind-badge absolute right-2 top-2" :class="kindBadgeClass(card.kind)">
            {{ t(kindLabelKey(card.kind)) }}
          </span>
        </div>

        <div class="p-3">
          <h2
            class="truncate text-sm font-semibold text-gray-900 dark:text-white"
            :title="card.name"
          >
            {{ card.name }}
          </h2>
          <p class="mt-0.5 truncate text-xs text-gray-400">{{ card.vendor }}</p>

          <!-- 价格：三类口径不同，各自说准 -->
          <div class="mt-2 space-y-0.5 text-xs">
            <template v-if="card.priceLines.length">
              <div v-for="(line, i) in card.priceLines" :key="i" class="flex items-baseline justify-between gap-2">
                <span class="text-gray-400">{{ line.label }}</span>
                <span class="font-mono font-medium text-gray-900 dark:text-gray-100">{{ line.value }}</span>
              </div>
            </template>
            <span v-else class="text-gray-400">{{ t('modelGallery.noPricing') }}</span>
          </div>

          <!-- 动作按钮不占满整行：整条绿色横杠在卡片墙里过于抢眼，
               每张卡都来一条会盖过商品本身。 -->
          <div class="mt-3 flex items-center justify-end">
            <RouterLink :to="playgroundLink(card)" class="gallery-try">
              {{ t('modelGallery.tryIt') }}
              <Icon name="arrowRight" size="xs" />
            </RouterLink>
          </div>
        </div>
      </article>
    </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import ModelBrandMark from '@/components/modelPlaza/ModelBrandMark.vue'
import { getModelPlaza, type PlazaModel } from '@/api/modelPlaza'
import { resolveModelVendor, resolveModelKind, type ModelKind } from '@/utils/modelVendor'
import { formatCredits } from '@/utils/format'

const { t } = useI18n()

interface PriceLine {
  label: string
  value: string
}

interface Card {
  name: string
  vendor: string
  kind: ModelKind
  priceLines: PriceLine[]
}

const cards = ref<Card[]>([])
const loading = ref(true)
const loadError = ref('')
const kind = ref<ModelKind | ''>('')
const vendor = ref('')

const kindFilters = [
  { value: '' as const, labelKey: 'modelGallery.all' },
  { value: 'text' as const, labelKey: 'modelGallery.kindText' },
  { value: 'image' as const, labelKey: 'modelGallery.kindImage' },
  { value: 'video' as const, labelKey: 'modelGallery.kindVideo' }
]

const vendorList = computed(() => [...new Set(cards.value.map((c) => c.vendor))].sort())

const visibleCards = computed(() =>
  cards.value.filter(
    (c) => (kind.value === '' || c.kind === kind.value) && (vendor.value === '' || c.vendor === vendor.value)
  )
)

function countByKind(k: ModelKind | ''): number {
  if (k === '') return cards.value.length
  return cards.value.filter((c) => c.kind === k).length
}

function kindLabelKey(k: ModelKind): string {
  return k === 'video' ? 'modelGallery.kindVideo' : k === 'image' ? 'modelGallery.kindImage' : 'modelGallery.kindText'
}

function kindBadgeClass(k: ModelKind): string {
  if (k === 'video') return 'bg-purple-50 text-purple-600 dark:bg-purple-900/25 dark:text-purple-300'
  if (k === 'image') return 'bg-amber-50 text-amber-600 dark:bg-amber-900/25 dark:text-amber-300'
  return 'bg-sky-50 text-sky-600 dark:bg-sky-900/25 dark:text-sky-300'
}

/**
 * 卡片视觉头的底色。
 *
 * 模型没有预览图，用各家品牌色的柔和渐变当识别面——同一家的卡片颜色一致，
 * 扫一眼就能按厂商归堆。用 color-mix 兑淡，直接上原色会过于刺眼且压住文字。
 */
const VENDOR_HERO: Record<string, string> = {
  deepseek: '#4D6BFE',
  openai: '#111827',
  doubao: '#00C8FF',
  xai: '#111827',
  gemini: '#3186FF',
  nanobanana: '#FBBC04',
  flux: '#1F2937',
  vidu: '#1D4ED8',
  qwen: '#615CED',
  generic: '#94A3B8'
}

function heroStyle(model: string): Record<string, string> {
  const c = VENDOR_HERO[resolveModelVendor(model).key] ?? VENDOR_HERO.generic
  return {
    background: `linear-gradient(135deg, color-mix(in srgb, ${c} 16%, transparent), color-mix(in srgb, ${c} 5%, transparent))`
  }
}

/** 点「去体验」直接带着模型名跳到在线使用页，省得用户再翻一遍下拉。 */
function playgroundLink(card: Card) {
  return { path: '/playground', query: { model: card.name, mode: card.kind } }
}

/**
 * 卡片上的价格摘要。
 *
 * 三类口径不同：文本按每万 token（与定价页一致），生图按每张、取最低与最高档，
 * 视频按每秒。只展示摘要，完整档位表在「模型定价」页。
 */
function buildPriceLines(m: PlazaModel, rate: number, k: ModelKind): PriceLine[] {
  if (k === 'video') {
    const vp = m.video_pricing
    const per = vp?.price_per_second_720p ?? vp?.price_per_second_480p ?? vp?.price_per_second_1080p
    if (per == null) return []
    return [{ label: t('modelGallery.perSecond'), value: formatCredits(per * rate) }]
  }

  if (k === 'image') {
    const prices = (m.pricing?.intervals ?? [])
      .map((iv) => iv.per_request_price)
      .filter((v): v is number => v != null)
    if (prices.length) {
      const lo = Math.min(...prices) * rate
      const hi = Math.max(...prices) * rate
      return [
        {
          label: t('modelGallery.perImage'),
          // 只有一档时不显示区间——"30.33 ~ 30.33" 只会让人以为算错了
          value: lo === hi ? formatCredits(lo) : `${formatCredits(lo)} ~ ${formatCredits(hi)}`
        }
      ]
    }
    const flat = m.pricing?.per_request_price
    return flat == null ? [] : [{ label: t('modelGallery.perImage'), value: formatCredits(flat * rate) }]
  }

  const input = m.pricing?.input_price
  const output = m.pricing?.output_price
  if (input == null || output == null) return []
  const PER_TEN_THOUSAND = 10_000
  return [
    { label: t('modelGallery.input'), value: formatCredits(input * rate * PER_TEN_THOUSAND) },
    { label: t('modelGallery.output'), value: formatCredits(output * rate * PER_TEN_THOUSAND) }
  ]
}

onMounted(async () => {
  try {
    const plaza = await getModelPlaza()
    const seen = new Set<string>()
    const list: Card[] = []
    for (const group of plaza.groups ?? []) {
      const rate = group.user_rate_multiplier ?? group.rate_multiplier ?? 1
      for (const m of group.models ?? []) {
        if (seen.has(m.name)) continue
        seen.add(m.name)
        const k = resolveModelKind(m.name, m.pricing?.billing_mode)
        list.push({
          name: m.name,
          vendor: resolveModelVendor(m.name).label,
          kind: k,
          priceLines: buildPriceLines(m, rate, k)
        })
      }
    }
    list.sort((a, b) => a.vendor.localeCompare(b.vendor) || a.name.localeCompare(b.name))
    cards.value = list
  } catch {
    // 广场开关关闭时后端回 404，这里给一句可读的说明而不是空白页
    loadError.value = t('modelGallery.loadFailed')
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
.filter-chip {
  @apply rounded-lg px-2.5 py-1 text-xs transition-colors;
}
.filter-chip-on {
  @apply bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-200;
}
.filter-chip-off {
  @apply text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800;
}
/* 瀑布流：按宽度分列，卡片依次填充，不像 grid 那样被同行最高的撑齐 */
.gallery-masonry {
  column-gap: 0.75rem;
  columns: 1;
}
@media (min-width: 640px) {
  .gallery-masonry { columns: 2; }
}
@media (min-width: 1024px) {
  .gallery-masonry { columns: 3; }
}
@media (min-width: 1536px) {
  .gallery-masonry { columns: 4; }
}

.gallery-card {
  @apply mb-3 overflow-hidden rounded-2xl border border-gray-100 bg-white transition-all;
  @apply hover:-translate-y-0.5 hover:shadow-card-hover;
  @apply dark:border-dark-700/60 dark:bg-dark-800/50;
  /* 卡片不能被拆到两列 */
  break-inside: avoid;
}

.gallery-hero {
  @apply relative flex h-24 items-center justify-center;
}

.gallery-try {
  @apply inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs font-medium;
  @apply text-primary-600 transition-colors hover:bg-primary-50;
  @apply dark:text-primary-300 dark:hover:bg-primary-900/30;
}

.kind-badge {
  @apply shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-medium;
}
</style>
