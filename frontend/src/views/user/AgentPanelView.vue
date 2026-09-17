<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-6xl space-y-6 p-4 sm:p-6">
      <div v-if="loading" class="py-16 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('common.loading') }}
      </div>

      <!--
        不是代理时不显示一个空面板，而是把人送回申请页。
        字段全空的面板会让人以为是加载失败，反复刷新。
      -->
      <section
        v-else-if="!profile"
        class="rounded-lg border border-gray-200 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800"
      >
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.notAgentTitle') }}</h2>
        <p class="mx-auto mt-2 max-w-md text-sm text-gray-500 dark:text-gray-400">{{ t('agentPanel.notAgentHint') }}</p>
        <RouterLink to="/become-agent" class="btn btn-primary mt-5 inline-flex">{{ t('agentPanel.goApply') }}</RouterLink>
      </section>

      <template v-else>
        <!-- 身份 + 邀请链接 -->
        <section class="rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.title') }}</h2>
              <p class="mt-1 max-w-2xl text-sm text-gray-500 dark:text-gray-400">{{ t('agentPanel.hint') }}</p>
            </div>
            <span class="inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium" :class="statusClass">
              {{ t(`agentPanel.statuses.${profile.status}`) }}
            </span>
          </div>

          <p
            v-if="profile.status !== 'active'"
            class="mt-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-500/10 dark:text-amber-300"
            role="alert"
          >
            {{ t(`agentPanel.statusHint.${profile.status}`) }}
          </p>

          <div v-if="affCode" class="mt-5 space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
            <p class="text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('agentPanel.inviteTitle') }}</p>
            <!-- 归属是注册那一刻锁定的、事后不能改，这一点必须写在链接旁边 -->
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.inviteHint') }}</p>
            <div class="flex flex-wrap items-center gap-2">
              <input :value="inviteLink" readonly class="input min-w-0 flex-1 font-mono text-xs" />
              <button type="button" class="btn btn-secondary btn-sm" @click="copyLink">
                <Icon name="copy" size="sm" />{{ t('common.copy') }}
              </button>
            </div>
          </div>
        </section>

        <!-- 概览 -->
        <section class="grid gap-4 sm:grid-cols-3">
          <div class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.customerCount') }}</p>
            <p class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">{{ customerTotal }}</p>
          </div>
          <div class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.customerSpend') }}</p>
            <p class="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">
              {{ formatCredits(totalCustomerSpend) }}
            </p>
          </div>
          <div class="rounded-lg border border-gray-200 bg-white p-4 dark:border-dark-600 dark:bg-dark-800">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.settledRebate') }}</p>
            <p class="mt-1 text-2xl font-semibold tabular-nums text-green-600 dark:text-green-400">
              {{ formatCredits(totalRebate) }}
            </p>
          </div>
        </section>

        <!-- 我的客户 -->
        <section class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
          <header class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-200 px-5 py-3 dark:border-dark-600">
            <div>
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.customersTitle') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.customersHint') }}</p>
            </div>
            <span v-if="customerError" class="text-sm text-red-500" role="alert">{{ customerError }}</span>
          </header>
          <DataTable :columns="customerColumns" :data="customers" :loading="customersLoading" row-key="user_id">
            <template #cell-email="{ row }">
              <div class="text-sm">
                <div class="text-gray-900 dark:text-white">{{ row.email }}</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">ID {{ row.user_id }}</div>
              </div>
            </template>
            <template #cell-total_cost_credits="{ row }">
              <span class="text-sm tabular-nums">{{ formatCredits(row.total_cost_credits) }}</span>
            </template>
            <template #cell-request_count="{ row }">
              <span class="text-sm tabular-nums">{{ row.request_count }}</span>
            </template>
            <template #cell-last_active_at="{ row }">
              <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(row.last_active_at) || '—' }}</span>
            </template>
            <template #cell-actions="{ row }">
              <button type="button" class="btn btn-secondary btn-sm" @click="filterUsageBy(row.user_id)">
                {{ t('agentPanel.viewUsage') }}
              </button>
            </template>
          </DataTable>
        </section>

        <!-- 客户用量 -->
        <section class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
          <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-5 py-3 dark:border-dark-600">
            <div>
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.usageTitle') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.usageHint') }}</p>
            </div>
            <button v-if="usageCustomerId" type="button" class="btn btn-secondary btn-sm" @click="filterUsageBy(0)">
              {{ t('agentPanel.clearFilter') }}
            </button>
          </header>
          <DataTable :columns="usageColumns" :data="usage" :loading="usageLoading" row-key="id">
            <template #cell-customer_email="{ row }">
              <span class="text-sm text-gray-700 dark:text-gray-200">{{ row.customer_email }}</span>
            </template>
            <template #cell-model="{ row }">
              <div class="text-sm">
                <div class="text-gray-900 dark:text-white">{{ row.model }}</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">
                  {{ row.billing_mode }}<span v-if="row.image_count > 1"> · ×{{ row.image_count }}</span>
                </div>
              </div>
            </template>
            <template #cell-actual_cost="{ row }">
              <span class="text-sm tabular-nums">{{ formatCredits(row.actual_cost) }}</span>
            </template>
            <template #cell-created_at="{ row }">
              <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(row.created_at) }}</span>
            </template>
          </DataTable>
          <div v-if="usageTotal > usagePageSize" class="border-t border-gray-200 px-5 py-3 dark:border-dark-600">
            <Pagination
              :page="usagePage"
              :total="usageTotal"
              :page-size="usagePageSize"
              @update:page="handleUsagePage"
              @update:pageSize="handleUsagePageSize"
            />
          </div>
        </section>

        <!-- 返现结算 -->
        <section class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
          <header class="border-b border-gray-200 px-5 py-3 dark:border-dark-600">
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.settlementsTitle') }}</h3>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.settlementsHint') }}</p>
          </header>
          <DataTable :columns="settlementColumns" :data="settlements" :loading="settlementsLoading" row-key="id">
            <template #cell-period="{ row }">
              <span class="text-sm text-gray-500 dark:text-gray-400">
                {{ formatDateTime(row.period_start) }} ~ {{ formatDateTime(row.period_end) }}
              </span>
            </template>
            <template #cell-text="{ row }">
              <div class="text-sm">
                <div class="tabular-nums">{{ formatCredits(row.text_rebate_credits) }}</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('agentPanel.fromSpend', { n: formatCredits(row.text_cost_credits) }) }}
                </div>
              </div>
            </template>
            <template #cell-multimodal="{ row }">
              <div class="text-sm">
                <div class="tabular-nums">{{ formatCredits(row.multimodal_rebate_credits) }}</div>
                <div class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('agentPanel.fromUnits', { n: row.multimodal_units }) }}
                </div>
              </div>
            </template>
            <template #cell-total_rebate_credits="{ row }">
              <span class="text-sm font-medium tabular-nums text-green-600 dark:text-green-400">
                {{ formatCredits(row.total_rebate_credits) }}
              </span>
            </template>
            <template #cell-created_at="{ row }">
              <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(row.created_at) }}</span>
            </template>
          </DataTable>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  getMyAgentProfile,
  listMyCustomers,
  listMyCustomerUsage,
  listMySettlements,
  type MyAgentProfile,
  type AgentCustomer,
  type AgentCustomerUsage,
  type AgentSettlement
} from '@/api/agent'
import userAPI from '@/api/user'
import { useClipboard } from '@/composables/useClipboard'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const loading = ref(true)
const profile = ref<MyAgentProfile | null>(null)
const affCode = ref('')

