<template>
  <div class="pricing-workspace space-y-6">
    <header class="pricing-document-header flex flex-wrap items-center justify-between gap-4">
      <h1 class="text-3xl font-semibold text-gray-900 dark:text-white">{{ t('modelPlaza.title') }}</h1>
      <div class="flex items-center gap-2">
        <RouterLink v-if="isAuthenticated" to="/usage" class="pricing-header-link"><Icon name="chart" size="sm" />{{ t('nav.usage') }}</RouterLink>
        <RouterLink to="/model-gallery" class="pricing-header-link"><Icon name="grid" size="sm" />{{ t('nav.modelGallery') }}</RouterLink>
      </div>
    </header>

    <!-- 全局价格说明(管理员配置,Markdown) -->
    <div
      v-if="descriptionHtml"
      class="plaza-description border-l-2 border-primary-300 py-2 pl-4 text-sm"
      v-html="descriptionHtml"
    ></div>

    <!-- 未登录提示 -->
    <p
      v-if="!isAuthenticated"
      class="flex items-center gap-1.5 text-xs text-gray-400 dark:text-dark-500"
    >
      <Icon name="infoCircle" size="xs" class="h-3.5 w-3.5" />
      {{ t('modelPlaza.anonymousHint') }}
    </p>

    <!-- 加载/错误/空 -->
    <div v-if="loading" class="divide-y divide-gray-100 dark:divide-dark-700" role="status" :aria-label="t('common.loading')">
      <div v-for="row in 6" :key="row" class="flex items-center gap-4 py-6 motion-safe:animate-pulse">
        <div class="h-9 w-9 rounded-lg bg-gray-100 dark:bg-dark-800"></div>
        <div class="h-3 w-40 rounded bg-gray-100 dark:bg-dark-800"></div>
        <div class="ml-auto h-3 w-28 rounded bg-gray-100 dark:bg-dark-800"></div>
      </div>
    </div>
    <div
      v-else-if="error"
      class="rounded-lg border border-red-200 bg-red-50 px-5 py-8 text-center text-sm text-red-600 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
    >
      {{ t('modelPlaza.loadFailed') }}
    </div>
    <template v-else>
      <!-- 筛选区:平台 → 分组 → 倍率 -->
      <PlazaFilterBar
        :platforms="platforms"
        :groups="groupOptions"
        :rates="rates"
        :platform="selectedPlatform"
        :group-id="selectedGroupId"
        :rate="selectedRate"
        :search="searchQuery"
        @update:platform="selectedPlatform = $event"
        @update:group-id="selectedGroupId = $event"
        @update:rate="selectedRate = $event"
        @update:search="searchQuery = $event"
      />
      <div class="pricing-tabs flex flex-wrap items-center gap-2">
        <button v-for="item in kinds" :key="item.value" class="pricing-tab" :class="{ 'is-active': kind === item.value }" :aria-pressed="kind === item.value" @click="kind = item.value">{{ t(item.label) }}<span class="pricing-tab-count">{{ kindCounts[item.value] }}</span></button>
        <span class="ml-auto hidden text-xs text-gray-500 sm:inline" aria-live="polite">{{ t('modelPlaza.table.modelCount', { n: visibleCount }) }}</span>
        <button v-if="searchActive || kind || selectedPlatform !== 'all' || selectedGroupId !== 'all' || selectedRate !== 'all'" class="inline-flex h-8 w-8 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100" :aria-label="t('modelPlaza.filters.all')" :title="t('modelPlaza.filters.all')" @click="resetFilters"><Icon name="refresh" size="sm" /></button>
      </div>

      <!-- 分组分节的模型清单(默认按生效倍率升序) -->
      <div v-if="filteredGroups.length > 0" class="space-y-10">
        <PlazaGroupSection v-for="g in filteredGroups" :key="g.id" :group="g" />
      </div>
      <div
        v-else
        class="border-b border-gray-200 px-5 py-16 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400"
      >
        {{ searchActive ? t('modelPlaza.noSearchResult') : t('modelPlaza.empty') }}
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import Icon from '@/components/icons/Icon.vue'
import PlazaFilterBar from './PlazaFilterBar.vue'
import PlazaGroupSection from './PlazaGroupSection.vue'
import type { ModelPlazaGroup, ModelPlazaResponse } from '@/api/modelPlaza'
import { useAuthStore } from '@/stores/auth'
import { resolveModelKind, resolveModelVendor, type ModelKind } from '@/utils/modelVendor'

const props = defineProps<{
  response: ModelPlazaResponse | null
  loading: boolean
  error?: boolean
  /** 后台内嵌形态(AppLayout 内):隐藏页头。 */
  embedded?: boolean
  initialSearch?: string
}>()

const { t } = useI18n()
const authStore = useAuthStore()
const isAuthenticated = computed(() => authStore.isAuthenticated)

const selectedPlatform = ref<string>('all')
const selectedGroupId = ref<number | 'all'>('all')
const selectedRate = ref<number | 'all'>('all')
const searchQuery = ref(props.initialSearch ?? '')
watch(() => props.initialSearch, value => { searchQuery.value = value ?? '' })
const kind = ref<ModelKind | ''>('')
const kinds = [
  { value: '' as const, label: 'modelGallery.all' },
  { value: 'text' as const, label: 'modelGallery.kindText' },
  { value: 'image' as const, label: 'modelGallery.kindImage' },
  { value: 'video' as const, label: 'modelGallery.kindVideo' }
]
const visibleCount = computed(() => filteredGroups.value.reduce((sum, group) => sum + group.models.length, 0))
const kindCounts = computed(() => {
  const counts = { '': 0, text: 0, image: 0, video: 0 }
  for (const group of props.response?.groups ?? []) {
    for (const model of group.models) {
      counts['']++
      counts[resolveModelKind(model.name, model.pricing?.billing_mode)]++
    }
  }
  return counts
})
function resetFilters() {
  searchQuery.value = ''
  kind.value = ''
  selectedPlatform.value = 'all'
  selectedGroupId.value = 'all'
  selectedRate.value = 'all'
}

