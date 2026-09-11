import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchMediaBlob, persistMedia, proxiedMediaUrl } from '../playground'

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

const SOURCE = 'https://files.toapis.cn/images/a.png'
const STORED = '/api/v1/playground/media/42'

describe('生成结果转存', () => {
  it('把上游地址换成本站永久地址', async () => {
    localStorage.setItem('auth_token', 'test-session')
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: [{ source_url: SOURCE, url: STORED, persisted: true }] })
    })
    vi.stubGlobal('fetch', fetcher)

    expect(await persistMedia([SOURCE], 'image', 'tsk_1')).toEqual([STORED])
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({
      urls: [SOURCE], kind: 'image', task_id: 'tsk_1'
    })
  })

  it('转存失败就退回上游地址，不能让用户当场看不到图', async () => {
    // 上游结果还能撑 24 小时，这一刻照样该显示；抛错会让整条消息变成错误态。
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('boom')))
    expect(await persistMedia([SOURCE], 'image', 'tsk_1')).toEqual([SOURCE])

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 507, json: async () => ({}) }))
    expect(await persistMedia([SOURCE], 'image', 'tsk_1')).toEqual([SOURCE])
  })

  it('本站转存地址不再套一层媒体中转', async () => {
    // 套了会变成 /playground/media?url=/api/v1/... ——代理只认 https 上游地址，直接 400
    expect(proxiedMediaUrl(STORED)).toBe(STORED)
    expect(proxiedMediaUrl(SOURCE)).toContain('/playground/media?url=')
  })

  it('本站转存地址能直接取到 blob', async () => {
    localStorage.setItem('auth_token', 'test-session')
    const blob = new Blob(['png'], { type: 'image/png' })
    const fetcher = vi.fn().mockResolvedValue({ ok: true, blob: async () => blob })
    vi.stubGlobal('fetch', fetcher)

    expect(await fetchMediaBlob(STORED)).toBe(blob)
    // 相对路径不该被当成「不支持的地址」挡掉
    expect(fetcher.mock.calls[0][0]).toBe(STORED)
  })
})
