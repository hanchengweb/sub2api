import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import MediaResult from './MediaResult.vue'
import { fetchMediaBlob } from '@/api/playground'

vi.mock('@/api/playground', () => ({ fetchMediaBlob: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('media result lifecycle', () => {
  beforeEach(() => vi.mocked(fetchMediaBlob).mockReset())

  it('fits square, landscape and portrait videos inside the player without letterboxing', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:video')
    URL.revokeObjectURL = vi.fn()
    HTMLDialogElement.prototype.close = vi.fn()
    vi.mocked(fetchMediaBlob).mockResolvedValue(new Blob(['video'], { type: 'video/mp4' }))
    const wrapper = mount(MediaResult, { props: { src: '/api/v1/playground/media/3', kind: 'video', name: 'video' } })
    await flushPromises()
    const video = wrapper.get('video')
    for (const [width, height, expected] of [[960, 960, 440], [1920, 1080, 520], [1080, 1920, 247.5]]) {
      Object.defineProperty(video.element, 'videoWidth', { configurable: true, value: width })
      Object.defineProperty(video.element, 'videoHeight', { configurable: true, value: height })
      await video.trigger('loadedmetadata')
      expect((wrapper.get('figure').element as HTMLElement).style.width).toBe(`${expected}px`)
      expect(wrapper.get('figcaption').text()).toContain(`${width} × ${height}`)
      expect(video.attributes('controls')).toBeDefined()
    }
    wrapper.unmount()
  })

  it('shows session errors, retries media only and releases the downloaded blob', async () => {
    URL.createObjectURL = vi.fn(() => 'blob:result')
    URL.revokeObjectURL = vi.fn()
    HTMLDialogElement.prototype.close = vi.fn()
    vi.mocked(fetchMediaBlob)
      .mockRejectedValueOnce(Object.assign(new Error('Unauthorized'), { status: 401 }))
      .mockResolvedValueOnce(new Blob(['png'], { type: 'image/png' }))
    const wrapper = mount(MediaResult, { props: { src: 'https://files.toapis.cn/result.png', name: 'result-1' } })
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('playground.mediaAuthRequired')
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(fetchMediaBlob).toHaveBeenCalledTimes(2)
    expect(wrapper.get('a[download]').attributes()).toMatchObject({ href: 'blob:result', download: 'result-1.png' })
    const image = wrapper.get('button img')
    for (const [width, height, expected] of [[1024, 1024, 440], [1600, 900, 520], [900, 1600, 247.5], [100, 2000, 200]]) {
      Object.defineProperty(image.element, 'naturalWidth', { configurable: true, value: width })
      Object.defineProperty(image.element, 'naturalHeight', { configurable: true, value: height })
      await image.trigger('load')
      expect((wrapper.get('figure').element as HTMLElement).style.width).toBe(`${expected}px`)
      expect(wrapper.get('figcaption').text()).toContain(`${width} × ${height}`)
    }
    const signal = vi.mocked(fetchMediaBlob).mock.calls[1][1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:result')
  })
})
