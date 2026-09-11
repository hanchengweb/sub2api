<template>
  <AppLayout>
    <div ref="pageTop" tabindex="-1" class="w-full scroll-mt-20 outline-none">
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
      <article v-for="card in pagedCards" :key="card.name" class="gallery-card group">
        <div class="gallery-hero">
          <img
            v-if="heroImage(card.name) && !failedImages.has(heroImage(card.name))"
            :src="heroImage(card.name)"
            :alt="`${card.vendor} showcase`"
            class="gallery-image"
            :class="{ 'gallery-image-brand': resolveModelVendor(card.name).key === 'deepseek' }"
            loading="lazy"
            decoding="async"
            @error="failedImages.add(heroImage(card.name))"
          />
          <ModelBrandMark :model="card.name" size="lg" class="gallery-brand !rounded-lg shadow-sm" />
          <span class="kind-badge absolute right-2 top-2" :class="kindBadgeClass(card.kind)">
            {{ t(kindLabelKey(card.kind)) }}
          </span>
        </div>

        <div class="gallery-details">
          <h2
            class="gallery-name text-sm font-semibold text-gray-900 dark:text-white"
            :title="card.name"
          >
            {{ card.name }}
          </h2>
          <!-- 上游真实版本号。ID 是对外契约不能改，但用户光看 ID 判断不出用的哪一版：
               deepseek-v4-flash 实际打到的是官方 DeepSeek-V4.1-Flash。 -->
          <p
            v-if="resolveModelVersion(card.name)"
            data-testid="model-version"
            class="mt-0.5 truncate text-[11px] text-gray-400 dark:text-gray-500"
            :title="resolveModelVersion(card.name)"
          >
            {{ resolveModelVersion(card.name) }}
          </p>
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
          <div class="mt-auto flex items-center justify-end">
            <RouterLink :to="playgroundLink(card)" class="gallery-try">
              {{ t('modelGallery.tryIt') }}
              <Icon name="arrowRight" size="xs" />
            </RouterLink>
          </div>
        </div>
      </article>
    </div>
    <Pagination v-if="!loading && !loadError && visibleCards.length > PAGE_SIZE" class="model-pagination mt-6 rounded-lg" :total="visibleCards.length" :page="page" :page-size="PAGE_SIZE" :show-page-size-selector="false" @update:page="changePage" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import ModelBrandMark from '@/components/modelPlaza/ModelBrandMark.vue'
import Pagination from '@/components/common/Pagination.vue'
import { getModelPlaza, type PlazaModel } from '@/api/modelPlaza'
import { resolveModelVersion } from '@/utils/modelDisplayName'
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
const PAGE_SIZE = 12
const page = ref(1)
const pageTop = ref<HTMLElement | null>(null)

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
const pagedCards = computed(() => visibleCards.value.slice((page.value - 1) * PAGE_SIZE, page.value * PAGE_SIZE))
watch(visibleCards, () => { page.value = 1 })

async function changePage(value: number) {
  page.value = value
  await nextTick()
  pageTop.value?.focus({ preventScroll: true })
  pageTop.value?.scrollIntoView({ block: 'start', behavior: 'instant' })
}

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

// Official vendor showcases, not output claims for individual reseller model aliases.
// Source pages and original asset URLs are retained alongside the images.
const VENDOR_HERO: Record<string, string> = {
  deepseek: 'deepseek.webp',
  openai: 'openai.webp',
  doubao: 'doubao.webp',
  xai: 'xai.webp',
  gemini: 'gemini.webp',
  nanobanana: 'gemini.webp',
  flux: 'flux.webp',
  vidu: 'vidu.webp',
  qwen: 'qwen.webp'
}

const failedImages = ref(new Set<string>())

// 放在 /model-gallery/ 而不是 /images/：网关把 /images/ 整个前缀吃掉了
// （shouldBypassEmbeddedFrontend 里为 /images/generations、/images/tasks 这些
// OpenAI 兼容的根级别名做的旁路），静态图放那儿一律落到 Gin 默认 404。
// 与当初 /models 被网关路由盖掉、改用 /model-gallery 是同一类碰撞。
function heroImage(model: string): string {
  const file = VENDOR_HERO[resolveModelVendor(model).key]
  return file ? `/model-gallery/${file}` : ''
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
.model-pagination :deep(> div:last-child) { flex-wrap: wrap; gap: 12px; }
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
  @apply mb-3 overflow-hidden rounded-lg bg-white transition-all;
  @apply hover:-translate-y-0.5 hover:shadow-card-hover;
  @apply dark:border-dark-700/60 dark:bg-dark-800/50;
  /* 卡片不能被拆到两列 */
  break-inside: avoid;
  display: grid;
  height: 420px;
  grid-template-rows: 60% 40%;
}

.gallery-hero {
  @apply relative overflow-hidden bg-gray-100 dark:bg-dark-700;
}

.gallery-image {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.gallery-brand {
  position: absolute;
  bottom: 12px;
  left: 12px;
}

.gallery-image-brand {
  object-fit: contain;
  background: #f5f8ff;
}

.gallery-details {
  @apply flex min-w-0 flex-col p-3;
}

.gallery-name {
  overflow-wrap: anywhere;
  line-height: 1.25rem;
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
