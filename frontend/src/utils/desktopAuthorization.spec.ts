import { describe, expect, it } from 'vitest'
import {
  FENGXING_DESKTOP_CLIENT_ID,
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
      '桌面授权请求无效',
    )
    expect(() => parseDesktopAuthorizationRequest({ ...query, redirect_uri: 'https://evil.example/callback' })).toThrow(
      '桌面授权请求无效',
    )
  })
})
