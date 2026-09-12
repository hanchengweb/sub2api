import { beforeEach, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import AgencyView from '../AgencyView.vue'
import dashboard from '@/i18n/locales/zh/dashboard'
import common from '@/i18n/locales/zh/common'

const copy = vi.hoisted(() => vi.fn())
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: copy }) }))

beforeEach(() => {
  copy.mockReset().mockResolvedValue(false)
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
  await wrapper.get('form').trigger('submit')
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
  wrapper.unmount()
})
