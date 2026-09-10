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
    const signal = vi.mocked(fetchMediaBlob).mock.calls[1][1]!
    wrapper.unmount()
    expect(signal.aborted).toBe(true)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:result')
  })
})
