<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-5xl space-y-6 p-4 sm:p-6">
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
        <!-- 身份 -->
        <section class="rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-600 dark:bg-dark-800">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t(`agentPanel.modes.${profile.mode}.title`) }}</h2>
              <p class="mt-1 max-w-xl text-sm text-gray-500 dark:text-gray-400">
                {{ t(`agentPanel.modes.${profile.mode}.hint`) }}
              </p>
            </div>
            <span class="inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium" :class="statusClass">
              {{ t(`agentPanel.statuses.${profile.status}`) }}
            </span>
          </div>

          <dl class="mt-5 grid gap-4 border-t border-gray-100 pt-4 sm:grid-cols-3 dark:border-dark-700">
            <div>
              <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.direction') }}</dt>
              <dd class="mt-0.5 text-sm text-gray-900 dark:text-white">{{ t(`agency.directions.${profile.direction}.title`) }}</dd>
            </div>
            <div>
              <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.activatedAt') }}</dt>
              <dd class="mt-0.5 text-sm text-gray-900 dark:text-white">{{ formatDateTime(profile.activated_at) }}</dd>
            </div>
          </dl>

          <!-- 停用状态必须说清楚后果，否则代理只会看到一个灰标签，不知道自己不能干什么 -->
          <p
            v-if="profile.status !== 'active'"
            class="mt-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-500/10 dark:text-amber-300"
            role="alert"
          >
            {{ t(`agentPanel.statusHint.${profile.status}`) }}
          </p>
        </section>

        <!-- 分佣模式：邀请码 + 客户 -->
        <template v-if="profile.mode === 'affiliate'">
          <section class="rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-600 dark:bg-dark-800">
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.inviteTitle') }}</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('agentPanel.inviteHint') }}</p>

            <div v-if="affiliate" class="mt-4 space-y-4">
              <div class="flex flex-wrap items-center gap-2">
                <code class="rounded-md bg-gray-100 px-3 py-1.5 font-mono text-sm dark:bg-dark-700">{{ affiliate.aff_code }}</code>
                <button type="button" class="btn btn-secondary btn-sm" @click="copyCode">
                  <Icon name="copy" size="sm" />{{ t('common.copy') }}
                </button>
              </div>

              <div class="flex flex-wrap items-center gap-2">
                <input :value="inviteLink" readonly class="input min-w-0 flex-1 font-mono text-xs" />
                <button type="button" class="btn btn-secondary btn-sm" @click="copyLink">
                  {{ t('common.copy') }}
                </button>
              </div>

              <dl class="grid gap-4 border-t border-gray-100 pt-4 sm:grid-cols-3 dark:border-dark-700">
                <div>
                  <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.customerCount') }}</dt>
                  <dd class="mt-0.5 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">{{ affiliate.aff_count }}</dd>
                </div>
                <div>
                  <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.rebateRate') }}</dt>
                  <dd class="mt-0.5 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">
                    {{ affiliate.effective_rebate_rate_percent }}%
                  </dd>
                </div>
                <div>
                  <dt class="text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.availableQuota') }}</dt>
                  <dd class="mt-0.5 text-lg font-semibold tabular-nums text-gray-900 dark:text-white">
                    {{ affiliate.aff_quota }} {{ t('common.creditUnit') }}
                  </dd>
                </div>
              </dl>
            </div>
            <p v-else-if="affiliateError" class="mt-4 text-sm text-red-500" role="alert">{{ affiliateError }}</p>
          </section>

          <section class="rounded-lg border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800">
            <header class="border-b border-gray-200 px-5 py-3 dark:border-dark-600">
              <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.customersTitle') }}</h3>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('agentPanel.customersHint') }}</p>
            </header>
            <DataTable :columns="customerColumns" :data="customers" :loading="false" row-key="user_id">
              <template #cell-email="{ row }">
                <span class="text-sm text-gray-900 dark:text-white">{{ row.email }}</span>
              </template>
              <template #cell-created_at="{ row }">
                <span class="text-sm text-gray-500 dark:text-gray-400">{{ formatDateTime(row.created_at) }}</span>
              </template>
            </DataTable>
          </section>
        </template>

        <!-- 转售模式 -->
        <section v-else class="rounded-lg border border-gray-200 bg-white p-5 dark:border-dark-600 dark:bg-dark-800">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('agentPanel.resellerTitle') }}</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('agentPanel.resellerHint') }}</p>
          <!--
            刻意不展示批发价目表：那是平台的成本底线，摊开给代理看等于把
            议价空间一起给出去了。代理需要的是「我的价已经配好了」这个确认。
          -->
          <p
            v-if="!profile.reseller_group_id"
            class="mt-4 rounded-md bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:bg-amber-500/10 dark:text-amber-300"
            role="alert"
          >
            {{ t('agentPanel.resellerPending') }}
          </p>
          <p v-else class="mt-4 rounded-md bg-green-50 px-3 py-2 text-sm text-green-800 dark:bg-green-500/10 dark:text-green-300">
            {{ t('agentPanel.resellerReady') }}
          </p>
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
import Icon from '@/components/icons/Icon.vue'
import { getMyAgentProfile, type MyAgentProfile } from '@/api/agent'
import userAPI from '@/api/user'
import type { UserAffiliateDetail } from '@/types'
import { useClipboard } from '@/composables/useClipboard'

const { t } = useI18n()
// copyToClipboard 自己会弹成功提示，不用再维护一份「已复制」文案状态。
const { copyToClipboard } = useClipboard()

const loading = ref(true)
const profile = ref<MyAgentProfile | null>(null)
const affiliate = ref<UserAffiliateDetail | null>(null)
const affiliateError = ref('')

const customers = computed(() => affiliate.value?.invitees ?? [])

const customerColumns = computed(() => [
  { key: 'email', label: t('agentPanel.customerEmail') },
  { key: 'created_at', label: t('agentPanel.joinedAt') }
])

const inviteLink = computed(() => {
  if (!affiliate.value) return ''
  return `${window.location.origin}/register?aff=${affiliate.value.aff_code}`
})

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

// invitee 的 created_at 是可选的，缺失时留空而不是渲染成 Invalid Date。
function formatDateTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString()
}

async function copyCode() {
  if (!affiliate.value) return
  await copyToClipboard(affiliate.value.aff_code)
}

async function copyLink() {
  await copyToClipboard(inviteLink.value)
}

onMounted(async () => {
  try {
    profile.value = await getMyAgentProfile()
  } catch {
    // 后端对「不是代理」回 404，这里就是那条路径，不是错误。
    profile.value = null
  } finally {
    loading.value = false
  }

  // 只有分佣代理用得上邀请码；转售代理拉这个只会拿到一个他永远不用的码。
  if (profile.value?.mode === 'affiliate') {
    try {
      affiliate.value = await userAPI.getAffiliateDetail()
    } catch {
      affiliateError.value = t('agentPanel.inviteLoadFailed')
    }
  }
})
</script>