const customers = ref<AgentCustomer[]>([])
const customerTotal = ref(0)
const customersLoading = ref(false)
const customerError = ref('')

const usage = ref<AgentCustomerUsage[]>([])
const usageTotal = ref(0)
const usageLoading = ref(false)
const usagePage = ref(1)
const usagePageSize = ref(20)
const usageCustomerId = ref(0)

const settlements = ref<AgentSettlement[]>([])
const settlementsLoading = ref(false)

const customerColumns = computed(() => [
  { key: 'email', label: t('agentPanel.customerEmail') },
  { key: 'total_cost_credits', label: t('agentPanel.customerSpend') },
  { key: 'request_count', label: t('agentPanel.requests') },
  { key: 'last_active_at', label: t('agentPanel.lastActive') },
  { key: 'actions', label: t('common.actions') }
])

const usageColumns = computed(() => [
  { key: 'customer_email', label: t('agentPanel.customerEmail') },
  { key: 'model', label: t('agentPanel.model') },
  { key: 'actual_cost', label: t('agentPanel.cost') },
  { key: 'created_at', label: t('agentPanel.time') }
])

const settlementColumns = computed(() => [
  { key: 'period', label: t('agentPanel.period') },
  { key: 'text', label: t('agentPanel.textRebate') },
  { key: 'multimodal', label: t('agentPanel.multimodalRebate') },
  { key: 'total_rebate_credits', label: t('agentPanel.totalRebate') },
  { key: 'created_at', label: t('agentPanel.settledAt') }
])

