import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import ModelGalleryView from '../ModelGalleryView.vue'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<slot />' } }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/api/modelPlaza', () => ({
  getModelPlaza: async () => ({ groups: [{ rate_multiplier: 1, models: [
    { name: 'gpt-image-2.5-flare', pricing: { billing_mode: 'image', per_request_price: 10 } },
    { name: 'unknown-model', pricing: { billing_mode: 'token', input_price: 0, output_price: 0 } }
  ] }] })
}))

describe('model gallery imagery', () => {
  it('uses vendor assets and keeps the card and destination usable if an image fails', async () => {
    const wrapper = mount(ModelGalleryView, {
      global: { stubs: { RouterLink: { props: ['to'], template: '<a :data-target="JSON.stringify(to)"><slot /></a>' } } }
    })
    await flushPromises()
    expect(wrapper.findAll('.gallery-card')).toHaveLength(2)
    const image = wrapper.get('.gallery-image')
    expect(image.attributes('src')).toBe('/model-gallery/openai.webp')
    // 不能放在 /images/ 下：网关把整个 /images/ 前缀旁路给了 OpenAI 兼容端点
    // （/images/generations、/images/tasks），静态图放那儿线上一律 404。
    // 2026-09-11 实测踩过一次，这条断言是防它再溜回去。
    expect(image.attributes('src')).not.toMatch(/^\/images\//)
    const card = wrapper.findAll('.gallery-card').find(item => item.text().includes('gpt-image'))!
    expect(JSON.parse(card.get('a').attributes('data-target'))).toEqual({
      path: '/playground', query: { model: 'gpt-image-2.5-flare', mode: 'image' }
    })
    await image.trigger('error')
    expect(wrapper.find('.gallery-image').exists()).toBe(false)
    expect(card.get('.gallery-brand').exists()).toBe(true)
    expect(card.text()).toContain('gpt-image-2.5-flare')
    wrapper.unmount()
  })
})
