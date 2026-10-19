import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { reactive, type Component } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import LoginView from '../LoginView.vue'
import RegisterView from '../RegisterView.vue'
import EmailVerifyView from '../EmailVerifyView.vue'
import ForgotPasswordView from '../ForgotPasswordView.vue'
import ResetPasswordView from '../ResetPasswordView.vue'
import LegalDocumentView from '@/views/public/LegalDocumentView.vue'
import LoginAgreementPrompt from '@/components/auth/LoginAgreementPrompt.vue'
import i18n, { loadLocaleMessages } from '@/i18n'
import { WXM_DESKTOP_CLIENT_ID, WXM_DESKTOP_REDIRECT_URI } from '@/utils/desktopAuthorization'

const mocks = vi.hoisted(() => ({
  app: {} as any, auth: {} as any, settings: {} as any,
  login: vi.fn(), register: vi.fn(), logout: vi.fn(), success: vi.fn(), error: vi.fn(),
  sendCode: vi.fn(), forgot: vi.fn(), reset: vi.fn(),
}))
vi.mock('@/stores', () => ({ useAppStore: () => mocks.app, useAuthStore: () => mocks.auth }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks.app }))
vi.mock('@/api/auth', async () => ({
  ...await vi.importActual<typeof import('@/api/auth')>('@/api/auth'),
  getPublicSettings: async () => mocks.settings,
  sendVerifyCode: (...args: unknown[]) => mocks.sendCode(...args),
  forgotPassword: (...args: unknown[]) => mocks.forgot(...args),
  resetPassword: (...args: unknown[]) => mocks.reset(...args),
}))

const query = { desktop: '1', client_id: WXM_DESKTOP_CLIENT_ID, redirect_uri: WXM_DESKTOP_REDIRECT_URI,
  state: 's'.repeat(43), code_challenge: 'c'.repeat(43), code_challenge_method: 'S256' }
const wrappers: VueWrapper[] = []
const oauthStub = { template: '<div data-oauth="true" />' }
const stubs = { Icon: true, TurnstileWidget: true, TotpLoginModal: true, LoginAgreementPrompt: true,
  EmailOAuthButtons: oauthStub, LinuxDoOAuthSection: oauthStub, DingTalkOAuthSection: oauthStub,
  WechatOAuthSection: oauthStub, OidcOAuthSection: oauthStub }

async function render(component: Component, path: string, params: Record<string, string> = query, props: Record<string, unknown> = {}) {
  const router = createRouter({ history: createMemoryHistory(), routes: [...[
    '/login', '/register', '/email-verify', '/forgot-password', '/reset-password', '/dashboard', '/home',
  ].map(path => ({ path, component: { template: '<div />' } })),
  { path: '/legal/:documentId', name: 'LegalDocument', component: { template: '<div />' } }] })
  await router.push({ path, query: params })
  const wrapper = mount(component, { props, global: { plugins: [router, i18n], stubs } })
  wrappers.push(wrapper)
  await flushPromises()
  return { wrapper, router }
}

function expectWxm(wrapper: VueWrapper) {
  expect(wrapper.find('[data-auth-brand="weimozhixue"]').exists()).toBe(true)
  expect(wrapper.text()).toContain('微末智学')
  expect(wrapper.text()).not.toMatch(/WindHub|风合智联|Sub2API|Fenghe Zhilian/i)
  expect(wrapper.find('img[alt="Logo"]').attributes('src')).toBe('/wxm-auth.svg')
}

