import { beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import AgencyView from '../AgencyView.vue'
import dashboard from '@/i18n/locales/zh/dashboard'
import common from '@/i18n/locales/zh/common'

const copy = vi.hoisted(() => vi.fn())
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: copy }) }))

const api = vi.hoisted(() => ({ submit: vi.fn(), listMine: vi.fn() }))
vi.mock('@/api/agency', () => ({
  submitAgencyApplication: api.submit,
  listMyAgencyApplications: api.listMine
}))

beforeEach(() => {
  copy.mockReset().mockResolvedValue(false)
  api.submit.mockReset().mockResolvedValue({ id: 1 })
  api.listMine.mockReset().mockResolvedValue([])
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value() { this.setAttribute('open', '') }
  })
})

function render() {
  return mount(AgencyView, {
    global: {
      plugins: [createPinia(), createI18n({
        legacy: false,
        locale: 'zh',
        messages: { zh: { ...common, ...dashboard } },
        // Vitest uses runtime-only i18n; these page messages have no interpolation.
        messageCompiler: (message) => () => String(message)
      })],
      stubs: { AppLayout: { template: '<main><slot /></main>' } }
    }
  })
}

it('prepares the selected partnership and retains readable text when copying fails', async () => {
  const wrapper = render()
  await wrapper.get('input[value="integration"]').setValue()
  await wrapper.get('#agency-name').setValue('  测试伙伴  ')
  await wrapper.get('#agency-email').setValue('partner@example.invalid')
  await wrapper.get('#agency-scenario').setValue('  企业知识库集成  ')
  // 「生成合作说明」现在是副按钮——主按钮改成了真提交
  await wrapper.get('button.agency-secondary').trigger('click')
  const dialog = wrapper.get('dialog')
  expect(dialog.attributes()).toHaveProperty('open')
  const text = (wrapper.get('.agency-brief').element as HTMLTextAreaElement).value
  expect(text).toContain('选择合作方向：技术集成')
  expect(text).toContain('姓名：测试伙伴\n')
  expect(text).toContain('合作场景：企业知识库集成')
  expect(text).not.toContain('公司或团队：')
  await dialog.get('.agency-primary').trigger('click')
  await flushPromises()
  expect(copy).toHaveBeenCalledWith(text)
  expect(dialog.get('[role="status"]').text()).toContain('手动复制')
  expect((wrapper.get('.agency-brief').element as HTMLTextAreaElement).value).toBe(text)
  wrapper.unmount()
})

it('rejects whitespace-only input and does not present an application as submitted', async () => {
  const wrapper = render()
  await wrapper.get('#agency-name').setValue('   ')
  await wrapper.get('#agency-email').setValue('partner@example.invalid')
  await wrapper.get('#agency-scenario').setValue('   ')
  await wrapper.get('form').trigger('submit')
  expect(wrapper.get('dialog').attributes()).not.toHaveProperty('open')
  expect((wrapper.get('#agency-name').element as HTMLInputElement).validity.customError).toBe(true)
  expect(copy).not.toHaveBeenCalled()
  // 校验没过就不能发请求——否则库里会堆一批空白申请
  expect(api.submit).not.toHaveBeenCalled()
  wrapper.unmount()
})

it('提交表单会真的把申请发到平台，并刷新我的申请', async () => {
  const wrapper = render()
  await wrapper.get('input[value="delivery"]').setValue()
  await wrapper.get('#agency-name').setValue('  测试伙伴  ')
  await wrapper.get('#agency-email').setValue('partner@example.invalid')
  await wrapper.get('#agency-company').setValue(' 某公司 ')
  await wrapper.get('#agency-scenario').setValue('  交付合作  ')
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  // 字段两端空白要去掉再发：留着会原样存进库，运营看到的就是带空格的名字
  expect(api.submit).toHaveBeenCalledWith({
    direction: 'delivery',
    contact_name: '测试伙伴',
    email: 'partner@example.invalid',
    company: '某公司',
    scenario: '交付合作'
  })
  // 提交后要刷新列表：没有回执用户只会反复再点一次
  expect(api.listMine).toHaveBeenCalledTimes(2)
  wrapper.unmount()
})

it('待处理达上限时提示「已有申请在处理中」，而不是「填写有误」', async () => {
  api.submit.mockRejectedValue({ response: { data: { error: { code: 'TOO_MANY_PENDING' } } } })
  const wrapper = render()
  await wrapper.get('#agency-name').setValue('测试伙伴')
  await wrapper.get('#agency-email').setValue('partner@example.invalid')
  await wrapper.get('#agency-scenario').setValue('渠道合作')
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  const err = wrapper.get('[role="alert"]').text()
  expect(err).toContain('处理中')
  expect(err).not.toContain('失败')
  wrapper.unmount()
})
