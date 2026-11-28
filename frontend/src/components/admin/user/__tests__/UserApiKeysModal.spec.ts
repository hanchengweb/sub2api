import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import UserApiKeysModal from '../UserApiKeysModal.vue'

const api = vi.hoisted(() => ({ getUserApiKeys: vi.fn(), getAll: vi.fn() }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: { getUserApiKeys: api.getUserApiKeys },
    groups: { getAll: api.getAll },
    apiKeys: { updateApiKeyGroup: vi.fn() }
  }
}))

const user = { id: 7, email: 'org@service.invalid', username: 'org' } as never
const personalKey = 'sk-personal0123456789abcdefghijklmnQRST'

async function render(keys: string[]) {
  api.getUserApiKeys.mockResolvedValue({
    items: keys.map((key, index) => ({
      id: index + 1,
      key,
      name: `key-${index}`,
      status: 'active',
      created_at: '2026-01-01T00:00:00Z'
    }))
  })
  const wrapper = mount(UserApiKeysModal, {
    props: { show: false, user },
    global: {
      plugins: [createPinia(), createI18n({ legacy: false, locale: 'zh', messages: { zh: {} }, missingWarn: false, fallbackWarn: false })],
      stubs: { BaseDialog: { template: '<div><slot /></div>' }, GroupBadge: true, GroupOptionItem: true }
    }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

describe('UserApiKeysModal 的 key 显示', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    api.getUserApiKeys.mockReset()
    api.getAll.mockReset().mockResolvedValue([])
  })

  // 组织服务账号的 key 接口只返回脱敏值，页面要原样显示，不能再切一遍拼出重复内容
  it('接口返回的脱敏值原样显示', async () => {
    const wrapper = await render(['orgkey...WXYZ'])

    expect(wrapper.get('p.font-mono').text()).toBe('orgkey...WXYZ')
    wrapper.unmount()
  })

  it('个人账号的明文只显示首尾几位', async () => {
    const wrapper = await render([personalKey])

    expect(wrapper.get('p.font-mono').text()).toBe('sk-per...QRST')
    expect(wrapper.html()).not.toContain(personalKey)
    wrapper.unmount()
  })
})
