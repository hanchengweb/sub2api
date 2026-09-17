import { apiClient } from './client'

/**
 * 代理身份与代理批发价。
 *
 * 与 api/agency.ts 分开：那边是「申请合作」（任何登录用户都能提交），
 * 这边是「已经是代理之后」的身份和价格。
 */

export type AgentMode = 'affiliate' | 'reseller'
export type AgentStatus = 'active' | 'suspended' | 'terminated'

export interface AgentProfile {
  user_id: number
  application_id?: number
  mode: AgentMode
  direction: string
  status: AgentStatus
  pricing_plan_id?: number
  reseller_group_id?: number
  note?: string
  activated_at: string
  created_at: string
  updated_at: string
  /** 只有管理员列表会返回。后台光有 user_id 的话运营认不出谁是谁。 */
  email?: string
}

/** 代理看自己的身份。不是代理时后端回 404 NOT_AN_AGENT。 */
export interface MyAgentProfile {
  mode: AgentMode
  direction: string
  status: AgentStatus
  pricing_plan_id?: number
  reseller_group_id?: number
  activated_at: string
}

export interface AgentPricingPlan {
  id: number
  name: string
  /** 文本类折扣，代理价 = 零售价 × 本值。0.8 = 八折。 */
  text_discount: number
  /** 多模态每次让利（人民币元）。0.1 = 每次让一毛。 */
  multimodal_deduction_cny: number
  /** 开启后低于成本价的条目退回零售价，不套用方案。 */
  enforce_cost_floor: boolean
  status: 'active' | 'archived'
  created_at: string
  updated_at: string
}

export interface AgentPriceChange {
  platform: string
  models: string[]
  billing_mode: string
  category: 'text' | 'multimodal'
  field: string
  /** 非空表示来自分层定价（如 1K·高），空表示行级默认价。 */
  tier_label?: string
  retail_price: number
  agent_price: number
  cost_price?: number
  warning?: string
  /** 为真时该条退回零售价写入，不套用方案。 */
  skipped: boolean
}

export interface AgentPricingPreview {
  plan_id: number
  plan_name: string
  /** 本次换算用的积分汇率（每元多少积分）。必须让运营核对。 */
  credits_per_cny: number
  /** 换算后的每次让利积分数。 */
  deduction_credits: number
  changes: AgentPriceChange[]
  total_rows: number
  skipped_rows: number
  warning_rows: number
}

export interface AgentPricingApplyResult {
  preview: AgentPricingPreview
  target_channel: string
  written_pricings: number
  /** 非空时要显眼展示，比如目标渠道还没挂分组。 */
  warning: string
}

/** 代理名下的一个客户。邮箱由后端脱敏。 */
export interface AgentCustomer {
  user_id: number
  email: string
  status: string
  bound_at: string
  total_cost_credits: number
  request_count: number
  last_active_at?: string
}

/**
 * 客户的一条用量记录（代理视角）。
 *
 * 后端刻意不返回 prompt 与 token 明细——代理该知道客户花了多少钱在什么模型上，
 * 不该看到客户具体问了什么。
 */
export interface AgentCustomerUsage {
  id: number
  customer_id: number
  customer_email: string
  model: string
  billing_mode: string
  actual_cost: number
  image_count: number
  created_at: string
}

/** 一次返现结算。规则字段是结算当时的快照，不是当前方案值。 */
export interface AgentSettlement {
  id: number
  agent_user_id: number
  from_usage_log_id: number
  to_usage_log_id: number
  period_start: string
  period_end: string
  text_cost_credits: number
  text_rebate_credits: number
  multimodal_units: number
  multimodal_rebate_credits: number
  total_rebate_credits: number
  text_discount: number
  multimodal_deduction_cny: number
  credits_per_cny: number
  customer_count: number
  status: string
  created_at: string
}

/** 代理查看自己的档案。不是代理时抛 404，调用方据此显示申请入口。 */
export async function getMyAgentProfile(): Promise<MyAgentProfile> {
  const { data } = await apiClient.get<{ data: MyAgentProfile }>('/agent/profile')
  return data.data
}

/**
 * 以下三个接口的作用域根都取自登录态，不接受代理 id 参数。
 * 后端 requireActiveAgent 会挡住非代理和已停用的代理。
 */
export async function listMyCustomers(params: { limit?: number; offset?: number } = {}): Promise<{
  items: AgentCustomer[]
  total: number
}> {
  const { data } = await apiClient.get<{ data: AgentCustomer[]; total: number }>('/agent/customers', {
    params
  })
  return { items: data.data ?? [], total: data.total ?? 0 }
}

