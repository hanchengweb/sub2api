<template>
  <span v-if="value == null" class="text-gray-400 dark:text-dark-500">-</span>
  <span v-else class="inline-flex flex-col items-end leading-4">
    <!-- 峰红谷绿。当前生效的那一档加粗并带底色，另一档淡化——
         两行同样醒目的话，用户还是不知道此刻按哪个价扣费。 -->
    <span
      class="whitespace-nowrap rounded px-1"
      :class="isPeakNow
        ? 'bg-red-50 font-semibold text-red-600 dark:bg-red-900/25 dark:text-red-300'
        : 'text-red-500/60 dark:text-red-400/50'"
    >
      <span class="mr-0.5 font-sans text-[10px]">{{ t('modelPlaza.time.peak') }}</span>{{ render(value) }}
    </span>
    <span
      class="whitespace-nowrap rounded px-1"
      :class="!isPeakNow
        ? 'bg-emerald-50 font-semibold text-emerald-600 dark:bg-emerald-900/25 dark:text-emerald-300'
        : 'text-emerald-600/60 dark:text-emerald-400/50'"
    >
      <span class="mr-0.5 font-sans text-[10px]">{{ t('modelPlaza.time.offPeak') }}</span>{{ render(offPeakValue) }}
    </span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PlazaModel } from '@/api/modelPlaza'

const props = defineProps<{
  model: PlazaModel
  /** 高峰价（渠道配置里的原值）。null 时整格显示 -。 */
  value: number | null | undefined
  /** 价格格式化函数，由表格传入，保证与其余单元格口径一致。 */
  render: (v: number | null | undefined) => string
}>()

const { t } = useI18n()

/** 当前是否处于高峰。由服务端按配置时区判定，前端不重算。 */
const isPeakNow = computed(() => props.model.time_pricing?.is_peak_now ?? true)

/** 空闲价 = 高峰价 × 折扣系数。 */
const offPeakValue = computed(() => {
  if (props.value == null) return null
  const m = props.model.time_pricing?.off_peak_multiplier
  if (m == null || !(m > 0)) return props.value
  return props.value * m
})
</script>
