import { describe, expect, it } from 'vitest'
import { parseDesktopAuthorizationRequest, buildDesktopAuthorizationCallback, REMOTE_CLIENT_ID, REMOTE_REDIRECT_URI, FENGXING_DESKTOP_CLIENT_ID, FENGXING_DESKTOP_REDIRECT_URI } from '../desktopAuthorization'

describe('remote authorization client boundary', () => {
  const remote = { client_id: REMOTE_CLIENT_ID, redirect_uri: REMOTE_REDIRECT_URI, state: 's'.repeat(43), code_challenge: 'c'.repeat(43), code_challenge_method: 'S256' }
  it('accepts the exact mobile client and keeps transaction state in callback', () => {
    const parsed = parseDesktopAuthorizationRequest(remote)!
    const callback = new URL(buildDesktopAuthorizationCallback(parsed, 'code'))
    expect(callback.origin + callback.pathname).toBe(REMOTE_REDIRECT_URI)
    expect(callback.searchParams.get('state')).toBe(remote.state)
  })
  it('rejects mixed clients, alternate redirects and invalid PKCE inputs', () => {
    for (const delta of [{ client_id: FENGXING_DESKTOP_CLIENT_ID }, { redirect_uri: FENGXING_DESKTOP_REDIRECT_URI }, { redirect_uri: REMOTE_REDIRECT_URI + '/evil' }, { state: 'short' }, { code_challenge_method: 'plain' }]) {
      expect(() => parseDesktopAuthorizationRequest({ ...remote, ...delta })).toThrow()
    }
  })
  it('preserves the existing desktop authorization client', () => {
    expect(parseDesktopAuthorizationRequest({ ...remote, client_id: FENGXING_DESKTOP_CLIENT_ID, redirect_uri: FENGXING_DESKTOP_REDIRECT_URI })?.client_id).toBe(FENGXING_DESKTOP_CLIENT_ID)
    expect(parseDesktopAuthorizationRequest({})).toBeNull()
  })
})