beforeAll(async () => {
  await loadLocaleMessages('zh')
  i18n.global.locale.value = 'zh'
})
beforeEach(() => {
  vi.clearAllMocks()
  sessionStorage.clear()
  localStorage.clear()
  mocks.settings = {
    site_name: '风合智联', site_subtitle: 'WindHub 多模态大模型 API 聚合平台', site_logo: '/logo.svg',
    registration_enabled: true, email_verify_enabled: false, promo_code_enabled: false, invitation_code_enabled: false,
    turnstile_enabled: false, backend_mode_enabled: false, password_reset_enabled: true, login_agreement_enabled: false,
    linuxdo_oauth_enabled: true, dingtalk_oauth_enabled: true, github_oauth_enabled: true, google_oauth_enabled: true,
    wechat_oauth_enabled: true, oidc_oauth_enabled: true, registration_email_suffix_whitelist: [],
  }
  mocks.app = reactive({ siteName: mocks.settings.site_name, siteLogo: mocks.settings.site_logo,
    cachedPublicSettings: mocks.settings, publicSettingsLoaded: true, fetchPublicSettings: async () => mocks.settings,
    showSuccess: mocks.success, showError: mocks.error, showWarning: vi.fn() })
  mocks.auth = reactive({ isAuthenticated: false, user: { email: 'fixture@example.test', username: 'Fixture' },
    pendingAuthSession: null, login: mocks.login, register: mocks.register, logout: mocks.logout })
  mocks.login.mockImplementation(async () => { mocks.auth.isAuthenticated = true; return {} })
  mocks.register.mockResolvedValue({})
  mocks.sendCode.mockResolvedValue({ countdown: 60 })
  mocks.forgot.mockResolvedValue({})
  mocks.reset.mockResolvedValue({})
})
afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount() })

