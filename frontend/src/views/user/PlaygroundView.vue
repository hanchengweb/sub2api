<template>
  <AppLayout>
    <div class="flex h-[calc(100vh-11rem)] min-h-[32rem] gap-4">
      <!-- 会话列表 -->
      <aside class="hidden w-60 shrink-0 flex-col card p-3 lg:flex">
        <button class="btn btn-primary w-full" @click="startNewConversation">
          <Icon name="plus" size="sm" class="mr-1" />
          {{ t('playground.newChat') }}
        </button>

        <div class="mt-3 min-h-0 flex-1 space-y-1 overflow-y-auto">
          <p v-if="!conversations.length" class="px-2 py-6 text-center text-xs text-gray-400">
            {{ t('playground.noConversations') }}
          </p>
          <div
            v-for="c in conversations"
            :key="c.id"
            class="group flex items-center gap-2 rounded-lg px-2 py-2 text-sm transition-colors"
            :class="c.id === activeId
              ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-200'
              : 'text-gray-600 hover:bg-gray-100 dark:text-dark-300 dark:hover:bg-dark-800'"
          >
            <button class="flex min-w-0 flex-1 items-center gap-2 text-left" @click="selectConversation(c.id)">
              <Icon :name="modeIcon(c.mode)" size="sm" class="shrink-0 opacity-60" />
              <span class="min-w-0 flex-1 truncate">{{ c.title }}</span>
            </button>
            <button
              class="shrink-0 opacity-0 transition-opacity hover:text-red-500 group-hover:opacity-60"
              :aria-label="t('playground.deleteConversation')"
              @click="removeConversation(c.id)"
            >
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </div>
      </aside>

      <!-- 对话区 -->
      <section class="flex min-w-0 flex-1 flex-col card">
        <!-- 顶栏：模型选择。余额不放这里——右上角全站头部已经有了，
             同一个数字在一屏里出现两次只会让人怀疑哪个是真的。 -->
        <header class="flex flex-wrap items-center gap-2 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <Icon :name="modeIcon(mode)" size="sm" class="text-primary-500" />
          <!-- 自定义模型选择器。
               原生 <select> 挂不了各厂商 logo，选项一多下拉还会拉满整屏
               （27 个模型时几乎盖住整个页面）。这里限高滚动 + 按供应商分组。 -->
          <div v-if="availableModels.length" ref="modelPickerRef" class="relative">
            <button
              type="button"
              data-testid="model-select"
              class="input flex h-9 min-w-[15rem] max-w-[20rem] items-center gap-2 py-1 text-left text-sm"
              :aria-label="t('playground.model')"
              :aria-expanded="modelPickerOpen"
              @click="modelPickerOpen = !modelPickerOpen"
            >
              <ModelBrandMark :model="selectedModel" size="sm" />
              <span class="min-w-0 flex-1 truncate">{{ selectedModel }}</span>
              <Icon name="chevronDown" size="xs" class="shrink-0 opacity-50" />
            </button>

            <div
              v-if="modelPickerOpen"
              class="absolute left-0 top-full z-20 mt-1 max-h-80 w-[22rem] overflow-y-auto rounded-xl border border-gray-200 bg-white p-1 shadow-lg dark:border-dark-600 dark:bg-dark-800"
            >
              <template v-for="group in groupedModels" :key="group.vendor">
                <p class="px-2 pb-1 pt-2 text-[11px] font-medium text-gray-400 dark:text-dark-500">
                  {{ group.vendor }}
                </p>
                <button
                  v-for="m in group.models"
                  :key="m"
                  type="button"
                  data-testid="model-option"
                  class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm transition-colors"
                  :class="m === selectedModel
                    ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-200'
                    : 'text-gray-700 hover:bg-gray-100 dark:text-dark-200 dark:hover:bg-dark-700'"
                  @click="pickModel(m)"
                >
                  <ModelBrandMark :model="m" size="sm" />
                  <span class="min-w-0 flex-1 truncate">{{ m }}</span>
                  <Icon v-if="m === selectedModel" name="check" size="xs" class="shrink-0" />
                </button>
              </template>
            </div>
          </div>
          <span v-else class="text-xs text-gray-400">{{ t('playground.noModelsForMode') }}</span>
        </header>

        <!-- 消息流 -->
        <div ref="scrollArea" class="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-5">
          <div v-if="!activeMessages.length" class="flex h-full flex-col items-center justify-center text-center">
            <Icon name="sparkles" size="xl" class="mb-3 text-primary-400" />
            <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('playground.emptyHint') }}</p>
          </div>

          <div
            v-for="msg in activeMessages"
            :key="msg.id"
            class="flex"
            :class="msg.role === 'user' ? 'justify-end' : 'justify-start'"
          >
            <div
              class="max-w-[85%] rounded-2xl px-4 py-2.5 text-sm leading-relaxed"
              :class="msg.role === 'user'
                ? 'bg-primary-500 text-white'
                : 'bg-gray-100 text-gray-800 dark:bg-dark-800 dark:text-dark-100'"
            >
              <p v-if="msg.content" class="whitespace-pre-wrap break-words">{{ msg.content }}</p>

              <!-- 媒体结果以卡片嵌在对话流里 -->
              <img
                v-if="msg.mediaUrl && !isVideoUrl(msg.mediaUrl)"
                :src="msg.mediaUrl"
                :alt="t('playground.generatedImage')"
                class="mt-2 max-h-80 rounded-lg"
              />
              <video
                v-else-if="msg.mediaUrl"
                :src="msg.mediaUrl"
                controls
                class="mt-2 max-h-80 rounded-lg"
              />

              <a
                v-if="msg.mediaUrl"
                :href="msg.mediaUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="mt-1 inline-flex items-center gap-1 text-xs text-primary-600 hover:underline dark:text-primary-300"
              >
                <Icon name="link" size="xs" />{{ t('playground.openOriginal') }}
              </a>

              <p v-if="msg.error" class="mt-1 text-xs text-red-500">{{ msg.error }}</p>
              <RouterLink
                v-if="msg.needsTopUp"
                to="/redeem"
                class="mt-1 inline-flex items-center gap-1 text-xs text-primary-600 hover:underline dark:text-primary-300"
              >
                <Icon name="gift" size="xs" />{{ t('playground.goRedeem') }}
              </RouterLink>
            </div>
          </div>

          <div v-if="busy" class="flex items-center gap-2 text-xs text-gray-400">
            <LoadingSpinner size="sm" />
            <span>{{ busyHint }}</span>
          </div>
        </div>

        <!-- 输入区 -->
        <footer class="border-t border-gray-100 p-3 dark:border-dark-700">
          <div class="rounded-2xl border border-gray-200 p-2 shadow-sm dark:border-dark-600">
            <textarea
              v-model="draft"
              rows="2"
              :placeholder="t('playground.placeholder')"
              class="w-full resize-none bg-transparent px-2 py-1 text-sm outline-none dark:text-dark-100"
              @keydown.enter.exact.prevent="submit"
            />

            <!-- 参数条：只显示当前模式用得上的参数，且真的会带进请求。
                 摆着好看但不生效的控件比没有更糟。 -->
            <div v-if="mode !== 'chat'" class="flex flex-wrap items-center gap-1.5 px-1 pb-2">
              <label v-if="mode === 'video'" class="param-chip">
                <span class="param-label">{{ t('playground.paramDuration') }}</span>
                <select v-model.number="videoDuration" class="param-select">
                  <option v-for="d in durationOptions" :key="d" :value="d">{{ d }}s</option>
                </select>
              </label>
              <label class="param-chip">
                <span class="param-label">{{ t('playground.paramAspect') }}</span>
                <select v-model="aspectRatio" class="param-select">
                  <option v-for="a in ASPECT_OPTIONS" :key="a" :value="a">{{ a }}</option>
                </select>
              </label>
              <label class="param-chip">
                <span class="param-label">{{ t('playground.paramResolution') }}</span>
                <!-- 选项里直接标出该档位的积分：清晰度与质量是联动的，
                     只给一个总价看不出改哪个参数会让价格变。 -->
                <select v-model="resolution" class="param-select">
                  <option v-for="r in resolutionOptions" :key="r.value" :value="r.value">
                    {{ r.label }}{{ optionPriceSuffix(r.value, imageQuality) }}
                  </option>
                </select>
              </label>
              <label
                v-if="mode === 'image'"
                class="param-chip"
                :class="{ 'opacity-50': !supportsQuality }"
                :title="supportsQuality ? '' : t('playground.qualityNotApplicable')"
              >
                <span class="param-label">{{ t('playground.paramQuality') }}</span>
                <!-- 不分质量档的模型（26/28 个）也把控件显示出来但禁用：
                     直接藏掉会让人以为功能坏了，反而更难解释。 -->
                <select v-model="imageQuality" class="param-select" :disabled="!supportsQuality">
                  <option v-for="q in QUALITY_OPTIONS" :key="q.value" :value="q.value">
                    {{ t(q.labelKey) }}{{ optionPriceSuffix(resolution, q.value) }}
                  </option>
                </select>
                <span v-if="!supportsQuality" class="ml-1 text-[10px] text-gray-400">
                  {{ t('playground.qualityNotApplicableShort') }}
                </span>
              </label>
              <label v-if="mode === 'image'" class="param-chip">
                <span class="param-label">{{ t('playground.paramCount') }}</span>
                <select v-model.number="imageCount" class="param-select">
                  <option v-for="n in [1, 2, 3, 4]" :key="n" :value="n">{{ n }}</option>
                </select>
              </label>
            </div>

            <div class="flex flex-wrap items-center gap-2 px-1">
              <!-- 三模式共用同一套界面，切换不跳页 -->
              <button
                v-for="m in modes"
                :key="m.value"
                class="inline-flex items-center gap-1 rounded-lg px-2.5 py-1 text-xs transition-colors"
                :class="mode === m.value
                  ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-200'
                  : 'text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800'"
                @click="switchMode(m.value)"
              >
                <Icon :name="m.icon" size="xs" />{{ t(m.labelKey) }}
              </button>

              <!-- 本次调用的费用预估。价格来自模型定价页同一份数据，
                   按当前选中的档位实时算，不是写死的文案。 -->
              <span
                v-if="costHint"
                class="ml-auto whitespace-nowrap rounded-lg bg-amber-50 px-2 py-1 text-[11px] font-medium text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
              >
                {{ costHint }}
              </span>

              <button
                class="btn btn-primary h-8 w-8 shrink-0 rounded-full p-0"
                :class="costHint ? 'ml-2' : 'ml-auto'"
                :disabled="busy || !draft.trim() || !selectedModel"
                :aria-label="t('playground.send')"
                @click="submit"
              >
                <Icon name="arrowUp" size="sm" />
              </button>
            </div>
          </div>
          <p class="mt-2 text-center text-[11px] text-gray-400">{{ t('playground.disclaimer') }}</p>
        </footer>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelBrandMark from '@/components/modelPlaza/ModelBrandMark.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getModelPlaza, type PlazaModel } from '@/api/modelPlaza'
