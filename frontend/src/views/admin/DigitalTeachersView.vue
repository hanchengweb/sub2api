<template>
  <AppLayout>
    <div class="space-y-6 p-4 sm:p-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-gray-500">{{ configured ? '服务已连接' : '服务尚未配置，请联系运维配置 LiveAvatar 连接' }}</p>
        <button class="btn btn-secondary" :disabled="busy" @click="run(refresh)">刷新</button>
      </div>
      <p v-if="message" role="status" class="rounded-lg bg-gray-100 p-3 text-sm dark:bg-dark-700">{{ message }}</p>
      <section class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
        <h2 class="mb-4 font-semibold">算力包</h2>
        <form class="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" @submit.prevent="run(savePlan)">
          <label><span class="mb-1 block text-sm">选择算力包</span><Select v-model="groupId" :options="groupOptions" searchable @update:model-value="selectPlan" /></label>
          <label><span class="mb-1 block text-sm">每分钟积分</span><input v-model.number="plan.price_per_minute" class="input w-full" type="number" min="0" step="0.0001" required /><span class="mt-1 block text-xs text-gray-500">设为 0 时，仅使用管理员明确发放的分钟额度。</span></label>
          <label><span class="mb-1 block text-sm">每次最长分钟数</span><input v-model.number="plan.max_minutes" class="input w-full" type="number" min="1" max="30" required /></label>
          <label class="flex items-center gap-2"><input v-model="plan.enabled" type="checkbox" />开放数字老师</label>
          <label class="flex items-center gap-2"><input v-model="plan.sandbox_enabled" type="checkbox" />允许 1 分钟免费连接检查</label>
          <div><button class="btn btn-primary" :disabled="busy || !groupId" type="submit">保存算力包设置</button></div>
        </form>
      </section>
      <div class="grid gap-6 lg:grid-cols-2">
        <section class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <h2 class="mb-4 font-semibold">发放分钟额度</h2>
          <form class="space-y-3" @submit.prevent="run(grantMinutes)">
            <label class="block"><span class="mb-1 block text-sm">用户 ID</span><input v-model.number="grant.user_id" type="number" min="1" required class="input w-full" /><span class="mt-1 block text-xs text-gray-500">可从用户管理中查看账户 ID；额度只属于该账户。</span></label>
            <label class="block"><span class="mb-1 block text-sm">增加分钟数</span><input v-model.number="grant.minutes" type="number" min="0.000001" step="0.000001" required class="input w-full" /></label>
            <label class="block"><span class="mb-1 block text-sm">发放原因</span><input v-model="grant.reason" maxlength="500" required class="input w-full" /></label>
            <div class="flex flex-wrap gap-2"><button class="btn btn-primary" :disabled="busy" type="submit">发放额度</button><button type="button" class="btn btn-secondary" :disabled="busy || !grant.user_id" @click="run(readMinutes)">查看当前额度</button></div>
            <p v-if="minuteStatus" class="text-sm text-gray-500">{{ minuteStatus }}</p>
          </form>
        </section>
        <section class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <h2 class="mb-2 font-semibold">绑定定制形象</h2><p class="mb-4 text-sm text-gray-500">在服务控制台用老师素材制作形象，再绑定给指定账户。只有该账户能看到和使用。</p>
          <form class="space-y-3" @submit.prevent="run(bindAsset)">
            <label class="block"><span class="mb-1 block text-sm">用户 ID</span><input v-model.number="asset.user_id" type="number" min="1" required class="input w-full" /></label>
            <label class="block"><span class="mb-1 block text-sm">素材类型</span><Select v-model="asset.kind" :options="[{value:'avatar',label:'老师形象'},{value:'voice',label:'定制声音'}]" /></label>
            <label class="block"><span class="mb-1 block text-sm">素材编号</span><input v-model="asset.asset_id" required class="input w-full" placeholder="填写服务控制台中的素材 ID" /></label>
            <div class="flex flex-wrap items-center gap-3"><button class="btn btn-primary" :disabled="busy" type="submit">绑定到账户</button><a class="text-sm text-primary-600" href="https://app.liveavatar.com" target="_blank" rel="noopener noreferrer">打开制作控制台 ↗</a></div>
          </form>
        </section>
      </div>
      <section class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
        <h2 class="mb-4 font-semibold">最近会话</h2>
        <DataTable :columns="columns" :data="sessions" :loading="busy" row-key="id">
          <template #cell-created_at="{row}">{{ new Date(row.created_at).toLocaleString() }}</template>
          <template #cell-state="{row}">{{ stateName(row.state) }}{{ row.sandbox ? ' · 免费检查' : '' }}</template>
          <template #cell-charged_credits="{row}">{{ row.charged_credits == null ? '待结算' : Number(row.charged_credits).toFixed(4) }}</template>
          <template #cell-duration_seconds="{row}">{{ row.duration_seconds == null ? '—' : Number(row.duration_seconds).toFixed(0) + ' 秒' }}</template>
          <template #cell-actions="{row}"><button v-if="!row.settled_at" class="btn btn-secondary btn-sm" :disabled="busy" @click="run(() => reconcile(row.id))">结束并核对</button><span v-else class="text-sm text-gray-500">{{ row.error_code ? '上游用量需复核' : '已结算' }}</span></template>
        </DataTable>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Select from '@/components/common/Select.vue'
