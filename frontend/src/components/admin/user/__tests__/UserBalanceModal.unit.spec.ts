import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import UserBalanceModal from '../UserBalanceModal.vue'
import common from '@/i18n/locales/zh/common'
import overview from '@/i18n/locales/zh/admin/overview'

const api = vi.hoisted(() => ({ updateBalance: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { users: { updateBalance: api.updateBalance } } }))

const user = { id: 7, email: 'a@b.com', balance: 156.9 } as never

function render() {
  return mount(UserBalanceModal, {
    props: { show: true, user, operation: 'add' as const },
    global: {
      plugins: [createPinia(), createI18n({
        legacy: false,
        locale: 'zh',
        messages: { zh: { ...common, admin: { ...overview } } },
        // vitest 用的是 runtime-only i18n，没有编译器就会原样回落成 key
        messageCompiler: (message) => () => String(message)
      })],
      stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' } }
    }
  })
}

describe('UserBalanceModal 的计价单位', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    api.updateBalance.mockReset().mockResolvedValue({})
  })

  // 管理员充值没有真实资金流：后端 UpdateUserBalance 是 user.Balance += balance，
  // 全程不乘 BALANCE_RECHARGE_MULTIPLIER。标成 ￥ 会让运营少充 100 倍。
  it('输入框标的是积分，不是人民币符号', () => {
    const wrapper = render()
    const html = wrapper.html()

    expect(html).toContain('积分')
    expect(html).not.toContain('￥ ')
    // 「当前余额」「操作后余额」本来就是积分，三处口径必须一致
    expect(wrapper.text()).toContain('当前余额')
    wrapper.unmount()
  })

  // 提交给接口的必须是积分原值——换算只发生在展示层
  it('提交的是输入的积分原值，不做任何换算', async () => {
    const wrapper = render()
    await wrapper.get('input[type="number"]').setValue(10000)
    await wrapper.get('form').trigger('submit')

    expect(api.updateBalance).toHaveBeenCalledWith(7, 10000, 'add', '')
    wrapper.unmount()
  })

  it('金额为 0 时不显示人民币旁注', () => {
    const wrapper = render()
    expect(wrapper.text()).not.toContain('≈')
    wrapper.unmount()
  })
})
