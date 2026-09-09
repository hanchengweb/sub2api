<template>
  <span
    class="inline-flex shrink-0 items-center justify-center rounded-[7px] font-semibold leading-none"
    :class="sizeClass"
    :style="{ backgroundColor: brand.bg, color: brand.fg }"
    :title="brand.label"
    aria-hidden="true"
  >
    <!-- xAI 的斜杠标是纯几何图形，能画准就用图形，其余用字母标 -->
    <svg v-if="brand.key === 'xai'" viewBox="0 0 24 24" fill="currentColor" :class="glyphClass">
      <path d="M4.6 20.4 15.1 6.1h3.5L8.1 20.4H4.6Zm9.7 0-3.1-4.3 2-2.7 5.2 7H14.3ZM8.9 10.6 5.4 6.1h4.4l1.8 2.5-2.7 2Z" />
    </svg>
    <span v-else :class="letterClass">{{ brand.text }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 模型名，用它推断厂商 */
    model: string
    size?: 'sm' | 'md'
  }>(),
  { size: 'md' }
)

/**
 * 厂商标识。
 *
 * 这里是**风格化字母标**，不是各家的官方 logo 文件：官方 logo 要从各厂商品牌
 * 页取原始 SVG，拿不到就自己描一个反而更糟（描歪的商标比没有商标难看）。
 * 配色取各家品牌主色，换成官方 SVG 时只需替换本组件内部，调用方不用动。
 */
interface Brand {
  key: string
  label: string
  text: string
  bg: string
  fg: string
}

const BRANDS: Record<string, Brand> = {
  deepseek: { key: 'deepseek', label: 'DeepSeek', text: 'DS', bg: '#4D6BFE', fg: '#FFFFFF' },
  openai: { key: 'openai', label: 'OpenAI', text: 'AI', bg: '#10A37F', fg: '#FFFFFF' },
  doubao: { key: 'doubao', label: '豆包 Seedream', text: '豆', bg: '#2B5FF6', fg: '#FFFFFF' },
  xai: { key: 'xai', label: 'xAI Grok', text: 'X', bg: '#111827', fg: '#FFFFFF' },
  generic: { key: 'generic', label: '', text: 'M', bg: '#94A3B8', fg: '#FFFFFF' }
}

/**
 * 按模型名判厂商。
 *
 * 用模型名而不是 platform 字段：线上组 4 是 composite 路由，platform 一律是
 * openai/anthropic，跟真正的模型厂商对不上（doubao 就借了 gpt-image-1 的名过闸门）。
 */
const brand = computed<Brand>(() => {
  const n = props.model.toLowerCase()
  if (n.includes('deepseek')) return BRANDS.deepseek
  if (n.includes('doubao') || n.includes('seedream')) return BRANDS.doubao
  if (n.includes('grok')) return BRANDS.xai
  if (n.includes('gpt') || n.includes('dall')) return BRANDS.openai
  return BRANDS.generic
})

const sizeClass = computed(() => (props.size === 'sm' ? 'h-5 w-5' : 'h-6 w-6'))
const glyphClass = computed(() => (props.size === 'sm' ? 'h-3 w-3' : 'h-3.5 w-3.5'))
const letterClass = computed(() => (props.size === 'sm' ? 'text-[9px]' : 'text-[10px]'))
</script>
