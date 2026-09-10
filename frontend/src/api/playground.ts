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

/**
 * 把网关返回的错误体解析成可读信息；解析不出就退回状态码。
 *
 * 错误码有两种位置：OpenAI 风格嵌在 error 里，网关自己的鉴权/计费错误放在顶层
 * （实测余额不足回的是 {"code":"INSUFFICIENT_BALANCE","message":...}）。两处都取，
 * 否则调用方拿不到码，只能拿一句英文原文去展示。
 */
async function toError(response: Response): Promise<Error> {
  const payload = (await response.json().catch(() => ({}))) as {
    error?: { message?: string; code?: string }
    message?: string
    code?: string
  }
  const message = payload.error?.message || payload.message || `请求失败 (${response.status})`
  const error = new Error(message)
  Object.assign(error, {
    status: response.status,
    code: payload.error?.code || payload.code
  })
  return error
}

/** 该错误是否为「余额不足」。 */
export function isInsufficientBalance(err: unknown): boolean {
  if (!(err instanceof Error)) return false
  return (err as Error & { code?: string }).code === 'INSUFFICIENT_BALANCE'
}

export interface PlaygroundModel {
  id: string
}

/**
 * 列出当前用户可调的模型。
 *
 * 走网关 /v1/models 而不是模型广场：广场按定价配置拼装，会漏掉没配定价行的
 * 模型——线上实测组 4 有 7 个可调模型，广场只回 6 个，漏的正是视频模型。
 */
export async function listModels(options?: { signal?: AbortSignal }): Promise<string[]> {
  const response = await fetch(buildApiUrl('/playground/models'), {
    headers: authHeaders(),
    signal: options?.signal
  })
  if (!response.ok) throw await toError(response)
  const payload = (await response.json()) as { data?: PlaygroundModel[] }
  return (payload.data ?? []).map((m) => m.id).filter(Boolean)
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
  opts: { size?: string; resolution?: string; n?: number; quality?: string },
  options?: { signal?: AbortSignal }
): Promise<MediaTaskResult> {
  const response = await fetch(buildApiUrl('/playground/images/generations'), {
    method: 'POST',
    headers: authHeaders(),
    // quality 必须带上：上游按「清晰度 × 质量」分档收费，不带就按低质量计费
    body: JSON.stringify({
      model,
      prompt,
      n: opts.n ?? 1,
      size: opts.size,
      resolution: opts.resolution,
      quality: opts.quality
    }),
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

export interface MediaTaskItem {
  url?: string
  b64_json?: string
}

export interface MediaTaskResult extends MediaTask {
  data?: MediaTaskItem[]
  url?: string
  /**
   * 异步生图任务完成时，结果套在 result 里：
   *   {"status":"completed","result":{"type":"image","data":[{"url":"https://..."}]}}
   * 只看顶层 url / data 会取不到，界面就报「任务已完成，但没有返回可用的结果地址」。
   */
  result?: {
    type?: string
    url?: string
    data?: MediaTaskItem[]
  }
}

/**
 * 把上游媒体地址换成本站中转地址。
 *
 * 上游把图片放在 files.toapis.cn，部分网络环境访问不到那个域名——用户换了出口
 * IP 后页面上就只剩碎图。服务器侧一直是通的，所以统一走 /playground/media 代取。
 * 非 http(s) 的（如 base64 data URI）原样返回，不必绕一圈。
 */
export function proxiedMediaUrl(url: string): string {
  if (!url || !/^https?:\/\//i.test(url)) return url
  return buildApiUrl(`/playground/media?url=${encodeURIComponent(url)}`)
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
  const pick = (item?: MediaTaskItem): string => {
    if (!item) return ''
    if (item.url) return item.url
    if (item.b64_json) return `data:image/png;base64,${item.b64_json}`
    return ''
  }
  if (task.url) return task.url
  const direct = pick(task.data?.[0])
  if (direct) return direct
  // 异步任务完成时结果套在 result 里，必须往里再找一层
  if (task.result?.url) return task.result.url
  return pick(task.result?.data?.[0])
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
