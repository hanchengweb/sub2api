<template>
  <AppLayout>
    <div class="pg-workspace">
      <!-- 会话列表 -->
      <aside class="pg-history" :class="historyOpen ? 'flex' : 'hidden lg:flex'">
        <button class="btn btn-primary w-full" :disabled="busy" @click="startNewConversation">
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
            <button class="flex min-w-0 flex-1 items-center gap-2 text-left disabled:opacity-50" :disabled="busy" @click="selectConversation(c.id)">
              <Icon :name="modeIcon(c.mode)" size="sm" class="shrink-0 opacity-60" />
              <span class="min-w-0 flex-1 truncate">{{ c.title }}</span>
            </button>
            <button
              class="shrink-0 opacity-60 transition-opacity hover:text-red-500 lg:opacity-0 lg:group-hover:opacity-60 focus:opacity-100"
              :disabled="busy"
              :aria-label="t('playground.deleteConversation')"
              @click="removeConversation(c.id)"
            >
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </div>
      </aside>

      <!-- 对话区 -->
      <section class="flex min-h-0 min-w-0 flex-1 flex-col">

        <!-- 消息流 -->
        <div ref="scrollArea" class="pg-messages min-h-0 flex-1 overflow-y-auto px-4 py-5">
          <div v-if="!activeMessages.length" class="flex h-full flex-col items-center justify-center text-center">
            <ModelBrandMark :model="selectedModel" size="lg" class="mb-4" />
            <h2 class="max-w-full break-words text-xl font-semibold text-gray-900 dark:text-white">{{ selectedModel || t('playground.title') }}</h2>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ t(mode === 'chat' ? 'playground.chatPrompt' : mode === 'image' ? 'playground.imagePrompt' : 'playground.videoPrompt') }}</p>
          </div>

          <!-- 消息裹在 pg-column 里：不限宽的话宽屏下每行铺满整个工作区，
               用户气泡贴最右、回复贴最左，中间空出一大片，看着不像同一轮对话。
               滚动条仍在外层容器上，所以贴着窗口边缘，不会跟着内容缩进来。 -->
          <div class="pg-column space-y-5">
            <div
              v-for="msg in activeMessages"
              :key="msg.id"
              class="flex"
              :class="msg.role === 'user' ? 'justify-end' : 'justify-start'"
            >
              <div
                class="min-w-0 text-sm leading-relaxed"
                :class="msg.role === 'user'
                  ? 'max-w-[85%] rounded-lg bg-primary-50 px-4 py-3 text-gray-900 dark:bg-primary-900/30 dark:text-white'
                  : 'w-full text-gray-800 dark:text-dark-100'"
              >
                <p v-if="msg.content" class="whitespace-pre-wrap break-words">{{ msg.content }}</p>

                <div v-if="msg.mediaUrl || msg.mediaUrls?.length" class="pg-results">
                  <MediaResult v-for="(url, index) in msg.mediaUrls?.length ? msg.mediaUrls : [msg.mediaUrl!]" :key="url" :src="url" :kind="msg.mediaKind" :name="`${msg.taskId || msg.id}-${index + 1}`" />
                </div>

                <!-- 生成进度：只在「有任务、还没出结果、也没报错」时显示 -->
                <div v-if="msg.taskId && !msg.mediaUrl && !msg.error" class="mt-1 w-48">
                  <div class="flex items-center justify-between text-[11px] text-gray-500 dark:text-dark-400">
                    <span>{{ msg.mediaKind === 'video' ? t('playground.generatingVideo') : t('playground.generatingImage') }}</span>
                    <span class="font-mono">{{ msg.progress ?? 0 }}%</span>
                  </div>
                  <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700">
                    <div
                      class="h-full rounded-full bg-primary-500 transition-[width] duration-500"
                      :style="{ width: `${msg.progress ?? 0}%` }"
                    />
                  </div>
                </div>

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
        </div>

        <!-- 输入区 -->
        <footer class="shrink-0 border-t border-gray-100 p-3 dark:border-dark-700">
          <div class="pg-column rounded-lg border border-gray-200 p-2 dark:border-dark-600">
            <div v-if="costHint" class="mb-2 px-2 text-right text-xs text-gray-500 dark:text-gray-300">{{ costHint }}</div>
            <textarea
              v-model="draft"
              rows="3"
              :placeholder="t(mode === 'chat' ? 'playground.chatPrompt' : mode === 'image' ? 'playground.imagePrompt' : 'playground.videoPrompt')"
              :aria-label="t('playground.placeholder')"
              class="w-full resize-none bg-transparent px-2 py-1 text-sm outline-none dark:text-dark-100"
              @keydown.enter.exact="onEnter"
            />

            <!-- 参考图（图生视频）。上游只收公网 URL、且只收一张，所以选完立刻上传换地址，
                 而不是等到发送时再传——发送时才传的话，用户要为上传多等一次。 -->
            <div v-if="mode === 'video'" class="flex flex-wrap items-center gap-2 px-1 pb-2">
              <input ref="refImageInput" type="file" class="hidden" :accept="REFERENCE_IMAGE_TYPES.join(',')" @change="onPickReferenceImage" />
              <button
                v-if="!referenceImageUrl"
                type="button"
                data-testid="ref-image-pick"
                class="pg-ref-add"
                :disabled="busy || refImageUploading"
                @click="refImageInput?.click()"
              >
                <LoadingSpinner v-if="refImageUploading" size="sm" />
                <Icon v-else name="upload" size="sm" />
                {{ refImageUploading ? t('playground.refImageUploading') : t('playground.refImageAdd') }}
              </button>
              <div v-else data-testid="ref-image-chip" class="pg-ref-chip">
                <img :src="referenceImagePreview" :alt="t('playground.refImageAlt')" class="h-8 w-8 shrink-0 rounded object-cover" />
                <span class="min-w-0 flex-1 truncate">{{ referenceImageName }}</span>
                <button type="button" data-testid="ref-image-remove" class="pg-icon !h-6 !w-6" :aria-label="t('playground.refImageRemove')" :title="t('playground.refImageRemove')" @click="clearReferenceImage">
                  <Icon name="x" size="xs" />
                </button>
              </div>
              <span v-if="refImageError" class="text-xs text-red-500">{{ refImageError }}</span>
            </div>

            <div v-if="mode !== 'chat'" class="flex flex-wrap items-center gap-2 px-1 pb-3">
              <Select v-if="mode === 'video'" v-model="videoDuration" class="pg-param" :disabled="busy" :searchable="false" :aria-label="t('playground.paramDuration')" :options="durationOptions.map(value => ({ value, label: t('playground.seconds', { n: value }) }))">
                <template #selected="{ option }"><span class="param-label">{{ t('playground.paramDuration') }}</span> {{ option?.label }}</template>
              </Select>
              <Select v-model="aspectRatio" class="pg-param" :disabled="busy" :searchable="false" :aria-label="t('playground.paramAspect')" :options="ASPECT_OPTIONS.map(value => ({ value, label: value }))">
                <template #selected="{ option }"><span class="param-label">{{ t('playground.paramAspect') }}</span> {{ option?.label }}</template>
              </Select>
              <Select v-model="resolution" class="pg-param" :disabled="busy" :searchable="false" :aria-label="t('playground.paramResolution')" :options="resolutionOptions.map(r => ({ value: r.value, label: r.label + optionPriceSuffix(r.value, imageQuality) }))">
                <template #selected="{ option }"><span class="param-label">{{ t('playground.paramResolution') }}</span> {{ option?.label }}</template>
              </Select>
              <Select v-if="mode === 'image' && supportsQuality" v-model="imageQuality" class="pg-param" :disabled="busy" :searchable="false" :aria-label="t('playground.paramQuality')" :options="QUALITY_OPTIONS.map(q => ({ value: q.value, label: t(q.labelKey) + optionPriceSuffix(resolution, q.value) }))">
                <template #selected="{ option }"><span class="param-label">{{ t('playground.paramQuality') }}</span> {{ option?.label }}</template>
              </Select>
              <Select v-if="mode === 'image'" v-model="imageCount" class="pg-param" :disabled="busy" :searchable="false" :aria-label="t('playground.paramCount')" :options="[1, 2, 3, 4].map(value => ({ value, label: t('playground.images', { n: value }) }))">
                <template #selected="{ option }">{{ option?.label }}</template>
              </Select>
            </div>

            <div class="pg-composer-toolbar flex flex-wrap items-center gap-2 px-1">
              <button class="pg-icon pg-history-toggle" :aria-label="t('playground.conversations')" :title="t('playground.conversations')" :aria-expanded="historyOpen" @click="historyOpen = !historyOpen"><Icon name="chat" size="sm" /></button>
              <div class="flex gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-800" role="group" :aria-label="t('playground.modeLabel')">
                <button v-for="m in modes" :key="m.value" class="pg-mode" :class="{ 'pg-mode-active': mode === m.value }" :aria-pressed="mode === m.value" :disabled="busy" @click="switchMode(m.value)">
                  <Icon :name="m.icon" size="xs" />{{ t(m.labelKey) }}
                </button>
              </div>
              <!-- 自定义模型选择器。
                   原生 <select> 挂不了各厂商 logo，选项一多下拉还会拉满整屏
                   （27 个模型时几乎盖住整个页面）。这里限高滚动 + 按供应商分组。 -->
              <div v-if="availableModels.length" ref="modelPickerRef" class="pg-model-picker relative ml-auto w-64 max-w-full min-w-0" @keydown.esc="modelPickerOpen = false">
                <button
                  type="button"
                  data-testid="model-select"
                  class="flex h-9 w-full items-center gap-2 rounded-lg px-3 text-left text-sm text-gray-800 transition hover:bg-gray-100 focus-visible:outline-primary-500 dark:text-gray-100 dark:hover:bg-dark-800"
                  :disabled="busy"
                  :aria-label="t('playground.model')"
                  :title="selectedModel"
                  :aria-expanded="modelPickerOpen"
                  @click="modelPickerOpen = !modelPickerOpen"
                >
                  <ModelBrandMark :model="selectedModel" size="sm" />
                  <span class="min-w-0 flex-1 truncate">{{ selectedModel }}</span>
                  <Icon name="chevronDown" size="xs" class="shrink-0 opacity-50" />
                </button>

                <div
                  v-if="modelPickerOpen"
                  class="absolute right-0 bottom-full z-20 mb-1 max-h-[min(20rem,45dvh)] w-full min-w-0 overflow-y-auto rounded-lg border border-gray-200 bg-white p-1 shadow-lg dark:border-dark-600 dark:bg-dark-800"
                >
                  <input v-model="modelSearch" type="search" class="input sticky top-0 mb-1 text-sm" :placeholder="t('playground.searchModels')" :aria-label="t('playground.searchModels')" />
                  <p v-if="!groupedModels.length" class="p-3 text-sm text-gray-500">{{ t('playground.noSearchResults') }}</p>
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
              <RouterLink :to="{ path: '/model-plaza', query: { embedded: '1', model: selectedModel } }" class="pg-icon" :title="t('playground.pricing')" :aria-label="t('playground.pricing')"><Icon name="infoCircle" size="sm" /></RouterLink>
              <button
                class="btn btn-primary h-9 w-9 shrink-0 rounded-lg p-0"
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
import { RouterLink, useRoute } from 'vue-router'