import {
  listModels,
  streamChat,
  createImageTask,
  getImageTask,
  createVideoTask,
  getVideoTask,
  mediaTaskId,
  mediaUrlOf,
  isTerminalStatus,
  isFailedStatus,
  isInsufficientBalance,
  type ChatMessage,
  type MediaTaskResult
} from '@/api/playground'
import {
  loadConversations,
  saveConversations,
  deriveTitle,
  newId,
  type PlaygroundConversation,
  type PlaygroundMessage,
  type PlaygroundMode
} from '@/utils/playgroundStore'
import { useAuthStore } from '@/stores/auth'
import { formatCredits } from '@/utils/format'
import { BILLING_MODE_TOKEN } from '@/constants/channel'

const { t } = useI18n()
const authStore = useAuthStore()

// as const 是必需的：Icon 的 name 是字面量联合类型，推成宽 string 会类型不匹配。
const modes = [
  { value: 'chat', labelKey: 'playground.modeChat', icon: 'chat' },
  { value: 'image', labelKey: 'playground.modeImage', icon: 'sparkles' },
  { value: 'video', labelKey: 'playground.modeVideo', icon: 'play' }
] as const

type ModeIconName = (typeof modes)[number]['icon']

const conversations = ref<PlaygroundConversation[]>([])
const activeId = ref('')
const mode = ref<PlaygroundMode>('chat')
const draft = ref('')
const busy = ref(false)
const busyHint = ref('')
const scrollArea = ref<HTMLElement | null>(null)