describe('embedded WXM account pages use the existing authentication flow', () => {
  it('brands login and consent, hides third-party buttons, and preserves the forgot-password transaction', async () => {
    const { wrapper, router } = await render(LoginView, '/login')
    expectWxm(wrapper)
    expect(wrapper.find('[data-oauth]').exists()).toBe(false)
    const forgot = wrapper.find('a[href^="/forgot-password"]')
    expect(new URL('https://fixture.test' + forgot.attributes('href')).searchParams.get('state')).toBe(query.state)
    await wrapper.get('#email').setValue('fixture@example.test')
    await wrapper.get('#password').setValue('Fixture-password-123')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.login).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('确认使用此账号登录')
    expectWxm(wrapper)
    expect(router.currentRoute.value.query).toEqual(query)
  })

  it('keeps the ordinary website brand and third-party providers unchanged', async () => {
    for (const component of [LoginView, RegisterView]) {
      const { wrapper } = await render(component, component === LoginView ? '/login' : '/register', {})
      expect(wrapper.text()).toContain('风合智联')
      expect(wrapper.text()).toContain('WindHub')
      expect(wrapper.find('[data-oauth]').exists()).toBe(true)
      expect(wrapper.find('[data-auth-brand]').exists()).toBe(false)
    }
  })

  it('registers through the existing store and returns the same authorization query', async () => {
    const { wrapper, router } = await render(RegisterView, '/register')
    expectWxm(wrapper)
    expect(wrapper.find('[data-oauth]').exists()).toBe(false)
    await wrapper.get('#email').setValue('fixture@example.test')
    await wrapper.get('#password').setValue('Fixture-password-123')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.register).toHaveBeenCalledOnce()
    expect(router.currentRoute.value.path).toBe('/login')
    expect(router.currentRoute.value.query).toEqual(query)
    expect(mocks.success.mock.calls.at(-1)?.[0]).toContain('微末智学')
  })

  it('preserves branding and the transaction through registration email verification', async () => {
    mocks.settings.email_verify_enabled = true
    const { wrapper, router } = await render(RegisterView, '/register')
    await wrapper.get('#email').setValue('fixture@example.test')
    await wrapper.get('#password').setValue('Fixture-password-123')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.register).not.toHaveBeenCalled()
    expect(router.currentRoute.value.path).toBe('/email-verify')
    expect(router.currentRoute.value.query).toEqual(query)
    const verification = await render(EmailVerifyView, '/email-verify')
    expectWxm(verification.wrapper)
    expect(mocks.sendCode).toHaveBeenCalledOnce()
    await verification.wrapper.get('#code').setValue('123456')
    await verification.wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.register).toHaveBeenCalledOnce()
    expect(verification.router.currentRoute.value.path).toBe('/login')
    expect(verification.router.currentRoute.value.query).toEqual(query)
    expect(mocks.success.mock.calls.at(-1)?.[0]).toContain('微末智学')
  })

  it('keeps reset navigation on the same transaction without copying email or reset tokens into login', async () => {
    for (const [component, path, params] of [
      [ForgotPasswordView, '/forgot-password', query],
      [ResetPasswordView, '/reset-password', { ...query, email: 'fixture@example.test', token: 'fixture-reset' }],
    ] as const) {
      const { wrapper } = await render(component, path, params)
      expectWxm(wrapper)
      const login = new URL('https://fixture.test' + wrapper.get('a[href^="/login"]').attributes('href'))
      expect(Object.fromEntries(login.searchParams)).toEqual(query)
    }
    expect(mocks.forgot).not.toHaveBeenCalled()
    expect(mocks.reset).not.toHaveBeenCalled()
  })

  it('preserves the transaction after mocked reset and reset-email confirmations', async () => {
    const reset = await render(ResetPasswordView, '/reset-password',
      { ...query, email: 'fixture@example.test', token: 'fixture-reset' })
    await reset.wrapper.get('#password').setValue('Fixture-password-123')
    await reset.wrapper.get('#confirmPassword').setValue('Fixture-password-123')
    await reset.wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.reset).toHaveBeenCalledOnce()
    expect(reset.wrapper.find('form').exists()).toBe(false)
    const forgot = await render(ForgotPasswordView, '/forgot-password')
    await forgot.wrapper.get('#email').setValue('fixture@example.test')
    await forgot.wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.forgot).toHaveBeenCalledOnce()
    for (const wrapper of [reset.wrapper, forgot.wrapper]) {
      const links = wrapper.findAll('a[href^="/login"]')
      expect(links.length).toBe(2)
      for (const link of links) {
        expect(Object.fromEntries(new URL('https://fixture.test' + link.attributes('href')).searchParams)).toEqual(query)
      }
    }
  })

  it('keeps authentication errors meaningful without exposing another product brand', async () => {
    mocks.login.mockRejectedValueOnce({ message: 'WindHub 连接暂不可用，请重试。' })
    const { wrapper } = await render(LoginView, '/login')
    await wrapper.get('#email').setValue('fixture@example.test')
    await wrapper.get('#password').setValue('Fixture-password-123')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(mocks.error).toHaveBeenLastCalledWith('微末智学 连接暂不可用，请重试。')
    expectWxm(wrapper)
  })

  it('keeps legal navigation in the account window and preserves the published legal text', async () => {
    const doc = { id: 'privacy', title: '隐私政策', content_md: 'WindHub 原协议正文，保持发布内容。' }
    mocks.settings.login_agreement_documents = [doc]
    for (const params of [query, {}]) {
      const { wrapper } = await render(LoginAgreementPrompt, '/login', params,
        { accepted: false, documents: [doc], mode: 'checkbox', visible: true })
      const link = wrapper.get('a[href^="/legal/privacy"]')
      const url = new URL('https://fixture.test' + link.attributes('href'))
      expect(Object.fromEntries(url.searchParams)).toEqual(params)
      expect(link.attributes('target')).toBe(params === query ? undefined : '_blank')
    }
    const { wrapper } = await render(LegalDocumentView, '/legal/privacy')
    expect(wrapper.get('header').text()).toContain('微末智学')
    expect(wrapper.get('header img').attributes('src')).toBe('/wxm-auth.svg')
    expect(wrapper.get('article').text()).toContain(doc.content_md)
    const login = new URL('https://fixture.test' + wrapper.get('a[href^="/login"]').attributes('href'))
    expect(Object.fromEntries(login.searchParams)).toEqual(query)
  })

  it('renders auxiliary navigation safely for an invalid authorization request', async () => {
    for (const [component, path] of [
      [ForgotPasswordView, '/forgot-password'], [ResetPasswordView, '/reset-password'], [LegalDocumentView, '/legal/privacy'],
    ] as const) {
      const { wrapper } = await render(component, path, { ...query, state: 'invalid' })
      expect(wrapper.text()).toContain('风合智联')
      expect(wrapper.get('a[href^="/login"]').attributes('href')).toBe('/login')
      expect(wrapper.find('[data-auth-brand]').exists()).toBe(false)
    }
  })
})
