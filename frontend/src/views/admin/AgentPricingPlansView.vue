<template>
  <AppLayout>
    <div class="space-y-6 p-4 sm:p-6">
      <!-- 方案列表 -->
      <section class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
        <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-600">
          <div>
            <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.agentPricing.plansTitle') }}</h2>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agentPricing.plansHint') }}</p>
          </div>
          <button type="button" class="btn btn-primary" @click="openPlanEditor(null)">
            <Icon name="plus" size="sm" />{{ t('admin.agentPricing.newPlan') }}
          </button>
        </header>

        <DataTable :columns="planColumns" :data="plans" :loading="plansLoading" row-key="id">
          <template #cell-text_discount="{ row }">
            <span class="text-sm">{{ formatDiscount(row.text_discount) }}</span>
          </template>
          <template #cell-multimodal_deduction_cny="{ row }">
            <span class="text-sm">￥{{ row.multimodal_deduction_cny.toFixed(2) }} / {{ t('admin.agentPricing.perCall') }}</span>
          </template>
          <template #cell-enforce_cost_floor="{ row }">
            <span :class="row.enforce_cost_floor ? 'text-green-600 dark:text-green-400' : 'text-amber-600 dark:text-amber-400'" class="text-sm">
              {{ row.enforce_cost_floor ? t('common.enabled') : t('common.disabled') }}
            </span>
          </template>
          <template #cell-actions="{ row }">
            <button type="button" class="btn btn-secondary btn-sm" @click="openPlanEditor(row)">
              {{ t('common.edit') }}
            </button>
          </template>
        </DataTable>
        <p v-if="plansError" class="px-4 py-2 text-sm text-red-500" role="alert">{{ plansError }}</p>
      </section>

      <!-- 生成器 -->
      <section class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
        <header class="border-b border-gray-200 px-4 py-3 dark:border-dark-600">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.agentPricing.generatorTitle') }}</h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agentPricing.generatorHint') }}</p>
        </header>

        <div class="grid gap-4 px-4 py-4 sm:grid-cols-3">
          <label class="block">
            <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agentPricing.plan') }}</span>
            <Select v-model="form.planId" :options="planOptions" :searchable="false" />
          </label>
          <label class="block">
            <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agentPricing.sourceChannel') }}</span>
            <Select v-model="form.sourceChannelId" :options="channelOptions" :searchable="false" />
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agentPricing.sourceHint') }}</span>
          </label>
          <label class="block">
            <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agentPricing.targetChannel') }}</span>
            <Select v-model="form.targetChannelId" :options="targetChannelOptions" :searchable="false" />
            <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agentPricing.targetHint') }}</span>
          </label>
        </div>

        <!-- 目标 = 来源会把零售价原地改成代理价，全站降价。挡在按钮之前。 -->
        <p v-if="targetEqualsSource" class="mx-4 mb-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-500/10 dark:text-red-300" role="alert">
          {{ t('admin.agentPricing.targetEqualsSource') }}
        </p>

        <div class="flex flex-wrap items-center gap-3 border-t border-gray-200 px-4 py-3 dark:border-dark-600">
          <button type="button" class="btn btn-secondary" :disabled="!canPreview || previewing" @click="runPreview">
            <Icon name="refresh" size="sm" />{{ t('admin.agentPricing.preview') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="!canApply || applying" @click="confirmOpen = true">
            {{ t('admin.agentPricing.apply') }}
          </button>
          <span v-if="genError" class="text-sm text-red-500" role="alert">{{ genError }}</span>
          <span v-if="applyResult" class="text-sm text-green-600 dark:text-green-400">
            {{ t('admin.agentPricing.applied', { n: applyResult.written_pricings, channel: applyResult.target_channel }) }}
          </span>
        </div>

        <p v-if="applyResult?.warning" class="mx-4 mb-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-500/10 dark:text-amber-300" role="alert">
          {{ applyResult.warning }}
        </p>
      </section>

      <!-- 预览结果 -->
      <section v-if="preview" class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
        <header class="border-b border-gray-200 px-4 py-3 dark:border-dark-600">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.agentPricing.previewTitle', { plan: preview.plan_name }) }}
          </h2>
          <!--
            汇率必须显眼：多模态让利是按人民币配的，￥1↔积分的换算率取错的话，
            「让一毛」会变成「让一分」或者「让一块」，而算出来的数字本身看不出问题。
          -->
          <div class="mt-2 flex flex-wrap gap-x-6 gap-y-1 text-sm">
            <span class="text-gray-700 dark:text-gray-200">
              {{ t('admin.agentPricing.creditRate') }}:
              <strong>{{ preview.credits_per_cny }}</strong> {{ t('admin.agentPricing.creditsPerYuan') }}
            </span>
            <span class="text-gray-700 dark:text-gray-200">
              {{ t('admin.agentPricing.deduction') }}:
              <strong>{{ preview.deduction_credits }}</strong> {{ t('common.creditUnit') }}
            </span>
            <span class="text-gray-500 dark:text-gray-400">
              {{ t('admin.agentPricing.rowSummary', {
                total: preview.total_rows,
                skipped: preview.skipped_rows,
                warned: preview.warning_rows
              }) }}
            </span>
          </div>
        </header>

        <DataTable :columns="previewColumns" :data="preview.changes" :loading="false" row-key="rowKey">
          <template #cell-models="{ row }">
            <div class="text-sm">
              <div class="font-medium text-gray-900 dark:text-white">{{ row.models.join(', ') }}</div>
              <div class="text-xs text-gray-500 dark:text-gray-400">
                {{ row.billing_mode }} · {{ row.field }}
                <span v-if="row.tier_label" class="ml-1 rounded bg-gray-100 px-1 dark:bg-dark-700">{{ row.tier_label }}</span>
              </div>
            </div>
          </template>
          <template #cell-retail_price="{ row }">
            <span class="text-sm tabular-nums">{{ trimNumber(row.retail_price) }}</span>
          </template>
          <template #cell-agent_price="{ row }">
            <span class="text-sm font-medium tabular-nums" :class="row.skipped ? 'text-amber-600 dark:text-amber-400' : ''">
              {{ trimNumber(row.agent_price) }}
            </span>
          </template>
          <template #cell-margin="{ row }">
            <span class="text-sm tabular-nums" :class="marginClass(row)">{{ marginText(row) }}</span>
          </template>
          <template #cell-warning="{ row }">
            <span v-if="row.warning" class="text-xs text-amber-700 dark:text-amber-300">{{ row.warning }}</span>
            <span v-else class="text-xs text-gray-400">—</span>
          </template>
        </DataTable>
      </section>
    </div>

    <!-- 方案编辑 -->
    <BaseDialog
      :show="planEditorOpen"
      :title="editingPlan ? t('admin.agentPricing.editPlan') : t('admin.agentPricing.newPlan')"
      @close="planEditorOpen = false"
    >
      <div class="space-y-4">
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agentPricing.planName') }}</span>
          <input v-model="planForm.name" type="text" class="input w-full" maxlength="100" />
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agentPricing.textDiscount') }}</span>
          <input v-model.number="planForm.text_discount" type="number" step="0.01" min="0.01" max="1" class="input w-full" />
          <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agentPricing.textDiscountHint') }}</span>
        </label>
        <label class="block">
          <span class="mb-1 block text-sm font-medium text-gray-700 dark:text-gray-200">{{ t('admin.agentPricing.deductionCny') }}</span>
          <input v-model.number="planForm.multimodal_deduction_cny" type="number" step="0.01" min="0" class="input w-full" />
          <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.agentPricing.deductionCnyHint') }}</span>
        </label>
        <label class="flex items-center gap-2">
          <input v-model="planForm.enforce_cost_floor" type="checkbox" class="checkbox" />
          <span class="text-sm text-gray-700 dark:text-gray-200">{{ t('admin.agentPricing.costFloor') }}</span>
        </label>
        <p v-if="planError" class="text-sm text-red-500" role="alert">{{ planError }}</p>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="planEditorOpen = false">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="planSaving" @click="savePlan">{{ t('common.save') }}</button>
      </template>
    </BaseDialog>

    <!-- 落库前二次确认：这一步会改代理实际成交价 -->
    <BaseDialog :show="confirmOpen" :title="t('admin.agentPricing.confirmTitle')" @close="confirmOpen = false">
      <div class="space-y-3 text-sm text-gray-700 dark:text-gray-200">
        <p>{{ t('admin.agentPricing.confirmBody', { channel: targetChannelName, n: preview?.total_rows ?? 0 }) }}</p>
        <p v-if="preview && preview.warning_rows > 0" class="rounded-md bg-amber-50 px-3 py-2 text-amber-800 dark:bg-amber-500/10 dark:text-amber-300">
          {{ t('admin.agentPricing.confirmWarnings', { n: preview.warning_rows }) }}
        </p>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="confirmOpen = false">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="applying" @click="runApply">{{ t('admin.agentPricing.confirmApply') }}</button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import {
  listAgentPricingPlans,
  createAgentPricingPlan,
  updateAgentPricingPlan,
  previewAgentPricing,
  applyAgentPricing,
  type AgentPricingPlan,
  type AgentPricingPreview,
  type AgentPricingApplyResult,
  type AgentPriceChange
} from '@/api/agent'
import { list as listChannels, type Channel } from '@/api/admin/channels'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

const plans = ref<AgentPricingPlan[]>([])
const plansLoading = ref(false)
const plansError = ref('')

const channels = ref<Channel[]>([])

const form = reactive({ planId: 0, sourceChannelId: 0, targetChannelId: 0 })
const preview = ref<AgentPricingPreview | null>(null)
const previewing = ref(false)
const applying = ref(false)
const genError = ref('')
const applyResult = ref<AgentPricingApplyResult | null>(null)
const confirmOpen = ref(false)

const planEditorOpen = ref(false)
const editingPlan = ref<AgentPricingPlan | null>(null)
const planSaving = ref(false)
const planError = ref('')
const planForm = reactive({
  name: '',
  text_discount: 0.8,
  multimodal_deduction_cny: 0.1,
  enforce_cost_floor: true
})

const planColumns = computed(() => [
  { key: 'name', label: t('admin.agentPricing.planName') },
  { key: 'text_discount', label: t('admin.agentPricing.textDiscount') },
  { key: 'multimodal_deduction_cny', label: t('admin.agentPricing.deductionCny') },
  { key: 'enforce_cost_floor', label: t('admin.agentPricing.costFloor') },
  { key: 'actions', label: t('common.actions') }
])

const previewColumns = computed(() => [
  { key: 'models', label: t('admin.agentPricing.model') },
  { key: 'retail_price', label: t('admin.agentPricing.retailPrice') },
  { key: 'agent_price', label: t('admin.agentPricing.agentPrice') },
  { key: 'margin', label: t('admin.agentPricing.margin') },
  { key: 'warning', label: t('admin.agentPricing.warning') }
])

const planOptions = computed(() =>
  plans.value.map(p => ({ value: p.id, label: p.name }))
)

const channelOptions = computed(() =>
  channels.value.map(c => ({ value: c.id, label: c.name }))
)

// 目标渠道选项里剔掉来源渠道：让手滑在选项层面就不可能发生，
// 而不是等提交后被后端拒绝。
const targetChannelOptions = computed(() =>
  channels.value
    .filter(c => c.id !== form.sourceChannelId)
    .map(c => ({ value: c.id, label: c.name }))
)

const targetEqualsSource = computed(
  () => form.targetChannelId > 0 && form.targetChannelId === form.sourceChannelId
)

const targetChannelName = computed(
  () => channels.value.find(c => c.id === form.targetChannelId)?.name ?? ''
)

const canPreview = computed(() => form.planId > 0 && form.sourceChannelId > 0)
const canApply = computed(
  () => !!preview.value && form.targetChannelId > 0 && !targetEqualsSource.value
)

function formatDiscount(v: number): string {
  // 0.8 → 8 折。运营说的是「折」，不是「0.8 倍」。
  return t('admin.agentPricing.discountValue', { n: Number((v * 10).toFixed(2)) })
}

function trimNumber(v: number): string {
  return Number(v.toPrecision(10)).toString()
}

/** 代理的利润空间占零售价的比例。运营最关心这个数。 */
function marginPercent(row: AgentPriceChange): number | null {
  if (!row.retail_price) return null
  return ((row.retail_price - row.agent_price) / row.retail_price) * 100
}

function marginText(row: AgentPriceChange): string {
  const m = marginPercent(row)
  if (m === null) return '—'
  return `${m.toFixed(1)}%`
}

// 退回零售价的条目利润是 0，要和正常条目区分开，否则运营会以为代理有得赚。
function marginClass(row: AgentPriceChange): string {
  const m = marginPercent(row)
  if (m === null || row.skipped) return 'text-amber-600 dark:text-amber-400'
  if (m < 10) return 'text-orange-600 dark:text-orange-400'
  return 'text-gray-700 dark:text-gray-200'
}

async function loadPlans() {
  plansLoading.value = true
  plansError.value = ''
  try {
    plans.value = await listAgentPricingPlans()
    if (!form.planId && plans.value.length > 0) form.planId = plans.value[0].id
  } catch {
    plansError.value = t('admin.agentPricing.loadPlansFailed')
  } finally {
    plansLoading.value = false
  }
}

async function loadChannels() {
  try {
    const result = await listChannels(1, 100)
    channels.value = result.items ?? []
    if (!form.sourceChannelId && channels.value.length > 0) {
      form.sourceChannelId = channels.value[0].id
    }
  } catch {
    genError.value = t('admin.agentPricing.loadChannelsFailed')
  }
}

async function runPreview() {
  if (!canPreview.value || previewing.value) return
  previewing.value = true
  genError.value = ''
  applyResult.value = null
  try {
    const result = await previewAgentPricing({
      plan_id: form.planId,
      source_channel_id: form.sourceChannelId
    })
    // DataTable 要一个稳定 row-key；一行定价会展开成多个字段和多个层级，
    // 单用模型名会重复。
    preview.value = {
      ...result,
      changes: result.changes.map((c, i) => ({ ...c, rowKey: `${i}` }) as AgentPriceChange)
    }
  } catch (e) {
    preview.value = null
    genError.value = extractError(e, t('admin.agentPricing.previewFailed'))
  } finally {
    previewing.value = false
  }
}

async function runApply() {
  if (!canApply.value || applying.value) return
  applying.value = true
  genError.value = ''
  try {
    applyResult.value = await applyAgentPricing({
      plan_id: form.planId,
      source_channel_id: form.sourceChannelId,
      target_channel_id: form.targetChannelId
    })
    confirmOpen.value = false
  } catch (e) {
    genError.value = extractError(e, t('admin.agentPricing.applyFailed'))
  } finally {
    applying.value = false
  }
}

function openPlanEditor(row: AgentPricingPlan | null) {
  editingPlan.value = row
  planForm.name = row?.name ?? ''
  planForm.text_discount = row?.text_discount ?? 0.8
  planForm.multimodal_deduction_cny = row?.multimodal_deduction_cny ?? 0.1
  planForm.enforce_cost_floor = row?.enforce_cost_floor ?? true
  planError.value = ''
  planEditorOpen.value = true
}

async function savePlan() {
  if (planSaving.value) return
  planSaving.value = true
  planError.value = ''
  try {
    const payload = {
      name: planForm.name,
      text_discount: planForm.text_discount,
      multimodal_deduction_cny: planForm.multimodal_deduction_cny,
      enforce_cost_floor: planForm.enforce_cost_floor
    }
    if (editingPlan.value) {
      await updateAgentPricingPlan(editingPlan.value.id, payload)
    } else {
      await createAgentPricingPlan(payload)
    }
    planEditorOpen.value = false
    await loadPlans()
  } catch (e) {
    planError.value = extractError(e, t('admin.agentPricing.savePlanFailed'))
  } finally {
    planSaving.value = false
  }
}

// 后端对折扣越界、汇率取不到这些情况给了具体文案，直接透出来比
// 统一显示「操作失败」有用得多。
function extractError(e: unknown, fallback: string): string {
  return extractApiErrorMessage(e) || fallback
}

onMounted(() => {
  void loadPlans()
  void loadChannels()
})
</script>