// 画幅比例。toAPI 的语义：size 是比例，resolution 才是清晰度档位——
// 与 OpenAI 相反，传反了上游直接拒。
const ASPECT_OPTIONS = ['1:1', '16:9', '9:16', '4:3', '3:4'] as const
const IMAGE_RESOLUTIONS = [
  { value: '1k', label: '1K' },
  { value: '2k', label: '2K' },
  { value: '4k', label: '4K' }
]
const VIDEO_RESOLUTIONS = [
  { value: '480p', label: '480p' },
  { value: '720p', label: '720p' },
  { value: '1080p', label: '1080p' }
]
// toAPI 视频时长边界 6~30 秒（见 video_billing_resolution.go 的
// toapiVideoMin/MaxDurationSeconds）；超出范围会被上游拒绝并产生一次退款往返。
const VIDEO_DURATIONS = [6, 8, 10, 12, 15, 20, 25, 30]

/**
 * 质量档。上游按「清晰度 × 质量」分档收费，同一个 1K 从低到高能差 35 倍
 * （vip 1K：低 0.38 / 中 3.38 / 高 13.5 上游积分），所以必须让用户自己选，
 * 也必须带进请求——不带就按低质量计费，用户拿到的却可能是高质量图。
 */
const QUALITY_OPTIONS = [
  { value: 'low', labelKey: 'playground.qualityLow' },
  { value: 'medium', labelKey: 'playground.qualityMedium' },
  { value: 'high', labelKey: 'playground.qualityHigh' }
] as const

