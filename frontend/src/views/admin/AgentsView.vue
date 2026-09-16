<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <Select
            v-model="statusFilter"
            class="w-40"
            :searchable="false"
            :aria-label="t('admin.agents.filterStatus')"
            :options="statusFilterOptions"
            @update:modelValue="handleFilterChange"
          />
          <Select
            v-model="modeFilter"
            class="w-40"
            :searchable="false"
            :aria-label="t('admin.agents.filterMode')"
            :options="modeFilterOptions"
            @update:modelValue="handleFilterChange"
          />
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="reload">
            <Icon name="refresh" size="sm" />{{ t('common.refresh') }}
          </button>
          <span class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.agents.totalCount', { n: pagination.total }) }}
          </span>
          <span v-if="loadError" class="text-sm text-red-500" role="alert">{{ loadError }}</span>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="rows" :loading="loading" row-key="user_id">
          <template #cell-user="{ row }">
            <div class="text-sm">
              <div class="font-medium text-gray-900 dark:text-white">{{ row.email || t('admin.agents.unknownUser') }}</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">ID {{ row.user_id }}</div>
            </div>
          </template>

          <template #cell-mode="{ row }">
            <div class="text-sm">
              <span class="inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium" :class="modeClass(row.mode)">
                {{ t(`admin.agents.modes.${row.mode}`) }}
              </span>
              <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                {{ t(`agency.directions.${row.direction}.title`) }}
              </div>
            </div>
          </template>

          <template #cell-status="{ row }">
            <span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium" :class="statusClass(row.status)">
              {{ t(`admin.agents.statuses.${row.status}`) }}
            </span>
            <p v-if="row.note" class="mt-1 max-w-xs truncate text-xs text-gray-400" :title="row.note">{{ row.note }}</p>
          </template>

          <!-- 分佣代理没有批发价，这一列对他们显示「不适用」而不是留空：
               留空会让人以为是漏配了，跑去给他配一个永远不生效的方案。 -->
          <template #cell-pricing="{ row }">
            <div v-if="row.mode === 'reseller'" class="text-sm">
              <div class="text-gray-900 dark:text-white">{{ planName(row.pricing_plan_id) }}</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">{{ groupName(row.reseller_group_id) }}</div>
            </div>
            <span v-else class="text-xs text-gray-400">{{ t('admin.agents.pricingNotApplicable') }}</span>
          </template>

          <template #cell-activated_at="{ row }">
            <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(row.activated_at) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex gap-2">
              <button type="button" class="btn btn-secondary btn-sm" @click="openStatusEditor(row)">
                {{ t('admin.agents.changeStatus') }}
              </button>
              <button
                v-if="row.mode === 'reseller'"
                type="button"
                class="btn btn-secondary btn-sm"
                @click="openPricingEditor(row)"
              >
                {{ t('admin.agents.changePricing') }}
              </button>
            </div>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.pageSize"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog :show="statusOpen" :title="t('admin.agents.changeStatus')" @close="statusOpen = false">
      <div class="space-y-4">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ editingLabel }}</p>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agents.status') }}</span>
          <Select v-model="statusForm.status" :options="statusOptions" :searchable="false" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agents.note') }}</span>
          <textarea v-model="statusForm.note" rows="3" class="input w-full" :placeholder="t('admin.agents.notePlaceholder')" />
          <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agents.noteHint') }}</span>
        </label>
        <!-- 停用只挡代理侧接口和后续结算，不动已发出去的分组和余额。
             不说清楚的话，运营会以为点一下就把人清干净了。 -->
        <p class="rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:bg-dark-800 dark:text-gray-300">
          {{ t('admin.agents.statusScopeHint') }}
        </p>
        <p v-if="saveError" class="text-sm text-red-500" role="alert">{{ saveError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="statusOpen = false">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="saving" @click="saveStatus">{{ t('common.save') }}</button>
      </template>
    </BaseDialog>

    <BaseDialog :show="pricingOpen" :title="t('admin.agents.changePricing')" @close="pricingOpen = false">
      <div class="space-y-4">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ editingLabel }}</p>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agents.pricingPlan') }}</span>
          <Select v-model="pricingForm.planId" :options="planOptions" :searchable="false" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agents.resellerGroup') }}</span>
          <Select v-model="pricingForm.groupId" :options="groupOptions" :searchable="true" />
          <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agents.resellerGroupHint') }}</span>
        </label>
        <p v-if="saveError" class="text-sm text-red-500" role="alert">{{ saveError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="pricingOpen = false">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="saving" @click="savePricing">{{ t('common.save') }}</button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  listAgents,
  updateAgentStatus,
  updateAgentPricing,
  listAgentPricingPlans,
  type AgentProfile,
  type AgentStatus,
  type AgentPricingPlan
} from '@/api/agent'
import { list as listGroups } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

const STATUSES: AgentStatus[] = ['active', 'suspended', 'terminated']
const MODES = ['affiliate', 'reseller'] as const

const rows = ref<AgentProfile[]>([])
const plans = ref<AgentPricingPlan[]>([])
const groups = ref<AdminGroup[]>([])
const loading = ref(false)
const loadError = ref('')
const statusFilter = ref('')
const modeFilter = ref('')
const pagination = reactive({ page: 1, pageSize: 20, total: 0 })

const editing = ref<AgentProfile | null>(null)
const statusOpen = ref(false)
const pricingOpen = ref(false)
const saving = ref(false)
const saveError = ref('')
const statusForm = reactive<{ status: AgentStatus; note: string }>({ status: 'active', note: '' })
const pricingForm = reactive<{ planId: number; groupId: number }>({ planId: 0, groupId: 0 })

const columns = computed(() => [
  { key: 'user', label: t('admin.agents.user') },
  { key: 'mode', label: t('admin.agents.mode') },
  { key: 'status', label: t('admin.agents.status') },
  { key: 'pricing', label: t('admin.agents.pricing') },
  { key: 'activated_at', label: t('admin.agents.activatedAt') },
  { key: 'actions', label: t('common.actions') }
])

const statusOptions = computed(() =>
  STATUSES.map(value => ({ value, label: t(`admin.agents.statuses.${value}`) }))
)

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.agents.allStatuses') },
  ...statusOptions.value
])

