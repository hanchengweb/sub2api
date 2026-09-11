import { afterEach, describe, expect, it, vi } from 'vitest'
import { createVideoTask, uploadReferenceImage } from '../playground'

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe('图生视频的参考图链路', () => {
  it('走 playground 代理上传，字段名必须是 file', async () => {
    localStorage.setItem('auth_token', 'test-session')
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: { url: 'https://files.toapis.cn/uploads/1/a.png' } })
    })
    vi.stubGlobal('fetch', fetcher)

    const file = new File(['png'], 'cat.png', { type: 'image/png' })
    expect(await uploadReferenceImage(file)).toBe('https://files.toapis.cn/uploads/1/a.png')

    const [url, init] = fetcher.mock.calls[0]
    // 必须走代理：网页端只有 JWT，网关的 /v1/uploads/images 认的是 API key
    expect(url).toContain('/playground/uploads/images')
    expect(init.headers).toEqual({ Authorization: 'Bearer test-session' })
    // 不能自己写 Content-Type，否则会漏掉 multipart 的 boundary
    expect(init.headers['Content-Type']).toBeUndefined()
    // 字段名固定 file——网关按这个名字取，改了会报 exactly one file field is required
    expect((init.body as FormData).get('file')).toBe(file)
  })

  it('上传成功但没返回地址要报错，而不是把空串当成功', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: {} }) }))
    await expect(uploadReferenceImage(new File([''], 'a.png', { type: 'image/png' })))
      .rejects.toThrow('没有返回图片地址')
  })

  it('有参考图才带 image 字段，没有就是文生视频', async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ id: 'tsk_vid_1' }) })
    vi.stubGlobal('fetch', fetcher)

    await createVideoTask('t-grok-video-1.5', '让猫动起来', {
      resolution: '720p',
      duration: 8,
      image: 'https://files.toapis.cn/uploads/1/a.png'
    })
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({
      model: 't-grok-video-1.5',
      resolution: '720p',
      duration: 8,
      image: 'https://files.toapis.cn/uploads/1/a.png'
    })

    await createVideoTask('t-grok-video-1.5', '一只猫在走路', { resolution: '720p', duration: 8 })
    // 文生视频不能带空的 image：上游收到空串会当成非法 URL 直接拒
    expect(JSON.parse(fetcher.mock.calls[1][1].body)).not.toHaveProperty('image')
  })
})
