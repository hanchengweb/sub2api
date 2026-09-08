import { defineStore } from 'pinia'
import { ref } from 'vue'
import { adminAPI } from '@/api'
import type { CustomMenuItem } from '@/types'

export const useAdminSettingsStore = defineStore('adminSettings', () => {
  const loaded = ref(false)
  const loading = ref(false)

  const readCachedBool = (key: string, defaultValue: boolean): boolean => {
    try {
      const raw = localStorage.getItem(key)
      if (raw === 'true') return true
      if (raw === 'false') return false
    } catch {
      // ignore localStorage failures
    }
    return defaultValue
  }

  const writeCachedBool = (key: string, value: boolean) => {
    try {
      localStorage.setItem(key, value ? 'true' : 'false')
    } catch {
      // ignore localStorage failures
    }
  }

  const readCachedString = (key: string, defaultValue: string): string => {
    try {
      const raw = localStorage.getItem(key)
      if (typeof raw === 'string' && raw.length > 0) return raw
    } catch {
      // ignore localStorage failures
    }
    return defaultValue
  }

  const writeCachedString = (key: string, value: string) => {
    try {
      localStorage.setItem(key, value)
    } catch {
      // ignore localStorage failures
    }
  }

  // Default open, but honor cached value to reduce UI flicker on first paint.
  const opsMonitoringEnabled = ref(readCachedBool('ops_monitoring_enabled_cached', true))
  const opsRealtimeMonitoringEnabled = ref(readCachedBool('ops_realtime_monitoring_enabled_cached', true))
  const opsQueryModeDefault = ref(readCachedString('ops_query_mode_default_cached', 'auto'))
  const paymentEnabled = ref(readCachedBool('payment_enabled_cached', false))
  // 积分↔人民币的换算率（后端 BALANCE_RECHARGE_MULTIPLIER，充 ¥1 得多少积分）。
  // 管理端把上游成本换回 ¥ 展示时需要它。不能写死 100：这个率是可改的，
  // 写死了改率那天所有金额都会静默错。
  const creditsPerCurrencyUnit = ref(Number(readCachedString('credits_per_currency_unit_cached', '100')) || 100)
  const customMenuItems = ref<CustomMenuItem[]>([])

  async function fetch(force = false): Promise<void> {
    if (loaded.value && !force) return
    if (loading.value) return

    loading.value = true
    try {
      const [settings, paymentConfigResp] = await Promise.all([
        adminAPI.settings.getSettings(),
        adminAPI.payment.getConfig()
      ])
      opsMonitoringEnabled.value = settings.ops_monitoring_enabled ?? true
      writeCachedBool('ops_monitoring_enabled_cached', opsMonitoringEnabled.value)

      opsRealtimeMonitoringEnabled.value = settings.ops_realtime_monitoring_enabled ?? true
      writeCachedBool('ops_realtime_monitoring_enabled_cached', opsRealtimeMonitoringEnabled.value)

      opsQueryModeDefault.value = settings.ops_query_mode_default || 'auto'
      writeCachedString('ops_query_mode_default_cached', opsQueryModeDefault.value)

      customMenuItems.value = Array.isArray(settings.custom_menu_items) ? settings.custom_menu_items : []

      paymentEnabled.value = paymentConfigResp.data?.enabled ?? false
      writeCachedBool('payment_enabled_cached', paymentEnabled.value)

      const multiplier = Number(paymentConfigResp.data?.balance_recharge_multiplier)
      if (Number.isFinite(multiplier) && multiplier > 0) {
        creditsPerCurrencyUnit.value = multiplier
        writeCachedString('credits_per_currency_unit_cached', String(multiplier))
      }

      loaded.value = true
    } catch (err) {
      // Keep cached/default value: do not "flip" the UI based on a transient fetch failure.
      loaded.value = true
      console.error('[adminSettings] Failed to fetch settings:', err)
    } finally {
      loading.value = false
    }
  }

  function setOpsMonitoringEnabledLocal(value: boolean) {
    opsMonitoringEnabled.value = value
    writeCachedBool('ops_monitoring_enabled_cached', value)
    loaded.value = true
  }

  function setOpsRealtimeMonitoringEnabledLocal(value: boolean) {
    opsRealtimeMonitoringEnabled.value = value
    writeCachedBool('ops_realtime_monitoring_enabled_cached', value)
    loaded.value = true
  }

  function setPaymentEnabledLocal(value: boolean) {
    paymentEnabled.value = value
    writeCachedBool('payment_enabled_cached', value)
    loaded.value = true
  }

  function setOpsQueryModeDefaultLocal(value: string) {
    opsQueryModeDefault.value = value || 'auto'
    writeCachedString('ops_query_mode_default_cached', opsQueryModeDefault.value)
    loaded.value = true
  }

  // Keep UI consistent if we learn that ops is disabled via feature-gated 404s.
  // (event is dispatched from the axios interceptor)
  let eventHandlerCleanup: (() => void) | null = null

  function initializeEventListeners() {
    if (eventHandlerCleanup) return

    try {
      const handler = () => {
        setOpsMonitoringEnabledLocal(false)
      }
      window.addEventListener('ops-monitoring-disabled', handler)
      eventHandlerCleanup = () => {
        window.removeEventListener('ops-monitoring-disabled', handler)
      }
    } catch {
      // ignore window access failures (SSR)
    }
  }

  if (typeof window !== 'undefined') {
    initializeEventListeners()
  }

  return {
    loaded,
    loading,
    opsMonitoringEnabled,
    opsRealtimeMonitoringEnabled,
    opsQueryModeDefault,
    paymentEnabled,
    creditsPerCurrencyUnit,
    customMenuItems,
    fetch,
    setOpsMonitoringEnabledLocal,
    setOpsRealtimeMonitoringEnabledLocal,
    setPaymentEnabledLocal,
    setOpsQueryModeDefaultLocal
  }
})
