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

    <!-- 卡片墙 -->
    <div v-else class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      <article
        v-for="card in visibleCards"
        :key="card.name"
        class="card flex flex-col gap-3 p-4 transition-shadow hover:shadow-card-hover"
      >
        <div class="flex items-start gap-2.5">
          <ModelBrandMark :model="card.name" />
          <div class="min-w-0 flex-1">
            <h2 class="truncate text-sm font-semibold text-gray-900 dark:text-white" :title="card.name">
              {{ card.name }}
            </h2>
            <p class="truncate text-xs text-gray-400">{{ card.vendor }}</p>
          </div>
          <span class="kind-badge" :class="kindBadgeClass(card.kind)">{{ t(kindLabelKey(card.kind)) }}</span>
        </div>

        <!-- 价格：三类的口径不同，各自说准 -->
        <div class="min-h-[2.5rem] text-xs text-gray-600 dark:text-dark-300">
          <template v-if="card.priceLines.length">
            <div v-for="(line, i) in card.priceLines" :key="i" class="flex justify-between gap-2 leading-5">
              <span class="text-gray-400">{{ line.label }}</span>
              <span class="font-mono">{{ line.value }}</span>
            </div>
          </template>
          <span v-else class="text-gray-400">{{ t('modelGallery.noPricing') }}</span>
        </div>

        <RouterLink :to="playgroundLink(card)" class="btn btn-primary w-full py-1.5 text-xs">
          {{ t('modelGallery.tryIt') }}
        </RouterLink>
      </article>
    </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '@/components/layout/AppLayout.vue'
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
.kind-badge {
  @apply shrink-0 rounded-md px-1.5 py-0.5 text-[10px] font-medium;
}
</style>
