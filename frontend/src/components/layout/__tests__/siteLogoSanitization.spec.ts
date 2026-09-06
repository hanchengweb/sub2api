import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const dir = dirname(fileURLToPath(import.meta.url))
const sidebarSource = readFileSync(resolve(dir, '../AppSidebar.vue'), 'utf8')
const authLayoutSource = readFileSync(resolve(dir, '../AuthLayout.vue'), 'utf8')
const plazaNavSource = readFileSync(resolve(dir, '../../modelPlaza/PlazaNavBar.vue'), 'utf8')
const productShellSource = readFileSync(resolve(dir, '../../product/ProductShell.vue'), 'utf8')
const homeViewSource = readFileSync(resolve(dir, '../../../views/HomeView.vue'), 'utf8')
const keyUsageViewSource = readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')
const legalDocumentSource = readFileSync(resolve(dir, '../../../views/public/LegalDocumentView.vue'), 'utf8')
const appEntrySource = readFileSync(resolve(dir, '../../../../index.html'), 'utf8')

describe('site_logo sanitization', () => {
  it('AppSidebar imports sanitizeUrl and applies it to siteLogo', () => {
    expect(sidebarSource).toContain("import { sanitizeUrl } from '@/utils/url'")
    expect(sidebarSource).toContain('sanitizeUrl(appStore.siteLogo')
  })

  it('HomeView applies sanitizeUrl to siteLogo', () => {
    expect(homeViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('KeyUsageView applies sanitizeUrl to siteLogo', () => {
    expect(keyUsageViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('all three pass allowRelative and allowDataUrl options', () => {
    for (const src of [sidebarSource, homeViewSource, keyUsageViewSource]) {
      expect(src).toContain('allowRelative: true')
      expect(src).toContain('allowDataUrl: true')
    }
  })

  it('uses the stable ICO fallback across application surfaces', () => {
    for (const src of [
      sidebarSource,
      authLayoutSource,
      plazaNavSource,
      productShellSource,
      homeViewSource,
      keyUsageViewSource,
      legalDocumentSource,
    ]) {
      expect(src).toContain("|| '/logo.ico'")
      expect(src).not.toContain("|| '/logo.svg'")
    }

    expect(appEntrySource).toContain('type="image/x-icon" href="/logo.ico"')
    expect(appEntrySource).not.toContain('href="/logo.svg"')
  })
})