/** quality 值到档位后缀的映射，必须与后端 NormalizeImageQuality 一致。 */
const QUALITY_LABELS: Record<string, string> = { low: '低', medium: '中', high: '高' }

const modelPickerOpen = ref(false)
const modelPickerRef = ref<HTMLElement | null>(null)

/**
 * 按供应商分组，供选择器分段展示。
 *
 * 供应商判定与 ModelBrandMark 保持同一套规则：那边按模型名判 logo，
 * 这边按同样的名字判分组名，两处必须一致，否则会出现「豆包组里挂着 Google 标」。
 */
const VENDOR_RULES: Array<{ test: (n: string) => boolean; vendor: string }> = [
  { test: (n) => n.includes('deepseek'), vendor: 'DeepSeek' },
  { test: (n) => n.includes('doubao') || n.includes('seedream'), vendor: '字节豆包 Doubao' },
  { test: (n) => n.includes('grok'), vendor: 'xAI Grok' },
  { test: (n) => n.includes('nano_banana') || n.includes('nano-banana'), vendor: 'Google Nano Banana' },
  { test: (n) => n.includes('gemini'), vendor: 'Google Gemini' },
  { test: (n) => n.startsWith('flux'), vendor: 'Black Forest Labs' },
  { test: (n) => n.startsWith('vidu'), vendor: '智谱清影 Vidu' },
  { test: (n) => n.startsWith('qwen'), vendor: '阿里云通义 Qwen' },
  { test: (n) => n.includes('gpt') || n.includes('dall'), vendor: 'OpenAI' }
]

const groupedModels = computed(() => {
  const buckets = new Map<string, string[]>()
  for (const m of availableModels.value) {
    const n = m.toLowerCase()
    const vendor = VENDOR_RULES.find((r) => r.test(n))?.vendor ?? '其他'
    if (!buckets.has(vendor)) buckets.set(vendor, [])
    buckets.get(vendor)!.push(m)
  }
  return [...buckets.entries()].map(([vendor, models]) => ({ vendor, models }))
})

function pickModel(m: string) {
  selectedModel.value = m
  modelPickerOpen.value = false
}

/** 点选择器之外的地方就收起——否则它会一直悬在页面上挡住内容。 */
function onDocumentClick(e: MouseEvent) {
  if (!modelPickerOpen.value) return
  const el = modelPickerRef.value
  if (el && !el.contains(e.target as Node)) modelPickerOpen.value = false
}

const imageQuality = ref<string>('low')
const aspectRatio = ref<string>('1:1')
const resolution = ref<string>('1k')
const imageCount = ref(1)
const videoDuration = ref(8)

/**
 * 模型名 → 广场定价 + 该分组的生效倍率，用于费用预估。
 *
 * 倍率必须一起存：广场返回的是渠道原价，用户实付 = 原价 × 生效倍率
 * （专属倍率优先于分组倍率）。只存价格会在配了倍率的分组上报错价。
 */