export async function listMyCustomerUsage(params: {
  customer_id?: number
  model?: string
  limit?: number
  offset?: number
} = {}): Promise<{ items: AgentCustomerUsage[]; total: number }> {
  const { data } = await apiClient.get<{ data: AgentCustomerUsage[]; total: number }>(
    '/agent/customers/usage',
    { params: { ...params, customer_id: params.customer_id || undefined, model: params.model || undefined } }
  )
  return { items: data.data ?? [], total: data.total ?? 0 }
}

export async function listMySettlements(params: { limit?: number; offset?: number } = {}): Promise<{
  items: AgentSettlement[]
  total: number
}> {
  const { data } = await apiClient.get<{ data: AgentSettlement[]; total: number }>(
    '/agent/settlements',
    { params }
  )
  return { items: data.data ?? [], total: data.total ?? 0 }
}

/** 管理员手动给某个代理结算一次。返回 null 表示上次结算后没有新消费。 */
export async function settleAgent(userId: number): Promise<AgentSettlement | null> {
  const { data } = await apiClient.post<{ data: AgentSettlement | null }>(
    `/admin/agents/${userId}/settle`
  )
  return data.data ?? null
}

export async function listAgentSettlements(
  userId: number,
  params: { limit?: number; offset?: number } = {}
): Promise<{ items: AgentSettlement[]; total: number }> {
  const { data } = await apiClient.get<{ data: AgentSettlement[]; total: number }>(
    `/admin/agents/${userId}/settlements`,
    { params }
  )
  return { items: data.data ?? [], total: data.total ?? 0 }
}

export interface AdminAgentListResult {
  items: AgentProfile[]
  total: number
}

export async function listAgents(params: {
  status?: string
  mode?: string
  limit?: number
  offset?: number
}): Promise<AdminAgentListResult> {
  const { data } = await apiClient.get<{ data: AgentProfile[]; total: number }>('/admin/agents', {
    params: {
      status: params.status || undefined,
      mode: params.mode || undefined,
      limit: params.limit,
      offset: params.offset
    }
  })
  return { items: data.data ?? [], total: data.total ?? 0 }
}

export async function updateAgentStatus(
  userId: number,
  payload: { status: AgentStatus; note: string }
): Promise<void> {
  await apiClient.put(`/admin/agents/${userId}/status`, payload)
}

/** 两个字段都允许传 null：把代理从批发价上摘下来是正常运营动作。 */
export async function updateAgentPricing(
  userId: number,
  payload: { pricing_plan_id: number | null; reseller_group_id: number | null }
): Promise<void> {
  await apiClient.put(`/admin/agents/${userId}/pricing`, payload)
}

export async function listAgentPricingPlans(): Promise<AgentPricingPlan[]> {
  const { data } = await apiClient.get<{ data: AgentPricingPlan[] }>('/admin/agent-pricing-plans')
  return data.data ?? []
}

export type AgentPricingPlanPayload = Pick<
  AgentPricingPlan,
  'name' | 'text_discount' | 'multimodal_deduction_cny' | 'enforce_cost_floor'
> & { status?: 'active' | 'archived' }

export async function createAgentPricingPlan(
  payload: AgentPricingPlanPayload
): Promise<AgentPricingPlan> {
  const { data } = await apiClient.post<{ data: AgentPricingPlan }>(
    '/admin/agent-pricing-plans',
    payload
  )
  return data.data
}

export async function updateAgentPricingPlan(
  id: number,
  payload: AgentPricingPlanPayload
): Promise<AgentPricingPlan> {
  const { data } = await apiClient.put<{ data: AgentPricingPlan }>(
    `/admin/agent-pricing-plans/${id}`,
    payload
  )
  return data.data
}

/** 只算不落库。配错价要等到用户被扣了错的钱才会发现，所以先看 diff。 */
export async function previewAgentPricing(payload: {
  plan_id: number
  source_channel_id: number
}): Promise<AgentPricingPreview> {
  const { data } = await apiClient.post<{ data: AgentPricingPreview }>(
    '/admin/agent-pricing-plans/preview',
    payload
  )
  return data.data
}

/**
 * 落库到目标渠道。
 *
 * 目标渠道必须不同于来源渠道——填成同一个会把零售价原地改成代理价。
 * 后端会硬挡（TARGET_EQUALS_SOURCE），前端也别让它走到那一步。
 */
export async function applyAgentPricing(payload: {
  plan_id: number
  source_channel_id: number
  target_channel_id: number
}): Promise<AgentPricingApplyResult> {
  const { data } = await apiClient.post<{ data: AgentPricingApplyResult }>(
    '/admin/agent-pricing-plans/apply',
    payload
  )
  return data.data
}

export default {
  getMyAgentProfile,
  listMyCustomers,
  listMyCustomerUsage,
  listMySettlements,
  settleAgent,
  listAgentSettlements,
  listAgents,
  updateAgentStatus,
  updateAgentPricing,
  listAgentPricingPlans,
  createAgentPricingPlan,
  updateAgentPricingPlan,
  previewAgentPricing,
  applyAgentPricing
}