const modeFilterOptions = computed(() => [
  { value: '', label: t('admin.agents.allModes') },
  ...MODES.map(value => ({ value, label: t(`admin.agents.modes.${value}`) }))
])

// 允许「不指定」：把代理从批发价上摘下来是正常运营动作，
// 不该只能靠删档案实现。
const planOptions = computed(() => [
  { value: 0, label: t('admin.agents.noPlan') },
  ...plans.value.map(p => ({ value: p.id, label: p.name }))
])

const groupOptions = computed(() => [
  { value: 0, label: t('admin.agents.noGroup') },
  ...groups.value.map(g => ({ value: g.id, label: g.name }))
])

const editingLabel = computed(() => {
  const row = editing.value
  if (!row) return ''
  return `${row.email || t('admin.agents.unknownUser')} · ID ${row.user_id}`
})

function planName(id?: number): string {
  if (!id) return t('admin.agents.noPlan')
  return plans.value.find(p => p.id === id)?.name ?? `#${id}`
}

function groupName(id?: number): string {
  if (!id) return t('admin.agents.noGroup')
  return groups.value.find(g => g.id === id)?.name ?? `#${id}`
}

function modeClass(mode: string): string {
  return mode === 'reseller'
    ? 'bg-purple-100 text-purple-700 dark:bg-purple-500/20 dark:text-purple-300'
    : 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-300'
}

function statusClass(status: string): string {
  switch (status) {
    case 'active':
      return 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-300'
    case 'suspended':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300'
    default:
      return 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300'
  }
}

function formatDateTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}

async function reload() {
  loading.value = true
  loadError.value = ''
  try {
    const result = await listAgents({
      status: statusFilter.value,
      mode: modeFilter.value,
      limit: pagination.pageSize,
      offset: (pagination.page - 1) * pagination.pageSize
    })
    rows.value = result.items
    pagination.total = result.total
  } catch {
    // 静默失败最糟：运营会盯着一份上次的旧列表，以为没人新开通。
    loadError.value = t('admin.agents.loadFailed')
  } finally {
    loading.value = false
  }
}

// 换筛选条件要回第一页：停在第 3 页筛一个只有两条的状态，列表会空一片。
function handleFilterChange() {
  pagination.page = 1
  void reload()
}

function handlePageChange(page: number) {
  pagination.page = page
  void reload()
}

function handlePageSizeChange(size: number) {
  pagination.pageSize = size
  pagination.page = 1
  void reload()
}

function openStatusEditor(row: AgentProfile) {
  editing.value = row
  statusForm.status = row.status
  statusForm.note = row.note ?? ''
  saveError.value = ''
  statusOpen.value = true
}

function openPricingEditor(row: AgentProfile) {
  editing.value = row
  pricingForm.planId = row.pricing_plan_id ?? 0
  pricingForm.groupId = row.reseller_group_id ?? 0
  saveError.value = ''
  pricingOpen.value = true
}

async function saveStatus() {
  if (!editing.value || saving.value) return
  saving.value = true
  saveError.value = ''
  try {
    await updateAgentStatus(editing.value.user_id, {
      status: statusForm.status,
      note: statusForm.note
    })
    statusOpen.value = false
    await reload()
  } catch (e) {
    saveError.value = extractApiErrorMessage(e) || t('admin.agents.saveFailed')
  } finally {
    saving.value = false
  }
}

async function savePricing() {
  if (!editing.value || saving.value) return
  saving.value = true
  saveError.value = ''
  try {
    // 0 表示「不指定」，转成 null 让后端清掉该字段。
    await updateAgentPricing(editing.value.user_id, {
      pricing_plan_id: pricingForm.planId || null,
      reseller_group_id: pricingForm.groupId || null
    })
    pricingOpen.value = false
    await reload()
  } catch (e) {
    saveError.value = extractApiErrorMessage(e) || t('admin.agents.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void reload()
  // 方案和分组用来把 ID 翻译成名字，加载失败不该挡住列表本身。
  listAgentPricingPlans()
    .then(list => { plans.value = list })
    .catch(() => { plans.value = [] })
  listGroups(1, 200)
    .then(result => { groups.value = result.items ?? [] })
    .catch(() => { groups.value = [] })
})
</script>