interface PricedModel {
  model: PlazaModel
  rate: number
}
const pricingByModel = ref<Map<string, PricedModel>>(new Map())

const chatModels = ref<string[]>([])
const imageModels = ref<string[]>([])
const videoModels = ref<string[]>([])
const selectedModel = ref('')

let abortController: AbortController | null = null
let pollTimer: ReturnType<typeof setTimeout> | null = null
let disposed = false

const activeConversation = computed(
  () => conversations.value.find((c) => c.id === activeId.value) ?? null
)
const activeMessages = computed<PlaygroundMessage[]>(() => activeConversation.value?.messages ?? [])

/**
 * 当前模型是否分质量档。
 *
 * 按定价档位标签判断：带「·」后缀的（如 "1K·高"）说明这个模型按质量分档收费。
 * 不分档的模型显示质量选择器只会误导——选了也不影响价格。
 */
const supportsQuality = computed(() => {
  const tiers = pricingTiersOf(selectedModel.value)
  return tiers.some((label) => label.includes('·'))
})

const resolutionOptions = computed(() => (mode.value === 'video' ? VIDEO_RESOLUTIONS : IMAGE_RESOLUTIONS))
const durationOptions = VIDEO_DURATIONS

/**
 * 本次调用的费用预估。
 *
 * 用的就是模型定价页那份数据，按当前选中的档位实时算——写死一个数字，
 * 改了定价就会对不上。对话模式按 token 计费，发之前算不出来，所以改成报单价。
 */
const costHint = computed(() => {
  const entry = pricingByModel.value.get(selectedModel.value)
  if (!entry) return ''
  const { model: m, rate } = entry

  if (mode.value === 'video') {
    const per = videoPricePerSecond(m, resolution.value)
    if (per == null) return ''
    return t('playground.costVideo', {
      total: formatCredits(per * rate * videoDuration.value),
      seconds: videoDuration.value
    })
  }

  if (mode.value === 'image') {
    const per = imagePricePerUnit(m, resolution.value, imageQuality.value)
    if (per == null) return ''
    return t('playground.costImage', {
      total: formatCredits(per * rate * imageCount.value),
      n: imageCount.value
    })
  }

  const input = m.pricing?.input_price
  const output = m.pricing?.output_price
  if (input == null || output == null) return ''
  return t('playground.costChat', {
    input: (input * rate * 1_000_000).toFixed(2),
    output: (output * rate * 1_000_000).toFixed(2)
  })
})

/**
 * 某个（清晰度, 质量）组合的单价后缀，直接标在下拉选项里。
 *
 * 两个参数是联动的：同一个 1K，低质量 30.33、高质量 76.25，差 46 积分。
 * 只给一个总价，用户看不出该改哪个参数——把价标在选项上，一眼就能比。
 * 取不到价（模型没配这一档）就不加后缀，不编造数字。
 */
function optionPriceSuffix(res: string, quality: string): string {
  if (mode.value !== 'image') return ''
  const entry = pricingByModel.value.get(selectedModel.value)
  if (!entry) return ''
  const per = imagePricePerUnit(entry.model, res, quality)
  if (per == null) return ''
  return `  ${(per * entry.rate).toFixed(2)}`
}

/** 取某模型在定价里配的全部档位标签。 */
function pricingTiersOf(model: string): string[] {
  const entry = pricingByModel.value.get(model)
  return (entry?.model.pricing?.intervals ?? [])
    .map((iv) => (iv.tier_label ?? '').trim())
    .filter(Boolean)
}

/** 视频每秒单价：按清晰度取分组配的那一档。 */
function videoPricePerSecond(m: PlazaModel, res: string): number | null {
  const vp = m.video_pricing
  if (!vp) return null
  if (res === '1080p') return vp.price_per_second_1080p ?? vp.price_per_second_720p ?? null
  if (res === '480p') return vp.price_per_second_480p ?? vp.price_per_second_720p ?? null
  return vp.price_per_second_720p ?? vp.price_per_second_480p ?? null
}