const totalCustomerSpend = computed(() =>
  customers.value.reduce((sum, c) => sum + (c.total_cost_credits || 0), 0)
)

const totalRebate = computed(() =>
  settlements.value.reduce((sum, s) => sum + (s.total_rebate_credits || 0), 0)
)

const inviteLink = computed(() =>
  affCode.value ? `${window.location.origin}/register?aff=${affCode.value}` : ''
)

const statusClass = computed(() => {
  switch (profile.value?.status) {
    case 'active':
      return 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-300'
    case 'suspended':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300'
    default:
      return 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300'
  }
})

// created_at 可能缺失，缺了就留空而不是渲染成 Invalid Date。
function formatDateTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}

// 积分保留两位。这是钱，不要用紧凑单位（1.2K 看不出差几分钱）。
function formatCredits(v: number | null | undefined): string {
  const n = Number(v)
  return Number.isFinite(n) ? n.toFixed(2) : '0.00'
}

async function copyLink() {
  await copyToClipboard(inviteLink.value)
}

async function loadCustomers() {
  customersLoading.value = true
  customerError.value = ''
  try {
    const result = await listMyCustomers({ limit: 100 })
    customers.value = result.items
    customerTotal.value = result.total
  } catch {
    customerError.value = t('agentPanel.customersLoadFailed')
  } finally {
    customersLoading.value = false
  }
}

async function loadUsage() {
  usageLoading.value = true
  try {
    const result = await listMyCustomerUsage({
      customer_id: usageCustomerId.value || undefined,
      limit: usagePageSize.value,
      offset: (usagePage.value - 1) * usagePageSize.value
    })
    usage.value = result.items
    usageTotal.value = result.total
  } catch {
    usage.value = []
  } finally {
    usageLoading.value = false
  }
}

async function loadSettlements() {
  settlementsLoading.value = true
  try {
    const result = await listMySettlements({ limit: 50 })
    settlements.value = result.items
  } catch {
    settlements.value = []
  } finally {
    settlementsLoading.value = false
  }
}

// 换筛选要回第一页：停在第 3 页筛一个只有两条的客户，列表会空一片。
function filterUsageBy(customerID: number) {
  usageCustomerId.value = customerID
  usagePage.value = 1
  void loadUsage()
}

function handleUsagePage(page: number) {
  usagePage.value = page
  void loadUsage()
}

function handleUsagePageSize(size: number) {
  usagePageSize.value = size
  usagePage.value = 1
  void loadUsage()
}

onMounted(async () => {
  try {
    profile.value = await getMyAgentProfile()
  } catch {
    // 后端对「不是代理」回 404，这是那条路径，不是错误。
    profile.value = null
  } finally {
    loading.value = false
  }
  if (!profile.value) return

  // 邀请码仍然从 affiliate 接口拿——它只负责发码和存归属关系，
  // 返利那套已经停用，代理的钱走 settlements。
  userAPI
    .getAffiliateDetail()
    .then(detail => { affCode.value = detail.aff_code })
    .catch(() => { affCode.value = '' })

  void loadCustomers()
  void loadUsage()
  void loadSettlements()
})
</script>
