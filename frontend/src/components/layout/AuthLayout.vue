<template>
  <div
    class="relative"
    :class="authBrand ? 'wxm-auth' : 'flex min-h-screen items-center justify-center overflow-hidden p-4'"
    :data-auth-brand="authBrand ? 'weimozhixue' : undefined"
  >
    <template v-if="!authBrand">
    <!-- Background -->
    <div
      class="absolute inset-0 bg-gradient-to-br from-gray-50 via-primary-50/30 to-gray-100 dark:from-dark-950 dark:via-dark-900 dark:to-dark-950"
    ></div>

    <!-- Decorative Elements -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <!-- Gradient Orbs -->
      <div
        class="absolute -right-40 -top-40 h-80 w-80 rounded-full bg-primary-400/20 blur-3xl"
      ></div>
      <div
        class="absolute -bottom-40 -left-40 h-80 w-80 rounded-full bg-primary-500/15 blur-3xl"
      ></div>
      <div
        class="absolute left-1/2 top-1/2 h-96 w-96 -translate-x-1/2 -translate-y-1/2 rounded-full bg-primary-300/10 blur-3xl"
      ></div>

      <!-- Grid Pattern -->
      <div
        class="absolute inset-0 bg-[linear-gradient(rgba(20,184,166,0.03)_1px,transparent_1px),linear-gradient(90deg,rgba(20,184,166,0.03)_1px,transparent_1px)] bg-[size:64px_64px]"
      ></div>
    </div>
    </template>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md" :class="{ 'wxm-auth-content': authBrand }">
      <!-- Logo/Brand -->
      <div v-if="authBrand" class="wxm-auth-brand">
        <img :src="siteLogo" alt="Logo" width="36" height="36" />
        <div>
          <h1>{{ siteName }}</h1>
          <p>{{ siteSubtitle }}</p>
        </div>
      </div>
      <div v-else class="mb-8 text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div
            class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl shadow-lg shadow-primary-500/30"
          >
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <h1 class="text-gradient mb-2 text-3xl font-bold">
            {{ siteName }}
          </h1>
          <p class="text-sm text-gray-500 dark:text-dark-400">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div :class="authBrand ? 'wxm-auth-card' : 'card-glass rounded-2xl p-8 shadow-glass'">
        <slot />
      </div>

      <!-- Footer Links -->
      <div :class="authBrand ? 'wxm-auth-footer' : 'mt-6 text-center text-sm'">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div :class="authBrand ? 'wxm-auth-copyright' : 'mt-8 text-center text-xs text-gray-400 dark:text-dark-500'">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import { desktopAuthBrand } from '@/utils/desktopAuthBranding'

const appStore = useAppStore()
const route = useRoute()
const authBrand = computed(() => desktopAuthBrand(route))

const siteName = computed(() => authBrand.value?.name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => authBrand.value?.logo || sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => authBrand.value?.subtitle || appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => Boolean(authBrand.value) || appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.text-gradient {
  @apply bg-gradient-to-r from-primary-600 to-primary-500 bg-clip-text text-transparent;
}

.wxm-auth {
  --wxm-accent: #6754cf;
  --wxm-accent-hover: #5743bd;
  --wxm-accent-soft: #f2efff;
  height: 100vh;
  height: 100dvh;
  overflow: auto;
  padding: 14px 18px;
  background: #faf9fd;
}
.wxm-auth-content { max-width: 420px; margin-inline: auto; }
.wxm-auth-brand { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; }
.wxm-auth-brand img { width: 36px; height: 36px; flex: none; }
.wxm-auth-brand h1 { color: #272136; font-size: 16px; font-weight: 650; line-height: 20px; }
.wxm-auth-brand p { margin-top: 2px; color: #777184; font-size: 12px; line-height: 16px; }
.wxm-auth-card { padding: 16px; border: 1px solid #e8e4f0; border-radius: 12px; background: #fff; }
.wxm-auth-footer { margin-top: 12px; text-align: center; font-size: 12px; line-height: 18px; }
.wxm-auth-copyright { margin-top: 12px; text-align: center; color: #9a95a3; font-size: 10px; line-height: 16px; }
.wxm-auth :deep(h2) { font-size: 20px; line-height: 28px; }
.wxm-auth :deep(h2 + p) { margin-top: 4px; font-size: 12px; line-height: 18px; }
.wxm-auth :deep(.space-y-6 > :not([hidden]) ~ :not([hidden])) { margin-top: 14px; margin-bottom: 0; }
.wxm-auth :deep(.space-y-5 > :not([hidden]) ~ :not([hidden])) { margin-top: 12px; margin-bottom: 0; }
.wxm-auth :deep(.input-label) { margin-bottom: 4px; font-size: 12px; }
.wxm-auth :deep(.input) { padding-top: 9px; padding-bottom: 9px; }
.wxm-auth :deep(.btn-primary) {
  background-image: linear-gradient(to right, #7661df, var(--wxm-accent));
  --tw-shadow-color: rgb(103 84 207 / 20%);
}
.wxm-auth :deep(.btn-primary:hover) {
  background-image: linear-gradient(to right, var(--wxm-accent), var(--wxm-accent-hover));
  --tw-shadow-color: rgb(103 84 207 / 25%);
}
.wxm-auth :deep(.btn:focus) { --tw-ring-color: rgb(103 84 207 / 45%); }
.wxm-auth :deep(.input:not(.input-error):focus) {
  border-color: var(--wxm-accent);
  --tw-ring-color: rgb(103 84 207 / 25%);
}
.wxm-auth :deep(a:not(.btn-primary)),
.wxm-auth :deep(.text-primary-600),
.wxm-auth :deep(.text-primary-700) { color: var(--wxm-accent); }
.wxm-auth :deep(a:not(.btn-primary):hover) { color: var(--wxm-accent-hover); }
.wxm-auth :deep(.bg-primary-50),
.wxm-auth :deep(.bg-primary-100) { background-color: var(--wxm-accent-soft); }
.wxm-auth :deep(.border-primary-200) { border-color: #d8cff7; }
.dark .wxm-auth { --wxm-accent: #b0a0f7; --wxm-accent-hover: #c4b7ff; --wxm-accent-soft: #292238; background: #15131b; }
.dark .wxm-auth-brand h1 { color: #ece8f7; }
.dark .wxm-auth-card { border-color: #373040; background: #1c1923; }
.dark .wxm-auth :deep(.btn-primary) { color: white; background-image: linear-gradient(to right, #7661df, #6754cf); }
.dark .wxm-auth :deep(.btn-primary:hover) { background-image: linear-gradient(to right, #6754cf, #5743bd); }

@media (max-height: 440px) {
  .wxm-auth { padding-block: 10px; }
  .wxm-auth-brand { margin-bottom: 10px; }
  .wxm-auth-card { padding: 12px; }
}
</style>