/**
 * 生图单价：先按「清晰度·质量」精确查，未命中再退到纯清晰度档，最后回落单一按次价。
 *
 * 这套两段匹配必须与后端一致（GetTierByLabel / GetRequestTierPrice），
 * 否则页面预估和实际扣费会对不上——1K 低与 1K 高在上游差 35 倍，估错很显眼。
 */
function imagePricePerUnit(m: PlazaModel, res: string, quality: string): number | null {
  const intervals = m.pricing?.intervals ?? []
  const find = (label: string) =>
    intervals.find(
      (iv) => (iv.tier_label ?? '').toUpperCase() === label.toUpperCase() && iv.per_request_price != null
    )?.per_request_price ?? null

  const base = res.toUpperCase()
  const suffix = QUALITY_LABELS[quality]
  if (suffix) {
    const exact = find(`${base}·${suffix}`)
    if (exact != null) return exact
  }
  const plain = find(base)
  if (plain != null) return plain
  return m.pricing?.per_request_price ?? null
}

const availableModels = computed(() => {
  if (mode.value === 'image') return imageModels.value
  if (mode.value === 'video') return videoModels.value
  return chatModels.value
})

function modeIcon(m: PlaygroundMode): ModeIconName {
  return modes.find((x) => x.value === m)?.icon ?? 'chat'
}

function isVideoUrl(url: string): boolean {
  return /\.(mp4|webm|mov)(\?|$)/i.test(url)
}

function persist() {
  saveConversations(conversations.value)
}

function scrollToBottom() {
  void nextTick(() => {
    if (scrollArea.value) scrollArea.value.scrollTop = scrollArea.value.scrollHeight
  })
}

function startNewConversation() {
  const conv: PlaygroundConversation = {
    id: newId(),
    title: t('playground.newChat'),
    mode: mode.value,
    model: selectedModel.value,
    messages: [],
    updatedAt: Date.now()
  }
  conversations.value.unshift(conv)
  activeId.value = conv.id
  persist()
}

function selectConversation(id: string) {
  activeId.value = id
  const conv = conversations.value.find((c) => c.id === id)
  if (!conv) return
  mode.value = conv.mode
  // 会话记的模型可能已经下架，落不到当前可用列表就退回该模式的第一个。
  if (conv.model && availableModels.value.includes(conv.model)) selectedModel.value = conv.model
  scrollToBottom()
}

function removeConversation(id: string) {
  conversations.value = conversations.value.filter((c) => c.id !== id)
  if (activeId.value === id) activeId.value = conversations.value[0]?.id ?? ''
  persist()
}

function switchMode(next: PlaygroundMode) {
  if (mode.value === next) return
  mode.value = next
}

function ensureConversation(): PlaygroundConversation {
  if (!activeConversation.value) startNewConversation()
  return activeConversation.value as PlaygroundConversation
}

/**
 * 追加一条消息，返回数组里那条**响应式**的引用。
 *
 * 必须用返回值而不是传进来的 msg：push 进响应式数组的是原始对象，
 * 后续直接改它不会触发依赖（实测过：流式增量全部写进了原始对象，
 * 界面一个字都不刷新）。只有改从数组读回来的代理才会重新渲染。
 */
function appendMessage(
  conv: PlaygroundConversation,
  msg: PlaygroundMessage
): PlaygroundMessage {
  conv.messages.push(msg)
  conv.updatedAt = Date.now()
  persist()
  scrollToBottom()
  return conv.messages[conv.messages.length - 1]
}

async function submit() {
  const text = draft.value.trim()
  if (!text || busy.value || !selectedModel.value) return

  const conv = ensureConversation()
  conv.mode = mode.value
  conv.model = selectedModel.value
  if (conv.messages.length === 0) conv.title = deriveTitle(text)

  appendMessage(conv, { id: newId(), role: 'user', content: text, createdAt: Date.now() })
  draft.value = ''
  busy.value = true

  try {
    if (mode.value === 'chat') await runChat(conv)
    else await runMedia(conv, text)
  } catch (err) {
    // 余额不足是新用户最常撞上的一条（注册不再自动送积分），单独给中文引导，
    // 不要把 "Insufficient account balance" 这句英文原文丢给用户。
    const needsTopUp = isInsufficientBalance(err)
    appendMessage(conv, {
      id: newId(),
      role: 'assistant',
      content: '',
      error: needsTopUp
        ? t('playground.insufficientBalance')
        : err instanceof Error
          ? err.message
          : String(err),
      needsTopUp,
      createdAt: Date.now()
    })
  } finally {
    busy.value = false
    busyHint.value = ''
    // 顶栏余额随每次调用刷新——这页的用途就是让用户看着试用积分怎么花掉的，
    // 等自动刷新那 60 秒的空档会让人以为没扣费。失败任务退款后同理需要回刷。
    void authStore.refreshUser().catch(() => {})
  }
}