import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelBrandMark from '@/components/modelPlaza/ModelBrandMark.vue'
import MediaResult from '@/components/playground/MediaResult.vue'
import Select from '@/components/common/Select.vue'
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
  mediaUrlsOf,
  isTerminalStatus,
  isFailedStatus,
  isInsufficientBalance,
  persistMedia,
  fetchConversations,
  pushConversation,
  removeConversation as removeRemoteConversation,
  uploadReferenceImage,
  REFERENCE_IMAGE_MAX_BYTES,
  REFERENCE_IMAGE_TYPES,
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
import { resolveModelVendor } from '@/utils/modelVendor'
import { BILLING_MODE_TOKEN } from '@/constants/channel'

const { t } = useI18n()
const authStore = useAuthStore()
const route = useRoute()

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
const historyOpen = ref(false)
const modelSearch = ref('')

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

// 供应商判定用共享的 resolveModelVendor（utils/modelVendor.ts）：
// 厂商标识组件与模型广场都用同一份，规则一旦分叉就会出现
// 「豆包分组里挂着 Google 标」这种自相矛盾的界面。
const groupedModels = computed(() => {
  const buckets = new Map<string, string[]>()
  for (const m of availableModels.value) {
    const vendor = resolveModelVendor(m).label
    if (!`${m} ${vendor}`.toLowerCase().includes(modelSearch.value.trim().toLowerCase())) continue
    if (!buckets.has(vendor)) buckets.set(vendor, [])
    buckets.get(vendor)!.push(m)
  }
  return [...buckets.entries()].map(([vendor, models]) => ({ vendor, models }))
})

