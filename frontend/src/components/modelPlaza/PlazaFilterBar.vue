<template>
  <div class="pricing-filters">
    <label class="relative min-w-0 sm:col-span-2 lg:col-span-1">
      <span class="sr-only">{{ t('playground.searchModels') }}</span>
      <Icon name="search" size="sm" class="absolute left-3 top-3 text-gray-400" />
      <input :value="search" type="search" class="input h-10 pl-9" :placeholder="t('playground.searchModels')" @input="$emit('update:search', ($event.target as HTMLInputElement).value)" />
    </label>
    <Select class="filter-field" :model-value="platform" :aria-label="t('modelPlaza.filters.platformLabel')" :searchable="false" :options="[{ value: 'all', label: t('modelPlaza.filters.all') }, ...platforms.map(p => ({ value: p, label: p, disabled: !platformEnabled(p) }))]" @update:model-value="$emit('update:platform', $event as string)">
      <template #selected="{ option }"><span class="filter-label">{{ t('modelPlaza.filters.platformLabel') }}</span><span class="truncate">{{ option?.label }}</span></template>
    </Select>
    <Select class="filter-field" :model-value="groupId" :aria-label="t('modelPlaza.filters.groupLabel')" :options="[{ value: 'all', label: t('modelPlaza.filters.all') }, ...groups.map(g => ({ value: g.id, label: g.name, disabled: !groupEnabled(g) }))]" @update:model-value="$emit('update:groupId', $event as number | 'all')">
      <template #selected="{ option }"><span class="filter-label">{{ t('modelPlaza.filters.groupLabel') }}</span><span class="truncate">{{ option?.label }}</span></template>
    </Select>
    <Select class="filter-field" :model-value="rate" :aria-label="t('modelPlaza.filters.rateLabel')" :searchable="false" :options="[{ value: 'all', label: t('modelPlaza.filters.all') }, ...rates.map(r => ({ value: r, label: `${r}x`, disabled: !rateEnabled(r) }))]" @update:model-value="$emit('update:rate', $event as number | 'all')">
      <template #selected="{ option }"><span class="filter-label">{{ t('modelPlaza.filters.rateLabel') }}</span><span class="truncate">{{ option?.label }}</span></template>
    </Select>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'

const props = defineProps<{
  platforms: string[]
  groups: Array<{ id: number; name: string; platform: string; rate: number }>
  rates: number[]
  platform: string
  groupId: number | 'all'
  rate: number | 'all'
  search: string
}>()
defineEmits<{
  'update:platform': [value: string]
  'update:groupId': [value: number | 'all']
  'update:rate': [value: number | 'all']
  'update:search': [value: string]
}>()
const { t } = useI18n()
function platformEnabled(p: string): boolean {
  return props.groups.some(g => g.platform === p && (props.groupId === 'all' || g.id === props.groupId) && (props.rate === 'all' || g.rate === props.rate))
}
function groupEnabled(g: { platform: string; rate: number }): boolean {
  return (props.platform === 'all' || g.platform === props.platform) && (props.rate === 'all' || g.rate === props.rate)
}
function rateEnabled(r: number): boolean {
  return props.groups.some(g => g.rate === r && (props.platform === 'all' || g.platform === props.platform) && (props.groupId === 'all' || g.id === props.groupId))
}
</script>

<style scoped>
.pricing-filters { display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; }
.pricing-filters .input { @apply rounded-lg; }
.filter-field { @apply min-w-0; }
.filter-field :deep(.select-trigger) { @apply h-10 rounded-lg px-3 py-1; }
.filter-field :deep(.select-value) { @apply flex min-w-0 items-center gap-3; }
.filter-label { @apply shrink-0 text-xs text-gray-500; }
@media (min-width: 640px) { .pricing-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1024px) { .pricing-filters { grid-template-columns: minmax(220px, 1.6fr) repeat(3, minmax(0, 1fr)); } }
</style>
