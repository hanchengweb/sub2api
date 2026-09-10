import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchMediaBlob, mediaUrlsOf } from '../playground'

afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })
describe('authenticated playground media', () => {
  it('sends the session only to the proxy and retains all results', async () => {
    localStorage.setItem('auth_token', 'test-session')
    const blob = new Blob(['png'], { type: 'image/png' })
    const fetcher = vi.fn().mockResolvedValue({ ok: true, blob: async () => blob })
    vi.stubGlobal('fetch', fetcher)
    const source = 'https://files.toapis.cn/images/a.png'
    expect(await fetchMediaBlob(source)).toBe(blob)
    expect(fetcher).toHaveBeenCalledWith(expect.stringContaining('/playground/media?url=' + encodeURIComponent(source)), {
      headers: { Authorization: 'Bearer test-session' }, signal: undefined
    })
    expect(mediaUrlsOf({ result: { data: [{ url: source }, { url: 'https://files.toapis.cn/b.png' }] } })).toHaveLength(2)
    await fetchMediaBlob('data:image/png;base64,cG5n')
    expect(fetcher.mock.calls[1][1].headers).toBeUndefined()
  })
  it('preserves 401 for login guidance and rejects unsafe media', async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: false, status: 401, json: async () => ({ message: 'Unauthorized' }) })
    vi.stubGlobal('fetch', fetcher)
    await expect(fetchMediaBlob('https://files.toapis.cn/a.png')).rejects.toMatchObject({ status: 401 })
    await expect(fetchMediaBlob('javascript:alert(1)')).rejects.toThrow('Unsupported')
    fetcher.mockResolvedValue({ ok: true, blob: async () => new Blob(['<svg/>'], { type: 'image/svg+xml' }) })
    await expect(fetchMediaBlob('https://files.toapis.cn/a.png')).rejects.toThrow('Invalid media')
  })
})
