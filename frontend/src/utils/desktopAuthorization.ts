export const FENGXING_DESKTOP_CLIENT_ID = 'com.fengxingzhonghe.desktop'
export const FENGXING_DESKTOP_REDIRECT_URI = 'fengxingzhonghe://auth/callback'
export const WXM_DESKTOP_CLIENT_ID = 'com.weimozhixue.desktop'
export const WXM_DESKTOP_REDIRECT_URI = 'weimozhixue://oauth/callback'
export const REMOTE_CLIENT_ID = 'wxm-mobile-remote'
export const REMOTE_REDIRECT_URI = 'https://app.wemoreai.com/remote/v1/personal/callback'

export interface DesktopAuthorizationRequest {
  client_id: string
  redirect_uri: string
  state: string
  code_challenge: string
  code_challenge_method: 'S256'
}

function queryString(query: Record<string, unknown>, key: string): string {
  const value = query[key]
  return typeof value === 'string' ? value.trim() : ''
}

function isWxmRedirect(uri: string): boolean {
  if (uri === WXM_DESKTOP_REDIRECT_URI) return true
  const match = /^http:\/\/127\.0\.0\.1:([1-9][0-9]{3,4})\/oauth\/callback$/.exec(uri)
  return Boolean(match && Number(match[1]) >= 1024 && Number(match[1]) <= 65535)
}

export function hasDesktopAuthorizationQuery(query: Record<string, unknown>): boolean {
  return ['desktop', 'client_id', 'redirect_uri', 'state', 'code_challenge', 'code_challenge_method']
    .some((key) => Object.prototype.hasOwnProperty.call(query, key))
}

export function parseDesktopAuthorizationRequest(
  query: Record<string, unknown>,
): DesktopAuthorizationRequest | null {
  if (!hasDesktopAuthorizationQuery(query)) return null

  const request = {
    client_id: queryString(query, 'client_id'),
    redirect_uri: queryString(query, 'redirect_uri'),
    state: queryString(query, 'state'),
    code_challenge: queryString(query, 'code_challenge'),
    code_challenge_method: queryString(query, 'code_challenge_method'),
  }
  if (
    !((request.client_id === FENGXING_DESKTOP_CLIENT_ID && request.redirect_uri === FENGXING_DESKTOP_REDIRECT_URI) ||
      (request.client_id === WXM_DESKTOP_CLIENT_ID && isWxmRedirect(request.redirect_uri) &&
        /^[A-Za-z0-9_-]{16,128}$/.test(request.state) && /^[A-Za-z0-9_-]{43}$/.test(request.code_challenge)) ||
      (request.client_id === REMOTE_CLIENT_ID && request.redirect_uri === REMOTE_REDIRECT_URI &&
        /^[A-Za-z0-9_-]{43}$/.test(request.state) && /^[A-Za-z0-9_-]{43}$/.test(request.code_challenge))) ||
    !request.state ||
    !request.code_challenge ||
    request.code_challenge_method !== 'S256'
  ) {
    throw new Error('授权请求无效，请返回原应用重新发起授权。')
  }

  return request as DesktopAuthorizationRequest
}

// Carry only validated authorization fields through registration and email verification.
export function desktopAuthorizationQuery(query: Record<string, unknown>): Record<string, string> {
  const request = parseDesktopAuthorizationRequest(query)
  return request ? { desktop: '1', ...request } : {}
}

export function postRegistrationRoute(query: Record<string, unknown>, fallback = '/dashboard') {
  const authorization = desktopAuthorizationQuery(query)
  return authorization.client_id ? { path: '/login', query: authorization } : fallback
}

export function buildDesktopAuthorizationCallback(
  request: DesktopAuthorizationRequest,
  code: string,
): string {
  const callback = new URL(request.redirect_uri)
  callback.searchParams.set('code', code)
  callback.searchParams.set('state', request.state)
  return callback.toString()
}

export function buildDesktopAuthorizationErrorCallback(
  request: DesktopAuthorizationRequest,
  error: string,
  description?: string,
): string {
  const callback = new URL(request.redirect_uri)
  callback.searchParams.set('error', error)
  callback.searchParams.set('state', request.state)
  if (description) callback.searchParams.set('error_description', description)
  return callback.toString()
}
