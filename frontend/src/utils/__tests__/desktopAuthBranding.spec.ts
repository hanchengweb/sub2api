import { describe, expect, it } from 'vitest'
import { desktopAuthBrand, desktopAuthBrandFromURL, desktopAuthLoginURL, desktopAuthMessage, desktopAuthNavigationQuery, WXM_AUTH_BRAND } from '../desktopAuthBranding'
import { WXM_DESKTOP_CLIENT_ID, WXM_DESKTOP_REDIRECT_URI, FENGXING_DESKTOP_CLIENT_ID, FENGXING_DESKTOP_REDIRECT_URI } from '../desktopAuthorization'
import { resolveRouteDocumentTitle } from '@/router/title'

const query = { desktop: '1', client_id: WXM_DESKTOP_CLIENT_ID, redirect_uri: WXM_DESKTOP_REDIRECT_URI,
  state: 's'.repeat(43), code_challenge: 'c'.repeat(43), code_challenge_method: 'S256' }
const url = (path = '/login', extra = '') => 'https://windhub.online' + path + '?' + new URLSearchParams(query) + extra

describe('Weimo embedded account brand boundary', () => {
  it.each(['/login', '/register', '/email-verify', '/forgot-password', '/reset-password', '/legal/privacy'])(
    'uses the fixed brand on the validated account surface %s', path => {
      expect(desktopAuthBrand({ path, query })).toBe(WXM_AUTH_BRAND)
      expect(desktopAuthBrandFromURL(url(path))).toBe(WXM_AUTH_BRAND)
    },
  )

  it('leaves ordinary pages, unrelated clients and untrusted requests on the website brand', () => {
    for (const route of [
      { path: '/login', query: {} }, { path: '/dashboard', query },
      { path: '/login', query: { ...query, client_id: FENGXING_DESKTOP_CLIENT_ID, redirect_uri: FENGXING_DESKTOP_REDIRECT_URI } },
      { path: '/login', query: { ...query, redirect_uri: 'https://evil.test/callback' } },
      { path: '/login', query: { ...query, code_challenge_method: 'plain' } },
      { path: '/login', query: { ...query, state: 'short' } },
      { path: '/login', query: { ...query, client_id: [WXM_DESKTOP_CLIENT_ID, WXM_DESKTOP_CLIENT_ID] } },
    ]) expect(desktopAuthBrand(route)).toBeNull()
    expect(desktopAuthBrandFromURL(url('/login', '&client_id=' + WXM_DESKTOP_CLIENT_ID))).toBeNull()
  })

  it('keeps only validated authorization parameters on an expired-session return', () => {
    const login = new URL(desktopAuthLoginURL(url('/reset-password', '&email=fixture%40example.test&token=reset-fixture&next=https://evil.test')), 'https://windhub.online')
    expect(login.pathname).toBe('/login')
    expect(Object.fromEntries(login.searchParams)).toEqual(query)
    expect(desktopAuthLoginURL('https://windhub.online/register')).toBe('/login')
    expect(desktopAuthNavigationQuery({ path: '/forgot-password', query: { ...query, state: 'invalid' } })).toEqual({})
    expect(desktopAuthNavigationQuery({ path: '/legal/privacy', query: { ...query, token: 'fixture-token' } })).toEqual(query)
  })

  it('brands titles and errors only within the matching account flow', () => {
    const route = { path: '/login', query, name: 'Login', params: {}, meta: { title: '登录' } }
    expect(resolveRouteDocumentTitle(route, '风合智联')).toBe('登录 - 微末智学')
    expect(resolveRouteDocumentTitle({ ...route, query: {} }, '风合智联')).toBe('登录 - 风合智联')
    expect(desktopAuthMessage(route, 'WindHub 账号暂不可用')).toBe('微末智学 账号暂不可用')
    expect(desktopAuthMessage({ path: '/login' }, 'WindHub 账号暂不可用')).toBe('WindHub 账号暂不可用')
  })
})
