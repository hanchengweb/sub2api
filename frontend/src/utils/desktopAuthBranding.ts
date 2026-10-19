import { desktopAuthorizationQuery, parseDesktopAuthorizationRequest, WXM_DESKTOP_CLIENT_ID } from './desktopAuthorization'

type AuthRoute = { path?: string; query?: Record<string, unknown> }
const authPaths = new Set(['/login', '/register', '/email-verify', '/forgot-password', '/reset-password'])

// Fixed application metadata, never a renderer-supplied name or logo URL.
export const WXM_AUTH_BRAND = Object.freeze({
  name: '微末智学',
  subtitle: '个人账号',
  logo: '/wxm-auth.svg',
})

export function desktopAuthBrand(route?: AuthRoute) {
  if (!route?.path || (!authPaths.has(route.path) && !route.path.startsWith('/legal/'))) return null
  try {
    return parseDesktopAuthorizationRequest(route.query || {})?.client_id === WXM_DESKTOP_CLIENT_ID
      ? WXM_AUTH_BRAND : null
  } catch {
    // Invalid authorization requests never acquire a trusted application's identity.
    return null
  }
}

function routeFromURL(value: string) {
  const url = new URL(value)
  const query: Record<string, unknown> = {}
  for (const key of url.searchParams.keys()) {
    const values = url.searchParams.getAll(key)
    query[key] = values.length === 1 ? values[0] : values
  }
  return { path: url.pathname, query }
}

export function desktopAuthBrandFromURL(value: string) {
  return desktopAuthBrand(routeFromURL(value))
}

// Auxiliary links must remain usable when an invalid authorization URL is opened.
export function desktopAuthNavigationQuery(route: AuthRoute): Record<string, string> {
  return desktopAuthBrand(route) ? desktopAuthorizationQuery(route.query || {}) : {}
}

export function desktopAuthLoginURL(value: string): string {
  const route = routeFromURL(value)
  return desktopAuthBrand(route)
    ? '/login?' + new URLSearchParams(desktopAuthNavigationQuery(route)).toString() : '/login'
}

export function desktopAuthMessage(route: AuthRoute | undefined, message: string): string {
  return desktopAuthBrand(route) ? message.replace(/WindHub|Fenghe Zhilian|风合智联|Sub2API/gi, WXM_AUTH_BRAND.name) : message
}
