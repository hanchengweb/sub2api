import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchConversations, pushConversation, removeConversation } from '../playground'

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

const CONV = {
  id: 'c1',
  title: '你好',
  mode: 'image',
  model: 'gpt-image-2.5-flare',
  updated_at: 1789000000000,
  messages: [{ id: 'm1', role: 'user', content: '你好', media_urls: [] }]
}

describe('会话跨设备同步', () => {
  it('拉取失败回 null，而不是空数组', async () => {
    // 这两者必须分开：null 是「拉不到，退回本地缓存」，
    // 空数组是「这个账号确实没有会话」。混为一谈会让新设备首次登录
    // 把空列表当成同步失败，转而显示上一个账号的旧缓存。
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))
    expect(await fetchConversations()).toBeNull()

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 500, json: async () => ({}) }))
    expect(await fetchConversations()).toBeNull()

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: [] }) }))
    expect(await fetchConversations()).toEqual([])
  })

  it('按会话 id 推送到自己的路径', async () => {
    localStorage.setItem('auth_token', 'test-session')
    const fetcher = vi.fn().mockResolvedValue({ ok: true })
    vi.stubGlobal('fetch', fetcher)

    expect(await pushConversation(CONV)).toBe(true)
    const [url, init] = fetcher.mock.calls[0]
    expect(url).toContain('/playground/conversations/c1')
    expect(init.method).toBe('PUT')
    expect(init.headers.Authorization).toBe('Bearer test-session')
    expect(JSON.parse(init.body)).toMatchObject({ id: 'c1', mode: 'image' })
  })

  it('会话 id 要转义，不能拼出越权路径', async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true })
    vi.stubGlobal('fetch', fetcher)
    await removeConversation('../conversations/999')
    expect(fetcher.mock.calls[0][0]).not.toContain('/conversations/../')
    expect(fetcher.mock.calls[0][1].method).toBe('DELETE')
  })

  it('同步失败不抛错，只返回 false', async () => {
    // 同步断了用户还该能照常对话——把后台同步失败弹进对话流只会打断他。
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))
    expect(await pushConversation(CONV)).toBe(false)
    expect(await removeConversation('c1')).toBe(false)
  })
})
