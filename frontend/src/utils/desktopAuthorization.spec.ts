import { describe, expect, it } from 'vitest'
import {
  FENGXING_DESKTOP_CLIENT_ID,
  WXM_DESKTOP_CLIENT_ID,
  WXM_DESKTOP_REDIRECT_URI,
  desktopAuthorizationQuery,
  postRegistrationRoute,
  FENGXING_DESKTOP_REDIRECT_URI,
  buildDesktopAuthorizationCallback,
  buildDesktopAuthorizationErrorCallback,
  parseDesktopAuthorizationRequest,
} from './desktopAuthorization'

const query = {
  desktop: '1',
  client_id: FENGXING_DESKTOP_CLIENT_ID,
  redirect_uri: FENGXING_DESKTOP_REDIRECT_URI,
  state: 'state-1',
  code_challenge: 'challenge-1',
  code_challenge_method: 'S256',
}

describe('Fengxing desktop authorization', () => {
  it('parses only the Fengxing client and callback URI', () => {
    expect(parseDesktopAuthorizationRequest(query)).toEqual({
      client_id: FENGXING_DESKTOP_CLIENT_ID,
      redirect_uri: FENGXING_DESKTOP_REDIRECT_URI,
      state: 'state-1',
      code_challenge: 'challenge-1',
      code_challenge_method: 'S256',
    })
    expect(parseDesktopAuthorizationRequest({ redirect: '/dashboard' })).toBeNull()
  })

  it('builds a code-and-state callback without access tokens', () => {
    const request = parseDesktopAuthorizationRequest(query)!
    expect(buildDesktopAuthorizationCallback(request, 'code/1')).toBe(
      'fengxingzhonghe://auth/callback?code=code%2F1&state=state-1',
    )
  })

  it('builds an explicit denial callback for the desktop client', () => {
    const request = parseDesktopAuthorizationRequest(query)!
    expect(buildDesktopAuthorizationErrorCallback(request, 'access_denied', '用户拒绝授权')).toBe(
      'fengxingzhonghe://auth/callback?error=access_denied&state=state-1&error_description=%E7%94%A8%E6%88%B7%E6%8B%92%E7%BB%9D%E6%8E%88%E6%9D%83',
    )
  })

  it('rejects incomplete or foreign desktop authorization requests', () => {
    expect(() => parseDesktopAuthorizationRequest({ ...query, client_id: 'other.desktop' })).toThrow(
      '授权请求无效',
    )
    expect(() => parseDesktopAuthorizationRequest({ ...query, redirect_uri: 'https://evil.example/callback' })).toThrow(
      '授权请求无效',
    )
  })
})

const wxmQuery = { ...query, client_id: WXM_DESKTOP_CLIENT_ID,
  redirect_uri: WXM_DESKTOP_REDIRECT_URI, state: 's'.repeat(43), code_challenge: 'c'.repeat(43) }

describe('Weimo desktop authorization', () => {
  it('accepts the installed callback and exact IPv4 preview loopback', () => {
    expect(parseDesktopAuthorizationRequest(wxmQuery)?.client_id).toBe(WXM_DESKTOP_CLIENT_ID)
    expect(parseDesktopAuthorizationRequest({ ...wxmQuery, redirect_uri: 'http://127.0.0.1:43186/oauth/callback' })).toBeTruthy()
  })
  it.each([
    'http://localhost:43186/oauth/callback', 'http://127.0.0.1:80/oauth/callback',
    'http://127.0.0.1:65536/oauth/callback', 'http://127.0.0.1:43186/oauth/callback?next=evil',
    'http://127.0.0.1:43186/oauth/callback#fragment', 'http://127.0.0.1:43186/other',
    'http://127.0.0.1.evil.test:43186/oauth/callback', 'http://user@127.0.0.1:43186/oauth/callback',
    FENGXING_DESKTOP_REDIRECT_URI, 'weimozhixue://oauth/callback/extra',
  ])('rejects unregistered callback %s', redirect_uri => {
    expect(() => parseDesktopAuthorizationRequest({ ...wxmQuery, redirect_uri })).toThrow()
  })
  it('rejects missing or weak PKCE and foreign client callback combinations', () => {
    for (const change of [{ state: 'short' }, { code_challenge: '' }, { code_challenge_method: 'plain' }, { client_id: FENGXING_DESKTOP_CLIENT_ID }]) {
      expect(() => parseDesktopAuthorizationRequest({ ...wxmQuery, ...change })).toThrow()
    }
  })
  it('preserves only validated fields through signup, verification and login', () => {
    const preserved = desktopAuthorizationQuery({ ...wxmQuery, redirect: 'https://evil.test', password: 'must-not-copy' })
    expect(preserved).toEqual(wxmQuery)
    expect(postRegistrationRoute(preserved)).toEqual({ path: '/login', query: wxmQuery })
    expect(postRegistrationRoute({})).toBe('/dashboard')
    expect(desktopAuthorizationQuery({ promo: 'x' })).toEqual({})
  })
})
