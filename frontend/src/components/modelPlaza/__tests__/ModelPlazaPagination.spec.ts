import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelPlazaContent from '../ModelPlazaContent.vue'
import PlazaFilterBar from '../PlazaFilterBar.vue'
import PlazaGroupSection from '../PlazaGroupSection.vue'
import ModelGalleryView from '@/views/ModelGalleryView.vue'
import Pagination from '@/components/common/Pagination.vue'
import { getModelPlaza, type ModelPlazaGroup, type ModelPlazaResponse, type PlazaModel } from '@/api/modelPlaza'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<slot />' } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAuthenticated: true }) }))
vi.mock('@/api/modelPlaza', () => ({ getModelPlaza: vi.fn() }))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

const group = (id: number, prefix: string, count: number): ModelPlazaGroup => ({
  id, name: `Group ${id}`, description: '', platform: 'openai', subscription_type: 'standard',
  rate_multiplier: id, user_rate_multiplier: id / 2, peak_rate_enabled: false,
  peak_start: '', peak_end: '', peak_rate_multiplier: 1, is_exclusive: false,
  models: Array.from({ length: count }, (_, i) => ({
    name: `${prefix}-${String(i).padStart(2, '0')}`, platform: 'openai', official_pricing: null,
    pricing: { billing_mode: 'image', per_request_price: i, intervals: Array.from({ length: 9 }, (_, tier) => ({ tier_label: `tier-${tier}`, per_request_price: tier })) }
  } as PlazaModel)).reverse()
})

const global = { stubs: { RouterLink: { template: '<a><slot /></a>' }, PlazaGroupSection: true } }
beforeEach(() => { HTMLElement.prototype.scrollIntoView = vi.fn() })

describe('model catalog pagination', () => {
  it('slices across pricing groups without losing tiers, rates, order or source data and resets on filtering', async () => {
    const response: ModelPlazaResponse = { description: '', groups: [group(1, 'gpt-image', 4), group(2, 'qwen-image', 9)] }
    const source = JSON.stringify(response)
    const wrapper = mount(ModelPlazaContent, { props: { response, loading: false }, global })
    const names = () => wrapper.findAllComponents(PlazaGroupSection).flatMap(section => section.props('group').models.map(model => model.name))
    expect(names()).toEqual(['gpt-image-00', 'gpt-image-01', 'gpt-image-02', 'gpt-image-03', 'qwen-image-00', 'qwen-image-01'])
    const visited = [...names()]
    for (const page of [2, 3]) {
      wrapper.getComponent(Pagination).vm.$emit('update:page', page)
      await flushPromises()
      visited.push(...names())
    }
    expect(visited).toHaveLength(13)
    expect(new Set(visited).size).toBe(13)
    expect(wrapper.getComponent(PlazaGroupSection).props('group').user_rate_multiplier).toBe(1)
    expect(wrapper.getComponent(PlazaGroupSection).props('group').models[0].pricing?.intervals).toHaveLength(9)
    expect(HTMLElement.prototype.scrollIntoView).toHaveBeenCalledTimes(2)
    wrapper.getComponent(PlazaFilterBar).vm.$emit('update:search', 'qwen')
    await flushPromises()
    expect(wrapper.getComponent(Pagination).props('page')).toBe(1)
    expect(names()).toHaveLength(6)
    wrapper.getComponent(PlazaFilterBar).vm.$emit('update:search', 'no-such-model')
    await flushPromises()
    expect(wrapper.findComponent(Pagination).exists()).toBe(false)
    expect(names()).toHaveLength(0)
    expect(JSON.stringify(response)).toBe(source)
    await wrapper.setProps({ response: { description: '', groups: [group(1, 'gpt-image', 0)] }, initialSearch: '' })
    expect(wrapper.text()).toContain('modelPlaza.empty')
    wrapper.unmount()
  })

  it('paginates gallery models without duplication and returns to page one when the category changes', async () => {
    vi.mocked(getModelPlaza).mockResolvedValue({ description: '', groups: [group(1, 'gpt-image', 25)] })
    const wrapper = mount(ModelGalleryView, { global })
    await flushPromises()
    const names = () => wrapper.findAll('.gallery-name').map(item => item.text())
    const visited = [...names()]
    expect(visited).toHaveLength(12)
    for (const page of [2, 3]) {
      wrapper.getComponent(Pagination).vm.$emit('update:page', page)
      await flushPromises()
      visited.push(...names())
    }
    expect(visited).toHaveLength(25)
    expect(new Set(visited).size).toBe(25)
    await wrapper.findAll('.filter-chip').find(item => item.text().includes('modelGallery.kindImage'))!.trigger('click')
    expect(wrapper.getComponent(Pagination).props('page')).toBe(1)
    expect(names()).toHaveLength(12)
    await wrapper.findAll('.filter-chip').find(item => item.text().includes('modelGallery.kindVideo'))!.trigger('click')
    expect(names()).toHaveLength(0)
    expect(wrapper.findComponent(Pagination).exists()).toBe(false)
    wrapper.unmount()
  })
})
