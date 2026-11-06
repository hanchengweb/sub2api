import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { ChannelModelPricing } from '@/api/admin/channels'
import PricingEntryCard from '../PricingEntryCard.vue'
import { apiPricingToForm, formPricingToAPI, yuanHint } from '../types'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const price: ChannelModelPricing = {
  platform: 'openai',
  models: ['gpt-image-2.6-flare-official'],
  billing_mode: 'token',
  input_price: 0.004,
  output_price: 0.024,
  cache_write_price: null,
  cache_read_price: 0.001,
  image_input_price: 0.0064,
  image_cache_read_price: 0.0016,
  image_output_price: 0.024,
  per_request_price: null,
  intervals: [],
}

describe('image token price editing', () => {
  it('reloads and saves every rate through the channel/account-rule conversion without losing the image cache price', () => {
    const form = apiPricingToForm(JSON.parse(JSON.stringify(price)))
    expect(form.image_cache_read_price).toBe(1600)
    expect(yuanHint(form.image_cache_read_price)).toBe('≈¥16.00')
    expect(formPricingToAPI(form, 'openai')).toEqual(price)
  })

  it('keeps a legacy absent rate unset and preserves an explicit free rate', () => {
    const { image_cache_read_price: _unused, ...legacy } = price
    expect(formPricingToAPI(apiPricingToForm(legacy), 'openai').image_cache_read_price).toBeNull()
    const free = { ...price, image_cache_read_price: 0 }
    expect(formPricingToAPI(apiPricingToForm(free), 'openai').image_cache_read_price).toBe(0)
  })

  it('edits or clears only the image cache rate and retains the other prices', async () => {
    const entry = apiPricingToForm(price)
    const wrapper = mount(PricingEntryCard, {
      props: { entry, platform: 'openai' },
      global: { stubs: { Icon: true, Select: true, ModelTagInput: true, IntervalRow: true } },
    })
    const field = wrapper.get('input[aria-label="admin.channels.form.imageCacheReadPrice"]')
    await field.setValue('0')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toEqual({ ...entry, image_cache_read_price: '0' })
    await field.setValue('')
    expect(wrapper.emitted('update')?.at(-1)?.[0]).toEqual({ ...entry, image_cache_read_price: null })
  })
})
