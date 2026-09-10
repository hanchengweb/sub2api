/**
 * 在线使用页的会话本地存储。
 *
 * 第一版存浏览器本地而非数据库：零后端改动、零存储成本，最快落地。
 * 代价是换浏览器/设备就没了，清缓存也会丢——这是已知取舍，不是疏漏。
 */

export type PlaygroundMode = 'chat' | 'image' | 'video'

export interface PlaygroundMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  /** 生图/生视频结果的媒体地址 */
  mediaUrl?: string
  /** 异步任务 id；未完成时用它在重进页面后续上轮询 */
  taskId?: string
  /** 该任务是图还是视频——恢复轮询时要用它选对查询接口。
   *  不能靠会话的 mode 推断：用户可能在等待期间切换了模式。 */
  mediaKind?: 'image' | 'video'
  /** 生成进度 0~100；上游在任务查询里回 progress，用来给用户一个真实的百分比 */
  progress?: number
  /** 该条消息是否处于错误态 */
  error?: string
  /** 错误是「余额不足」——渲染时额外给一个兑换入口 */
  needsTopUp?: boolean
  createdAt: number
}

export interface PlaygroundConversation {
  id: string
  title: string
  mode: PlaygroundMode
  model: string
  messages: PlaygroundMessage[]
  updatedAt: number
}

const STORAGE_KEY = 'playground_conversations_v1'

/** 会话数上限。超出后丢弃最旧的——localStorage 通常只有 5MB，写爆会直接抛异常。 */
const MAX_CONVERSATIONS = 50
/** 单会话消息数上限，同上考虑。 */
const MAX_MESSAGES_PER_CONVERSATION = 200

export function newId(): string {
  // crypto.randomUUID 在非 HTTPS 的旧环境可能没有，退回时间戳 + 随机数。
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
}

export function loadConversations(): PlaygroundConversation[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed as PlaygroundConversation[]
  } catch {
    // 存储损坏或被禁用时当作空列表，不能让页面打不开。
    return []
  }
}

export function saveConversations(list: PlaygroundConversation[]): void {
  const trimmed = list
    .slice()
    .sort((a, b) => b.updatedAt - a.updatedAt)
    .slice(0, MAX_CONVERSATIONS)
    .map((c) => ({
      ...c,
      messages: c.messages.slice(-MAX_MESSAGES_PER_CONVERSATION)
    }))
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(trimmed))
  } catch {
    // 配额写满时退一步：只保留最近 10 个会话再试一次，仍失败就放弃，
    // 不能因为存不下历史而让当前对话中断。
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(trimmed.slice(0, 10)))
    } catch {
      /* 放弃持久化 */
    }
  }
}

/** 用首条用户消息生成会话标题。 */
export function deriveTitle(text: string): string {
  const clean = text.replace(/\s+/g, ' ').trim()
  if (!clean) return '新对话'
  return clean.length > 24 ? `${clean.slice(0, 24)}…` : clean
}