async function runChat(conv: PlaygroundConversation) {
  busyHint.value = t('playground.thinking')
  abortController = new AbortController()

  // 整段历史一起送：网关无状态，上下文由前端携带。
  // 带媒体结果的消息没有文本内容，会被这里过滤掉。
  const history: ChatMessage[] = conv.messages
    .filter((m) => !m.error && m.content)
    .map((m) => ({ role: m.role, content: m.content }))

  const reply = appendMessage(conv, {
    id: newId(),
    role: 'assistant',
    content: '',
    createdAt: Date.now()
  })

  await streamChat(
    conv.model,
    history,
    {
      onDelta: (delta) => {
        reply.content += delta
        conv.updatedAt = Date.now()
        scrollToBottom()
      }
    },
    { signal: abortController.signal }
  )
  persist()
}

async function runMedia(conv: PlaygroundConversation, prompt: string) {
  const isVideo = mode.value === 'video'
  busyHint.value = isVideo ? t('playground.generatingVideo') : t('playground.generatingImage')

  // size 是画幅比例、resolution 才是清晰度档位 —— 与 OpenAI 的语义相反，
  // 这是 toAPI 的约定（见 quickstart 文档），传反了会被上游拒绝。
  const task = isVideo
    ? await createVideoTask(conv.model, prompt, {
        resolution: resolution.value,
        duration: videoDuration.value
      })
    : await createImageTask(conv.model, prompt, {
        size: aspectRatio.value,
        resolution: resolution.value,
        quality: imageQuality.value,
        n: imageCount.value
      })

  const reply = appendMessage(conv, {
    id: newId(),
    role: 'assistant',
    content: '',
    taskId: mediaTaskId(task),
    createdAt: Date.now()
  })

  // 同步渠道一次就把结果带回来了，不必再轮询。
  const direct = mediaUrlOf(task)
  if (direct) {
    reply.mediaUrl = direct
    conv.updatedAt = Date.now()
    persist()
    scrollToBottom()
    return
  }

  if (!reply.taskId) {
    reply.error = t('playground.noMediaUrl')
    persist()
    return
  }
  await pollTask(conv, reply, reply.taskId, isVideo)
}

/**
 * 轮询异步任务。
 *
 * 轮询本身也是退款的触发条件——状态查询发现终态失败时，网关才会退回提交阶段
 * 扣的积分。所以失败也必须查到终态，不能提前放弃。
 */
async function pollTask(
  conv: PlaygroundConversation,
  reply: PlaygroundMessage,
  taskId: string,
  isVideo: boolean
) {
  const deadline = Date.now() + POLL_TIMEOUT_MS
  for (;;) {
    if (disposed) return
    if (Date.now() > deadline) {
      reply.error = t('playground.timeout')
      persist()
      return
    }
    await new Promise<void>((resolve) => {
      pollTimer = setTimeout(resolve, POLL_INTERVAL_MS)
    })
    if (disposed) return

    const result: MediaTaskResult = isVideo ? await getVideoTask(taskId) : await getImageTask(taskId)

    const url = mediaUrlOf(result)
    // 拿到地址就算完成——有的渠道结果就绪时不再回传 status。
    if (url) {
      reply.mediaUrl = url
      conv.updatedAt = Date.now()
      persist()
      scrollToBottom()
      return
    }
    if (!isTerminalStatus(result.status)) continue

    reply.error = isFailedStatus(result.status)
      ? result.error?.message || t('playground.generationFailed')
      : t('playground.noMediaUrl')
    conv.updatedAt = Date.now()
    persist()
    scrollToBottom()
    return
  }
}

const POLL_INTERVAL_MS = 5000
const POLL_TIMEOUT_MS = 10 * 60 * 1000

