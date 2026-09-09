<template>
  <AppLayout>
    <div class="flex h-[calc(100vh-11rem)] min-h-[32rem] gap-4">
      <!-- 会话列表 -->
      <aside class="hidden w-60 shrink-0 flex-col card p-3 lg:flex">
        <button class="btn-primary w-full justify-center" @click="startNewConversation">
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
        <!-- 顶栏：模型选择 -->
        <header class="flex flex-wrap items-center gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <Icon :name="modeIcon(mode)" size="sm" class="text-primary-500" />
          <select
            v-model="selectedModel"
            class="input h-9 max-w-[16rem] py-1 text-sm"
            :aria-label="t('playground.model')"
          >
            <option v-if="!availableModels.length" value="">{{ t('playground.noModels') }}</option>
            <option v-for="m in availableModels" :key="m" :value="m">{{ m }}</option>
          </select>
          <span class="ml-auto text-xs text-gray-400">
            {{ t('playground.balance') }}: {{ balanceText }}
          </span>
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
            </div>
          </div>

          <div v-if="busy" class="flex items-center gap-2 text-xs text-gray-400">
            <LoadingSpinner size="sm" />
            <span>{{ busyHint }}</span>
          </div>
        </div>

        <!-- 输入区 -->
        <footer class="border-t border-gray-100 p-3 dark:border-dark-700">
          <div class="rounded-xl border border-gray-200 p-2 dark:border-dark-600">
            <textarea
              v-model="draft"
              rows="2"
              :placeholder="t('playground.placeholder')"
              class="w-full resize-none bg-transparent px-2 py-1 text-sm outline-none dark:text-dark-100"
              @keydown.enter.exact.prevent="submit"
            />
            <div class="flex flex-wrap items-center gap-2 px-1">
              <!-- 三模式共用同一套界面，切换不跳页 -->
              <button
                v-for="m in modes"
                :key="m.value"
                class="rounded-lg px-2.5 py-1 text-xs transition-colors"
                :class="mode === m.value
                  ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/40 dark:text-primary-200'
                  : 'text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800'"
                @click="switchMode(m.value)"
              >
                <Icon :name="m.icon" size="xs" class="mr-1" />{{ t(m.labelKey) }}
              </button>

              <button
                class="btn-primary ml-auto h-8 w-8 shrink-0 justify-center rounded-full p-0"
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

import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getModelPlaza } from '@/api/modelPlaza'
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
const balanceText = computed(() => formatCredits(authStore.user?.balance ?? 0))

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
    const message = err instanceof Error ? err.message : String(err)
    appendMessage(conv, {
      id: newId(),
      role: 'assistant',
      content: '',
      error: message,
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
    ? await createVideoTask(conv.model, prompt, { resolution: '720p', duration: 8 })
    : await createImageTask(conv.model, prompt, { size: '1:1', resolution: '1k', n: 1 })

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
  try {
    names = await listModels()
  } catch {
    // 名单拿不到不该让页面白屏——历史会话仍可查看。
    chatModels.value = []
    imageModels.value = []
    videoModels.value = []
    return
  }

  // 广场只是补充元数据，拿不到就退回按名字判断，不影响名单本身。
  const billingModes = new Map<string, string>()
  try {
    const plaza = await getModelPlaza()
    for (const group of plaza.groups ?? []) {
      for (const m of group.models ?? []) {
        if (m.pricing?.billing_mode) billingModes.set(m.name, m.pricing.billing_mode)
      }
    }
  } catch {
    /* 广场关闭时会 404，忽略 */
  }

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

onMounted(async () => {
  conversations.value = loadConversations()
  activeId.value = conversations.value[0]?.id ?? ''
  if (activeConversation.value) mode.value = activeConversation.value.mode
  await loadModels()
})

onBeforeUnmount(() => {
  disposed = true
  abortController?.abort()
  if (pollTimer) clearTimeout(pollTimer)
})
</script>
