import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import MediaResult from './MediaResult.vue'
import { fetchMediaBlob } from '@/api/playground'

vi.mock('@/api/playground', () => ({ fetchMediaBlob: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('media result lifecycle', () => {
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
