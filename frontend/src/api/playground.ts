import { buildApiUrl } from './url'

/**
 * 在线使用页的网关代理客户端。
 *
 * 走 /api/v1/playground/* 而不是直连 /v1/*：网页端拿不到明文 API key
 * （只在创建时显示一次），后端用登录态 JWT 认证后再取该用户自己的密钥调网关。
 */

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system'
  content: string
}

export interface StreamCallbacks {
  /** 每收到一个增量片段回调一次 */
  onDelta: (text: string) => void
  /** 流正常结束 */
  onDone?: () => void
}

function authHeaders(): Record<string, string> {
  return {
    Authorization: `Bearer ${localStorage.getItem('auth_token') ?? ''}`,
    'Content-Type': 'application/json'
  }
}

/** 把网关返回的错误体解析成可读信息；解析不出就退回状态码。 */
async function toError(response: Response): Promise<Error> {
  const payload = (await response.json().catch(() => ({}))) as {
    error?: { message?: string; code?: string }
    message?: string
  }
  const message = payload.error?.message || payload.message || `请求失败 (${response.status})`
  const error = new Error(message)
  Object.assign(error, { status: response.status, code: payload.error?.code })
  return error
}

/**
 * 流式对话。
 *
 * 用 fetch + ReadableStream 而不是 EventSource：后者只支持 GET，带不了消息体，
 * 也带不了 Authorization 头。
 */
export async function streamChat(
  model: string,
  messages: ChatMessage[],
  callbacks: StreamCallbacks,
  options?: { signal?: AbortSignal }
): Promise<void> {
  const response = await fetch(buildApiUrl('/playground/chat/completions'), {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ model, messages, stream: true }),
    signal: options?.signal
  })
  if (!response.ok) throw await toError(response)

  const reader = response.body?.getReader()
  if (!reader) throw new Error('响应没有可读流')

  const decoder = new TextDecoder()
  let buffer = ''

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })

    // SSE 以空行分隔事件，但增量片段一行一个 data:，按行切分即可。
    // 最后一段可能不完整，留在 buffer 里等下一个 chunk 补齐。
    const lines = buffer.split('\n')
    buffer = lines.pop() ?? ''

    for (const line of lines) {
      const trimmed = line.trim()
      if (!trimmed.startsWith('data:')) continue
      const payload = trimmed.slice(5).trim()
      if (!payload) continue
      if (payload === '[DONE]') {
        callbacks.onDone?.()
        return
      }
      try {
        const chunk = JSON.parse(payload) as {
          choices?: Array<{ delta?: { content?: string } }>
        }
        const delta = chunk.choices?.[0]?.delta?.content
        if (delta) callbacks.onDelta(delta)
      } catch {
        // 单个片段解析失败不该中断整条流——跳过继续读。
      }
    }
  }
  callbacks.onDone?.()
}

export interface MediaTask {
  id?: string
  /** 上游对任务 id 的字段名不统一，两个都可能出现。 */
  task_id?: string
  status?: string
  progress?: number
  error?: { code?: string; message?: string }
}

/**
 * 提交生图任务。
 *
 * 返回体有两种形态：同步渠道直接带 data[].url，异步渠道只给 task id。
 * 这里不做归一化，交给调用方用 mediaTaskId / mediaUrlOf 判别。
 */
export async function createImageTask(
  model: string,
  prompt: string,
  opts: { size?: string; resolution?: string; n?: number },
  options?: { signal?: AbortSignal }
): Promise<MediaTaskResult> {
  const response = await fetch(buildApiUrl('/playground/images/generations'), {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ model, prompt, n: opts.n ?? 1, size: opts.size, resolution: opts.resolution }),
    signal: options?.signal
  })
  if (!response.ok) throw await toError(response)
  return (await response.json()) as MediaTaskResult
}

/** 查询生图任务状态。异步生图的退款同样挂在这条路径上。 */
export async function getImageTask(
  taskId: string,
  options?: { signal?: AbortSignal }
): Promise<MediaTaskResult> {
  const response = await fetch(
    buildApiUrl(`/playground/images/generations/${encodeURIComponent(taskId)}`),
    { headers: authHeaders(), signal: options?.signal }
  )
  if (!response.ok) throw await toError(response)
  return (await response.json()) as MediaTaskResult
}

/** 提交生视频任务（异步，返回 task id）。 */
export async function createVideoTask(
  model: string,
  prompt: string,
  opts: { resolution?: string; duration?: number },
  options?: { signal?: AbortSignal }
): Promise<MediaTaskResult> {
  const response = await fetch(buildApiUrl('/playground/videos/generations'), {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ model, prompt, resolution: opts.resolution, duration: opts.duration }),
    signal: options?.signal
  })
  if (!response.ok) throw await toError(response)
  return (await response.json()) as MediaTaskResult
}

export interface MediaTaskResult extends MediaTask {
  data?: Array<{ url?: string; b64_json?: string }>
  url?: string
}

/** 取任务 id。上游用 id 还是 task_id 不统一，两个都认。 */
export function mediaTaskId(task: MediaTaskResult): string {
  return task.id || task.task_id || ''
}

/**
 * 从任务结果里取出可直接渲染的媒体地址。
 *
 * 覆盖三种返回形态：顶层 url、data[].url、data[].b64_json（转成 data URI）。
 * 取不到返回空串，由调用方决定是继续轮询还是报错。
 */
export function mediaUrlOf(task: MediaTaskResult): string {
  if (task.url) return task.url
  const first = task.data?.[0]
  if (!first) return ''
  if (first.url) return first.url
  if (first.b64_json) return `data:image/png;base64,${first.b64_json}`
  return ''
}

/**
 * 查询视频任务状态。
 *
 * 必须走代理：失败任务的退款挂在这条路径上，前端直连网关拿不到用户密钥，
 * 退款就永远触发不了。
 */
export async function getVideoTask(
  taskId: string,
  options?: { signal?: AbortSignal }
): Promise<MediaTaskResult> {
  const response = await fetch(buildApiUrl(`/playground/videos/${encodeURIComponent(taskId)}`), {
    headers: authHeaders(),
    signal: options?.signal
  })
  if (!response.ok) throw await toError(response)
  return (await response.json()) as MediaTaskResult
}

/** 任务是否已进入终态。 */
export function isTerminalStatus(status: string | undefined): boolean {
  const s = (status ?? '').toLowerCase()
  return ['success', 'succeeded', 'completed', 'failed', 'failure', 'error', 'cancelled', 'canceled'].includes(s)
}

/** 终态是否为失败。 */
export function isFailedStatus(status: string | undefined): boolean {
  const s = (status ?? '').toLowerCase()
  return ['failed', 'failure', 'error', 'cancelled', 'canceled'].includes(s)
}