/**
 * 装配三种模式各自的模型列表。
 *
 * 名单来自网关 /v1/models（密钥实际能调的那一份），计费模式来自模型广场。
 * 两个来源分工是实测出来的：广场按定价配置拼装，线上组 4 有 7 个可调模型，
 * 广场只回 6 个——漏的正好是视频模型，只靠广场视频这一档会是空的。
 * 反过来 /v1/models 只有模型 id，没有计费信息，分不出对话还是生图。
 */
async function loadModels() {
  let names: string[] = []
  let listFailed = false
  try {
    names = await listModels()
  } catch {
    // 拿不到就退到广场目录，不能让下拉是空的。
    //
    // 最常见的失败不是网络问题而是余额为 0：/v1/models 走网关鉴权，
    // 余额闸门会把它一起拦掉（实测返回 INSUFFICIENT_BALANCE）。注册不再送积分后
    // 每个新用户第一次打开都会撞上——看到空下拉只会以为服务坏了，
    // 而不是知道自己该去兑换积分。
    listFailed = true
  }

  // 广场同时提供两样东西：计费模式（分不分对话/生图）和定价（做费用预估）。
  // 拿不到就退回按名字判断，名单本身不受影响，只是没有预估。
  const billingModes = new Map<string, string>()
  const priced = new Map<string, PricedModel>()
  try {
    const plaza = await getModelPlaza()
    for (const group of plaza.groups ?? []) {
      // 专属倍率优先于分组倍率，与定价页的口径一致。
      const rate = group.user_rate_multiplier ?? group.rate_multiplier ?? 1
      for (const m of group.models ?? []) {
        priced.set(m.name, { model: m, rate })
        if (m.pricing?.billing_mode) billingModes.set(m.name, m.pricing.billing_mode)
      }
    }
  } catch {
    /* 广场关闭时会 404，忽略 */
  }
  pricingByModel.value = priced

  // 名单拿不到时用广场目录兜底：它是公开的，不受余额闸门影响，
  // 而且现在也含视频模型（后端已把分组视频价并进广场）。
  if (listFailed) names = [...priced.keys()]

  const chat: string[] = []
  const image: string[] = []
  const video: string[] = []
  for (const name of names) {
    const mode = billingModes.get(name)
    // 名字里带 video 的一律归视频：视频模型常常没有定价行，等不到 billing_mode。
    if (/video/i.test(name)) video.push(name)
    else if (mode && mode !== BILLING_MODE_TOKEN) image.push(name)
    else chat.push(name)
  }
  chatModels.value = chat
  imageModels.value = image
  videoModels.value = video
  selectedModel.value = availableModels.value[0] ?? ''
}

watch([mode, availableModels], () => {
  if (!availableModels.value.includes(selectedModel.value)) {
    selectedModel.value = availableModels.value[0] ?? ''
  }
})

// 生图与视频的清晰度档位是两套值（1k/2k/4k vs 480p/720p/1080p）。
// 切模式后不校正的话，会把 "1k" 当分辨率发给视频接口。
watch(mode, () => {
  const allowed = resolutionOptions.value.map((r) => r.value)
  if (!allowed.includes(resolution.value)) resolution.value = allowed[0]
})

onMounted(async () => {
  document.addEventListener('click', onDocumentClick)
  conversations.value = loadConversations()
  activeId.value = conversations.value[0]?.id ?? ''
  if (activeConversation.value) mode.value = activeConversation.value.mode
  await loadModels()
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocumentClick)
  disposed = true
  abortController?.abort()
  if (pollTimer) clearTimeout(pollTimer)
})
</script>

<style scoped>
/* 参数芯片：外观像 toAPI 的胶囊控件，内核仍是原生 select——
   自绘下拉要自己处理键盘、滚动与移动端弹层，收益不抵成本。 */
.param-chip {
  @apply inline-flex cursor-pointer items-center gap-1 rounded-full border border-gray-200 bg-white px-2.5 py-1 text-[11px] transition-colors;
  @apply hover:border-gray-300 dark:border-dark-600 dark:bg-dark-800 dark:hover:border-dark-500;
}

.param-label {
  @apply text-gray-400 dark:text-dark-500;
}

.param-select {
  @apply cursor-pointer appearance-none border-none bg-transparent pr-0 text-[11px] font-medium text-gray-700 outline-none dark:text-gray-200;
}
</style>