import { apiClient } from '@/api/client'

type Plan = { group_id:number; enabled:boolean; sandbox_enabled:boolean; price_per_minute:number; max_minutes:number; version:number }
type Session = { id:string; user_id:number; key_id:number; state:string; created_at:string; settled_at:string|null; sandbox:boolean; charged_credits:number|null; duration_seconds:number|null; error_code?:string }
const configured=ref(false),busy=ref(false),message=ref(''),groupId=ref<number|string>(''),groups=ref<{id:number;name:string}[]>([]),plans=ref<Plan[]>([]),sessions=ref<Session[]>([]),minuteStatus=ref('')
const plan=reactive<Plan>({group_id:0,enabled:false,sandbox_enabled:false,price_per_minute:0,max_minutes:2,version:0})
const grant=reactive({user_id:0,minutes:1,reason:''}),asset=reactive({user_id:0,kind:'avatar',asset_id:''})
let pendingGrant:{digest:string;id:string}|null=null
const groupOptions=computed(()=>groups.value.map(g=>({value:g.id,label:g.name})))
const columns=[{key:'created_at',label:'开始时间'},{key:'user_id',label:'用户 ID'},{key:'state',label:'状态'},{key:'duration_seconds',label:'时长'},{key:'charged_credits',label:'扣除积分'},{key:'actions',label:'操作'}]
const root='/admin/digital-teachers'
function stateName(state:string){return ({preparing:'准备中',ready:'等待连接',starting:'正在连接',running:'对话中',stopping:'正在结束',settling:'结算中',completed:'已完成',failed:'未开始',review:'待核对'} as Record<string,string>)[state]||state}
async function run(fn:()=>Promise<void>){if(busy.value)return;busy.value=true;message.value='';try{await fn()}catch(e:unknown){message.value=(e as {message?:string}).message||'操作未确认，请刷新后核对'}finally{busy.value=false}}
async function refresh(){const [status,g,s]=await Promise.all([apiClient.get(root),apiClient.get('/admin/groups/all'),apiClient.get(root+'/sessions')]);configured.value=status.data.provider_configured;plans.value=status.data.plans;groups.value=g.data;sessions.value=s.data;if(groupId.value)selectPlan()}
function selectPlan(){Object.assign(plan,plans.value.find(p=>p.group_id===Number(groupId.value))||{group_id:Number(groupId.value),enabled:false,sandbox_enabled:false,price_per_minute:0,max_minutes:2,version:0})}
async function savePlan(){await apiClient.put(root+'/plans',{...plan,group_id:Number(groupId.value)});await refresh();message.value='算力包设置已保存，新的会话使用此配置。'}
async function readMinutes(){const {data:m}=await apiClient.get(root+'/minutes/'+grant.user_id);minuteStatus.value=m.configured ? `已发放 ${m.granted} 分钟 · 已用 ${m.consumed} · 预留 ${m.reserved} · 可用 ${m.available}`:'尚未发放分钟额度'}
async function grantMinutes(){const digest=JSON.stringify(grant);if(!pendingGrant||pendingGrant.digest!==digest)pendingGrant={digest,id:crypto.randomUUID()};await apiClient.post(root+'/minutes',{...grant,request_id:pendingGrant.id});await readMinutes();message.value='分钟额度已发放。';pendingGrant=null;grant.reason=''}
async function bindAsset(){await apiClient.post(root+'/assets',asset);message.value='素材已绑定，用户刷新形象目录后即可选择。'}
async function reconcile(id:string){await apiClient.post(root+'/sessions/'+id+'/reconcile');await refresh();message.value='已请求结束并核对会话，结算结果以列表状态为准。'}
onMounted(()=>run(refresh))
</script>