function pickModel(m: string) {
  selectedModel.value = m
  modelPickerOpen.value = false
  modelSearch.value = ''
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
 * 图生视频的参考图。
 *
 * 存两个值：referenceImagePreview 是本地 objectURL，只用来显示缩略图；
 * referenceImageUrl 是上传后的公网地址，真正发给上游的是它。
 * 上游明确要求「不要直接传 base64，先调上传接口拿公网 URL」，两者不能混用。
 *
 * 选完图就立刻上传，而不是等到点发送——发送时才传的话，用户要为上传多等一次，
 * 而且上传失败会混在生成失败里，分不清是哪一步出的问题。
 */
const refImageInput = ref<HTMLInputElement | null>(null)
const referenceImageUrl = ref('')
const referenceImagePreview = ref('')
const referenceImageName = ref('')
const refImageUploading = ref(false)
const refImageError = ref('')

function releaseReferencePreview() {
  if (referenceImagePreview.value) URL.revokeObjectURL(referenceImagePreview.value)
  referenceImagePreview.value = ''
}

function clearReferenceImage() {
  releaseReferencePreview()
  referenceImageUrl.value = ''
  referenceImageName.value = ''
  refImageError.value = ''
  if (refImageInput.value) refImageInput.value.value = ''
}

async function onPickReferenceImage(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  // 取完就清空 input：否则再选同一个文件不会触发 change，用户以为点了没反应
  input.value = ''
  if (!file) return

  // 本地先拦一道，省得把注定失败的请求发上去。网关侧仍会再校验一次——
  // 前端校验只是体验，不是防线。
  if (!REFERENCE_IMAGE_TYPES.includes(file.type)) {
    refImageError.value = t('playground.refImageBadType')
    return
  }
  if (file.size > REFERENCE_IMAGE_MAX_BYTES) {
    refImageError.value = t('playground.refImageTooLarge')
    return
  }

  refImageError.value = ''
  refImageUploading.value = true
  const preview = URL.createObjectURL(file)
  try {
    const url = await uploadReferenceImage(file)
    releaseReferencePreview()
    referenceImagePreview.value = preview
    referenceImageUrl.value = url
    referenceImageName.value = file.name
  } catch (err) {
    URL.revokeObjectURL(preview)
    refImageError.value = err instanceof Error ? err.message : t('playground.refImageFailed')
  } finally {
    refImageUploading.value = false
  }
}

// 切走视频模式就丢掉参考图：留着它会在切回来时变成一张用户早已忘记的图，
// 而图生视频和文生视频的计费与产出完全不同，静默带上去风险太大。
watch(mode, value => {
  if (value !== 'video') clearReferenceImage()
})

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
    input: (input * rate * 10_000).toFixed(2),
    output: (output * rate * 10_000).toFixed(2)
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
  return `  ${formatCredits(per * entry.rate)}`
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

/**
 * 本地立刻存，服务端防抖推。
 *
 * 本地是同步的：刷新页面不能丢当前对话，所以 localStorage 一次不落。
 * 服务端走 600ms 防抖：流式对话每秒要改好几次内容，每次都发一遍 PUT
 * 等于把整条会话反复上传，既浪费也可能乱序。
 */
function persist() {
  saveConversations(conversations.value)
  schedulePush(activeConversation.value)
}

const pushTimers = new Map<string, ReturnType<typeof setTimeout>>()

function schedulePush(conv: PlaygroundConversation | null | undefined) {
  if (!conv) return
  const existing = pushTimers.get(conv.id)
  if (existing) clearTimeout(existing)
  pushTimers.set(conv.id, setTimeout(() => {
    pushTimers.delete(conv.id)
    void pushConversation(toSynced(conv))
  }, 600))
}

/** 把本地会话转成接口形状。字段名不同是因为后端用蛇形命名。 */
function toSynced(conv: PlaygroundConversation) {
  return {
    id: conv.id,
    title: conv.title,
    mode: conv.mode,
    model: conv.model,
    updated_at: conv.updatedAt,
    messages: conv.messages.map(m => ({
      id: m.id,
      role: m.role,
      content: m.content,
      // 统一成数组：老会话只有 mediaUrl 单值，不转的话服务端存不到它
      media_urls: m.mediaUrls?.length ? m.mediaUrls : (m.mediaUrl ? [m.mediaUrl] : []),
      media_kind: m.mediaKind ?? null,
      task_id: m.taskId ?? null,
      error: m.error ?? null,
      needs_top_up: Boolean(m.needsTopUp),
      created_at: m.createdAt
    }))
  }
}

/** 接口形状转回本地会话。 */
function fromSynced(raw: Awaited<ReturnType<typeof fetchConversations>> extends (infer T)[] | null ? T : never): PlaygroundConversation {
  const urls = (m: { media_urls?: string[] | null }) => m.media_urls ?? []
  return {
    id: raw.id,
    title: raw.title,
    mode: (raw.mode === 'image' || raw.mode === 'video' ? raw.mode : 'chat') as PlaygroundMode,
    model: raw.model,
    updatedAt: raw.updated_at,
    messages: (raw.messages ?? []).map(m => ({
      id: m.id,
      role: (m.role === 'user' ? 'user' : 'assistant') as 'user' | 'assistant',
      content: m.content,
      mediaUrls: urls(m),
      mediaUrl: urls(m)[0],
      mediaKind: (m.media_kind ?? undefined) as 'image' | 'video' | undefined,
      taskId: m.task_id ?? undefined,
      error: m.error ?? undefined,
      needsTopUp: Boolean(m.needs_top_up),
      createdAt: m.created_at ?? Date.now()
    }))
  }
}

function scrollToBottom() {
  void nextTick(() => {
    if (scrollArea.value) scrollArea.value.scrollTop = scrollArea.value.scrollHeight
  })
}

function startNewConversation() {
  historyOpen.value = false
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
  historyOpen.value = false
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
  saveConversations(conversations.value)
  // 服务端也要删：只删本地的话，换台设备它还在，下次同步又被拉回来
  const pending = pushTimers.get(id)
  if (pending) {
    clearTimeout(pending)
    pushTimers.delete(id)
  }
  void removeRemoteConversation(id)
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

function onEnter(event: KeyboardEvent) {
  if (event.isComposing) return
  event.preventDefault()
  void submit()
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

/**
 * 结果到手就转存到本站，把会话里存的地址换成永久地址。
 *
 * 上游结果 24 小时后过期，而会话存的是**地址**不是图片本身——不转存的话，
 * 隔天重新打开这条会话只会看到一排碎图，用户会以为是我们把图弄丢了。
 *
 * 先把上游地址写进会话再异步替换：转存要把整个文件下载一遍，让用户干等
 * 这一步没道理；万一转存失败，退回的就是上游地址，这一刻照样看得见。
 */
async function adoptMedia(
  conv: PlaygroundConversation,
  reply: PlaygroundMessage,
  urls: string[],
  kind: 'image' | 'video'
) {
  reply.mediaUrls = urls
  reply.mediaUrl = urls[0]
  conv.updatedAt = Date.now()
  persist()
  scrollToBottom()

  const stored = await persistMedia(urls, kind, reply.taskId || '')
  if (stored.some((url, i) => url !== urls[i])) {
    reply.mediaUrls = stored
    reply.mediaUrl = stored[0]
    conv.updatedAt = Date.now()
    persist()
  }
}

async function runMedia(conv: PlaygroundConversation, prompt: string) {
  const isVideo = mode.value === 'video'
  busyHint.value = isVideo ? t('playground.generatingVideo') : t('playground.generatingImage')

  // size 是画幅比例、resolution 才是清晰度档位 —— 与 OpenAI 的语义相反，
  // 这是 toAPI 的约定（见 quickstart 文档），传反了会被上游拒绝。
  const task = isVideo
    ? await createVideoTask(conv.model, prompt, {
        resolution: resolution.value,
        duration: videoDuration.value,
        // 有参考图就是图生视频，没有就是文生视频；上游字段名固定叫 image
        image: referenceImageUrl.value || undefined
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
    mediaKind: isVideo ? 'video' : 'image',
    createdAt: Date.now()
  })

  // 同步渠道一次就把结果带回来了，不必再轮询。
  const direct = mediaUrlsOf(task)
  if (direct.length) {
    await adoptMedia(conv, reply, direct, isVideo ? 'video' : 'image')
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

    // 上游在任务查询里回 progress（0~100）。给真实百分比而不是一条空转的动画——
    // 生图要一分钟上下，没有进度用户不知道是在跑还是卡死了。
    if (typeof result.progress === 'number' && result.progress >= 0) {
      reply.progress = Math.min(100, Math.round(result.progress))
      conv.updatedAt = Date.now()
    }

    const urls = mediaUrlsOf(result)
    // 拿到地址就算完成——有的渠道结果就绪时不再回传 status。
    if (urls.length) {
      await adoptMedia(conv, reply, urls, isVideo ? 'video' : 'image')
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

/**
 * 续上离开页面时还没完成的生成任务。
 *
 * 任务在服务端照常跑，是前端在 onBeforeUnmount 里停掉了轮询——切走再回来，
 * 那条消息就永远停在灰条上，图其实早就生成好了。所以重进页面时把所有
 * 「有 taskId、既没结果也没报错」的消息重新接上轮询。
 *
 * 逐条 await 而不是并发：这类任务通常只有一两条，并发除了把请求打散没别的好处。
 */
async function resumePendingTasks() {
  for (const conv of conversations.value) {
    for (const msg of conv.messages) {
      if (!msg.taskId || msg.mediaUrl || msg.error) continue
      const isVideo = msg.mediaKind === 'video'
      busy.value = true
      busyHint.value = isVideo ? t('playground.resumingVideo') : t('playground.resumingImage')
      try {
        await pollTask(conv, msg, msg.taskId, isVideo)
      } catch (err) {
        msg.error = err instanceof Error ? err.message : String(err)
        persist()
      } finally {
        busy.value = false
        busyHint.value = ''
      }
      if (disposed) return
    }
  }
}

onMounted(async () => {
  document.addEventListener('click', onDocumentClick)
  // 从模型广场「去体验」跳过来时带着 ?model=&mode=，直接落到那个模型，
  // 省得用户在下拉里再找一遍。模型名对不上就忽略，不要报错吓人。
  const wantedMode = String(route.query.mode ?? '')
  if (wantedMode === 'text') mode.value = 'chat'
  else if (wantedMode === 'chat' || wantedMode === 'image' || wantedMode === 'video') {
    mode.value = wantedMode
  }
  // 先用本地缓存把界面撑起来，再用服务端数据覆盖——服务端是真源，
  // 但拉取要一次往返，这期间让用户盯着空白列表没必要。
  conversations.value = loadConversations()
  const remote = await fetchConversations()
  if (remote) {
    // null 才是「拉不到」，空数组是「这个账号确实没有会话」——
    // 混为一谈的话，新设备首次登录会把空列表当成同步失败而显示旧缓存。
    conversations.value = remote.map(fromSynced)
    saveConversations(conversations.value)
  }
  activeId.value = conversations.value[0]?.id ?? ''
  if (activeConversation.value && !wantedMode) mode.value = activeConversation.value.mode
  await loadModels()
  const wantedModel = String(route.query.model ?? '')
  if (wantedModel && availableModels.value.includes(wantedModel)) {
    selectedModel.value = wantedModel
  }

  void resumePendingTasks()
})

onBeforeUnmount(() => {
  // 防抖窗口里离开页面会丢掉最后一次改动，这里补发一次
  for (const [id, timer] of pushTimers) {
    clearTimeout(timer)
    const conv = conversations.value.find(c => c.id === id)
    if (conv) void pushConversation(toSynced(conv))
  }
  pushTimers.clear()
  releaseReferencePreview()
  document.removeEventListener('click', onDocumentClick)
  disposed = true
  abortController?.abort()
  if (pollTimer) clearTimeout(pollTimer)
})
</script>

<style scoped>
.pg-param { max-width: 100%; }
.pg-param :deep(.select-trigger) { @apply h-9 gap-2 rounded-lg border-transparent bg-gray-50 px-3 py-1 text-xs hover:bg-gray-100 dark:bg-dark-800 dark:hover:bg-dark-700; }
.pg-param :deep(.select-value) { @apply flex items-center gap-2; }

.param-label {
  @apply text-gray-400 dark:text-dark-500;
}

.pg-workspace { @apply relative flex min-h-0 overflow-hidden bg-white dark:bg-dark-900; height: calc(100dvh - 8rem - 1px); }
.pg-history { @apply w-56 shrink-0 flex-col border-r border-gray-100 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-950; }
.pg-icon { @apply inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-700; }
.pg-mode { @apply inline-flex items-center gap-1 rounded-md px-2.5 py-1.5 text-xs text-gray-500 disabled:opacity-50; }
.pg-mode-active { @apply bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300; }
/* 对话列的可读宽度。消息行是 justify-end / justify-start 的 flex，不限宽的话
   宽屏（工作区可达 1500px 以上）下用户气泡贴最右、回复贴最左，中间空出一大片，
   看着不像同一轮对话。输入区共用这个宽度，上下两块边缘才对得齐。 */
.pg-column { width: 100%; max-width: 56rem; margin-inline: auto; }
.pg-ref-add { @apply inline-flex items-center gap-1.5 rounded-lg border border-dashed border-gray-300 px-3 py-1.5 text-xs text-gray-600 hover:border-primary-400 hover:text-primary-600 disabled:opacity-50 dark:border-dark-600 dark:text-gray-300; }
.pg-ref-chip { @apply inline-flex max-w-xs items-center gap-2 rounded-lg border border-gray-200 bg-gray-50 py-1 pl-1 pr-1.5 text-xs text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200; }
.pg-results { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: flex-start; gap: 12px; margin-top: 8px; }
@media (min-width: 1024px) { .pg-history-toggle { display: none; } }
@media (max-width: 1023px) {
  .pg-history { position: absolute; inset: 0 auto 0 0; z-index: 30; box-shadow: 8px 0 16px rgb(0 0 0 / 8%); }
}
@media (max-width: 767px) {
  .pg-model-picker { width: min(256px, calc(100% - 88px)); }
  .pg-composer-toolbar > [role="group"] { margin-right: calc(100% - 240px); }
  .pg-workspace { height: calc(100dvh - 6rem - 1px); }
}
</style>
