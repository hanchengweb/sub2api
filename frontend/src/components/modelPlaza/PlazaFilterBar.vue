<template>
  <div class="pricing-filters">
    <label class="relative col-span-2 min-w-0 lg:col-span-1">
      <span class="sr-only">{{ t('playground.searchModels') }}</span>
      <Icon name="search" size="sm" class="absolute left-3 top-3 text-gray-400" />
      <input :value="search" type="search" class="input h-10 pl-9" :placeholder="t('playground.searchModels')" @input="$emit('update:search', ($event.target as HTMLInputElement).value)" />
    </label>
    <label class="filter-field">
      <span>{{ t('modelPlaza.filters.platformLabel') }}</span>
      <select :value="platform" @change="$emit('update:platform', ($event.target as HTMLSelectElement).value)">
        <option value="all">{{ t('modelPlaza.filters.all') }}</option>
        <option v-for="p in platforms" :key="p" :value="p" :disabled="!platformEnabled(p)">{{ p }}</option>
      </select>
    </label>
    <label class="filter-field">
      <span>{{ t('modelPlaza.filters.groupLabel') }}</span>
      <select :value="groupId" @change="$emit('update:groupId', numericValue($event))">
        <option value="all">{{ t('modelPlaza.filters.all') }}</option>
        <option v-for="g in groups" :key="g.id" :value="g.id" :disabled="!groupEnabled(g)">{{ g.name }}</option>
      </select>
    </label>
    <label class="filter-field">
      <span>{{ t('modelPlaza.filters.rateLabel') }}</span>
      <select :value="rate" @change="$emit('update:rate', numericValue($event))">
        <option value="all">{{ t('modelPlaza.filters.all') }}</option>
        <option v-for="r in rates" :key="r" :value="r" :disabled="!rateEnabled(r)">{{ r }}x</option>
      </select>
    </label>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

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
function numericValue(event: Event): number | 'all' {
  const value = (event.target as HTMLSelectElement).value
  return value === 'all' ? 'all' : Number(value)
}
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
.pricing-filters { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.filter-field { @apply flex h-10 min-w-0 items-center gap-3 rounded-md border border-gray-200 bg-white px-3 text-xs dark:border-dark-700 dark:bg-dark-900; }
.filter-field span { @apply shrink-0 text-gray-500; }
.filter-field select { @apply min-w-0 flex-1 bg-transparent py-1 text-sm text-gray-800 dark:text-gray-200; }
@media (min-width: 640px) { .pricing-filters { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (min-width: 1024px) { .pricing-filters { grid-template-columns: minmax(220px, 1.6fr) repeat(3, minmax(0, 1fr)); } }
</style>
