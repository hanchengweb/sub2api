import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import PlazaFilterBar from '../PlazaFilterBar.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
describe('pricing filters', () => {
  it('keeps numeric group and zero-rate values and disables incompatible facets', async () => {
    const wrapper = mount(PlazaFilterBar, { props: {
      platforms: ['openai', 'anthropic'],
      groups: [{id: 1, name: 'A', platform: 'openai', rate: 0}, {id: 2, name: 'B', platform: 'anthropic', rate: 1}],
      rates: [0, 1], platform: 'openai', groupId: 'all', rate: 'all', search: ''
    } })
    const selects = wrapper.findAll('select')
    expect(selects[1].get('option[value="2"]').attributes('disabled')).toBeDefined()
    await selects[1].setValue('1')
    await selects[2].setValue('0')
    expect(wrapper.emitted('update:groupId')?.[0]).toEqual([1])
    expect(wrapper.emitted('update:rate')?.[0]).toEqual([0])
    await wrapper.get('input').setValue('qwen')
    expect(wrapper.emitted('update:search')?.[0]).toEqual(['qwen'])
  })
})