const searchActive = computed(() => searchQuery.value.trim() !== '')

const descriptionHtml = computed(() => {
  const md = props.response?.description?.trim()
  if (!md) return ''
  return DOMPurify.sanitize(marked.parse(md) as string)
})

/** 生效倍率 = 用户专属倍率 ?? 分组默认倍率。 */
function effectiveRate(g: ModelPlazaGroup): number {
  return g.user_rate_multiplier ?? g.rate_multiplier
}

const platforms = computed(() =>
  [...new Set((props.response?.groups ?? []).map((g) => g.platform).filter(Boolean))].sort()
)

const groupOptions = computed(() =>
  (props.response?.groups ?? []).map((g) => ({
    id: g.id,
    name: g.name,
    platform: g.platform,
    rate: effectiveRate(g)
  }))
)

/** 全量生效倍率;当前组合下不可用的项由 FilterBar 置灰而非隐藏。 */
const rates = computed(() =>
  [...new Set((props.response?.groups ?? []).map(effectiveRate))].sort((a, b) => a - b)
)

/** 数据刷新后选中的倍率可能不复存在,重置为全部。 */
watch(rates, (list) => {
  if (selectedRate.value !== 'all' && !list.includes(selectedRate.value)) {
    selectedRate.value = 'all'
  }
})

const filteredGroups = computed(() => {
  let groups = props.response?.groups ?? []
  if (selectedPlatform.value !== 'all') {
    groups = groups.filter((g) => g.platform === selectedPlatform.value)
  }
  if (selectedGroupId.value !== 'all') {
    groups = groups.filter((g) => g.id === selectedGroupId.value)
  }
  if (selectedRate.value !== 'all') {
    groups = groups.filter((g) => effectiveRate(g) === selectedRate.value)
  }
  // 模型名搜索:分组内只留命中的模型,整组无命中则隐藏该分组。
  const q = searchQuery.value.trim().toLowerCase()
  if (q || kind.value) {
    groups = groups
      .map((g) => ({ ...g, models: g.models.filter((m) =>
        `${m.name} ${resolveModelVendor(m.name).label}`.toLowerCase().includes(q) &&
        (!kind.value || resolveModelKind(m.name, m.pricing?.billing_mode) === kind.value)
      ) }))
      .filter((g) => g.models.length > 0)
  }
  // 专属倍率会改变生效值,不能只依赖后端按默认倍率的排序。
  return [...groups].sort(
    (a, b) => effectiveRate(a) - effectiveRate(b) || a.name.localeCompare(b.name)
  )
})
</script>

<style scoped>
.pricing-workspace { max-width: 1400px; margin-inline: auto; }
.pricing-document-header { @apply pb-2 pt-1; }
.pricing-header-link { @apply inline-flex h-9 items-center gap-2 rounded-md border border-gray-200 bg-white px-3 text-xs font-medium text-gray-600 hover:border-primary-300 hover:bg-primary-50 hover:text-primary-700 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-300 dark:hover:border-primary-700 dark:hover:bg-primary-950; }
.pricing-header-link :deep(svg) { @apply text-primary-600 dark:text-primary-400; }
.pricing-tab-count { @apply ml-2 rounded bg-gray-100 px-1.5 py-0.5 text-[11px] tabular-nums text-gray-500 dark:bg-dark-800 dark:text-gray-400; }
.pricing-tab { @apply relative min-h-9 rounded-lg border border-gray-200 bg-white px-3 text-sm font-medium text-gray-600 transition-colors hover:border-primary-300 hover:text-primary-700 focus-visible:outline-primary-500 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-400 dark:hover:text-gray-100; }
.pricing-tab.is-active { @apply border-primary-700 bg-primary-700 text-white dark:border-primary-700 dark:bg-primary-700 dark:text-white; }
.pricing-tab.is-active .pricing-tab-count { @apply bg-white/20 text-white; }
@media (max-width: 639px) {
  .pricing-tab { padding-inline: 8px; font-size: 12px; }
  .pricing-tab-count { margin-left: 4px; padding-inline: 4px; font-size: 10px; }
}
.plaza-description {
  line-height: 1.7;
  overflow-wrap: anywhere;
}

.plaza-description :deep(h1),
.plaza-description :deep(h2),
.plaza-description :deep(h3) {
  @apply mb-2 mt-3 font-semibold text-gray-900 first:mt-0 dark:text-white;
}

.plaza-description :deep(p) {
  @apply mb-2 text-gray-700 last:mb-0 dark:text-dark-200;
}

.plaza-description :deep(a) {
  @apply text-primary-600 underline underline-offset-4 hover:text-primary-700 dark:text-primary-300;
}

.plaza-description :deep(ul) {
  @apply mb-2 list-disc pl-5;
}

.plaza-description :deep(ol) {
  @apply mb-2 list-decimal pl-5;
}

.plaza-description :deep(li) {
  @apply mb-0.5 text-gray-700 dark:text-dark-200;
}

.plaza-description :deep(code) {
  @apply rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs dark:bg-dark-800;
}

.plaza-description :deep(blockquote) {
  @apply my-2 border-l-4 border-gray-300 pl-3 text-gray-600 dark:border-dark-600 dark:text-dark-300;
}
</style>
