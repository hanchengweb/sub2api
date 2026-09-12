import { apiClient } from './client'

/**
 * 成为代理 · 合作申请。
 *
 * 这个页面原本是纯前端：表单只在本地拼一段文案让用户自己复制走，
 * 提交按钮不发请求——申请进不到平台，没人知道有人申请过。
 */

export type AgencyDirection = 'channel' | 'integration' | 'delivery'
export type AgencyStatus = 'pending' | 'contacted' | 'accepted' | 'rejected'

export interface AgencyApplication {
  id: number
  direction: AgencyDirection
  contact_name: string
  email: string
  company: string
  scenario: string
  status: AgencyStatus
  admin_note?: string
  created_at: string
  updated_at: string
}

export interface SubmitAgencyPayload {
  direction: AgencyDirection
  contact_name: string
  email: string
  company: string
  scenario: string
}

/** 提交申请。待处理达上限时后端回 409 TOO_MANY_PENDING，由调用方区分提示。 */
export async function submitAgencyApplication(payload: SubmitAgencyPayload): Promise<AgencyApplication> {
  const { data } = await apiClient.post<{ data: AgencyApplication }>('/agency/applications', payload)
  return data.data
}

/** 我提交过的申请。提交完什么都看不到的话，用户只会反复再提交一遍。 */
export async function listMyAgencyApplications(): Promise<AgencyApplication[]> {
  const { data } = await apiClient.get<{ data: AgencyApplication[] }>('/agency/applications/mine')
  return data.data ?? []
}

export default { submitAgencyApplication, listMyAgencyApplications }
