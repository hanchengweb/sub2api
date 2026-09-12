<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <Select
            v-model="statusFilter"
            class="w-44"
            :searchable="false"
            :aria-label="t('admin.agency.filterStatus')"
            :options="statusFilterOptions"
            @update:modelValue="handleStatusChange"
          />
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="reload">
            <Icon name="refresh" size="sm" />{{ t('common.refresh') }}
          </button>
          <span class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.agency.totalCount', { n: pagination.total }) }}
          </span>
          <span v-if="loadError" class="text-sm text-red-500" role="alert">{{ loadError }}</span>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="rows" :loading="loading" row-key="id">
          <template #cell-direction="{ row }">
            <span class="inline-flex items-center rounded-md bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-200">
              {{ t(`agency.directions.${row.direction}.title`) }}
            </span>
          </template>

          <template #cell-contact="{ row }">
            <div class="text-sm">
              <div class="font-medium text-gray-900 dark:text-white">{{ row.contact_name }}</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">{{ row.email }}</div>
              <div v-if="row.company" class="text-xs text-gray-400">{{ row.company }}</div>
            </div>
          </template>

          <!-- 场景是判断值不值得跟进的关键，但可能很长。这里限高，点一下展开全文。 -->
          <template #cell-scenario="{ row }">
            <p
              class="max-w-md cursor-pointer whitespace-pre-wrap text-sm text-gray-700 dark:text-gray-200"
              :class="expanded.has(row.id) ? '' : 'line-clamp-2'"
              :title="t('admin.agency.toggleScenario')"
              @click="toggleScenario(row.id)"
            >{{ row.scenario }}</p>
          </template>

          <template #cell-status="{ row }">
            <span class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium" :class="statusClass(row.status)">
              {{ t(`agency.status.${row.status}`) }}
            </span>
            <p v-if="row.admin_note" class="mt-1 max-w-xs truncate text-xs text-gray-400" :title="row.admin_note">
              {{ row.admin_note }}
            </p>
          </template>

          <template #cell-created_at="{ row }">
            <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(row.created_at) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <button type="button" class="btn btn-secondary btn-sm" @click="openEditor(row)">
              {{ t('admin.agency.handle') }}
            </button>
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

    <BaseDialog :show="editorOpen" :title="t('admin.agency.handle')" @close="editorOpen = false">
      <div v-if="editing" class="space-y-4">
        <div class="rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800">
          <p class="font-medium text-gray-900 dark:text-white">
            {{ editing.contact_name }}
            <span class="ml-2 font-normal text-gray-500">{{ editing.email }}</span>
          </p>
          <p class="mt-1 whitespace-pre-wrap text-gray-600 dark:text-gray-300">{{ editing.scenario }}</p>
        </div>

        <div>
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">
            {{ t('admin.agency.status') }}
          </label>
          <Select v-model="editForm.status" :searchable="false" :options="statusOptions" />
        </div>

        <div>
          <label class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">
            {{ t('admin.agency.note') }}
          </label>
          <!-- 这条备注申请人看得到，别写内部信息 -->
          <textarea
            v-model="editForm.admin_note"
            rows="3"
            maxlength="2000"
            class="input w-full"
            :placeholder="t('admin.agency.notePlaceholder')"
          />
          <p class="mt-1 text-xs text-gray-400">{{ t('admin.agency.noteHint') }}</p>
        </div>

        <p v-if="saveError" class="text-sm text-red-500" role="alert">{{ saveError }}</p>
      </div>

      <template #footer>
        <button type="button" class="btn btn-secondary" @click="editorOpen = false">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="saving" @click="save">
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
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
  listAgencyApplications,
  updateAgencyApplication,
  type AdminAgencyApplication,
  type AgencyStatus
} from '@/api/agency'

const { t } = useI18n()

const STATUSES: AgencyStatus[] = ['pending', 'contacted', 'accepted', 'rejected']

const rows = ref<AdminAgencyApplication[]>([])
const loading = ref(false)
const statusFilter = ref('')
const expanded = ref(new Set<number>())
const pagination = reactive({ page: 1, pageSize: 20, total: 0 })

const editorOpen = ref(false)
const editing = ref<AdminAgencyApplication | null>(null)
const editForm = reactive<{ status: AgencyStatus; admin_note: string }>({ status: 'pending', admin_note: '' })
const saving = ref(false)
const saveError = ref('')
const loadError = ref('')

const columns = computed(() => [
  { key: 'direction', label: t('admin.agency.direction') },
  { key: 'contact', label: t('admin.agency.contact') },
  { key: 'scenario', label: t('agency.scenario') },
  { key: 'status', label: t('admin.agency.status') },
  { key: 'created_at', label: t('admin.agency.submittedAt') },
  { key: 'actions', label: t('common.actions') }
])

const statusOptions = computed(() =>
  STATUSES.map(value => ({ value, label: t(`agency.status.${value}`) }))
)

// 默认「全部」：运营最常做的是扫一眼有没有新的，而不是先选筛选条件。
const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.agency.allStatuses') },
  ...statusOptions.value
])

function statusClass(status: string): string {
  switch (status) {
    case 'accepted':
      return 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-300'
    case 'rejected':
      return 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-300'
    case 'contacted':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-300'
    default:
      return 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300'
  }
}

function formatDateTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}

function toggleScenario(id: number) {
  const next = new Set(expanded.value)
  next.has(id) ? next.delete(id) : next.add(id)
  expanded.value = next
}

async function reload() {
  loading.value = true
  loadError.value = ''
  try {
    const result = await listAgencyApplications({
      status: statusFilter.value,
      limit: pagination.pageSize,
      offset: (pagination.page - 1) * pagination.pageSize
    })
    rows.value = result.items
    pagination.total = result.total
  } catch {
    // 静默失败最糟：运营会盯着一份上次的旧列表，以为没有新申请。
    loadError.value = t('admin.agency.loadFailed')
  } finally {
    loading.value = false
  }
}

// 换筛选条件要回第一页：停在第 3 页筛一个只有两条的状态，列表会空一片。
function handleStatusChange() {
  pagination.page = 1
  void reload()
}

function handlePageChange(page: number) {
  pagination.page = page
  void reload()
}

function handlePageSizeChange(size: number) {
  pagination.pageSize = size
  // 每页条数变了要回到第一页：留在第 5 页可能已经越界，列表会空一片
  pagination.page = 1
  void reload()
}

function openEditor(row: AdminAgencyApplication) {
  editing.value = row
  editForm.status = row.status
  editForm.admin_note = row.admin_note ?? ''
  saveError.value = ''
  editorOpen.value = true
}

async function save() {
  if (!editing.value || saving.value) return
  saving.value = true
  saveError.value = ''
  try {
    await updateAgencyApplication(editing.value.id, {
      status: editForm.status,
      admin_note: editForm.admin_note
    })
    editorOpen.value = false
    await reload()
  } catch {
    saveError.value = t('admin.agency.saveFailed')
  } finally {
    saving.value = false
  }
}

onMounted(reload)
</script>
