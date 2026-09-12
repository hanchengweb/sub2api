import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get, put }
}))

import { listAgencyApplications, updateAgencyApplication } from '@/api/agency'

describe('admin agency applications api', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
  })

  it('omits the status param when the filter is "全部"', async () => {
    get.mockResolvedValue({ data: { data: [], total: 0 } })

    await listAgencyApplications({ status: '', limit: 20, offset: 0 })

    expect(get).toHaveBeenCalledWith('/admin/agency-applications', {
      params: { status: undefined, limit: 20, offset: 0 }
    })
  })

  it('passes a concrete status through', async () => {
    get.mockResolvedValue({ data: { data: [], total: 0 } })

    await listAgencyApplications({ status: 'pending', limit: 10, offset: 30 })

    expect(get).toHaveBeenCalledWith('/admin/agency-applications', {
      params: { status: 'pending', limit: 10, offset: 30 }
    })
  })

  // 后端只在有数据时带 data/total；缺字段时列表页不能崩成白屏。
  it('falls back to an empty page when the response has no data/total', async () => {
    get.mockResolvedValue({ data: {} })

    const result = await listAgencyApplications({})

    expect(result).toEqual({ items: [], total: 0 })
  })

  it('returns items and total as given', async () => {
    const row = {
      id: 7,
      user_id: 3,
      direction: 'channel',
      contact_name: '韩诚',
      email: 'a@b.com',
      company: '',
      scenario: '想做渠道',
      status: 'pending',
      created_at: '2026-09-11T00:00:00Z',
      updated_at: '2026-09-11T00:00:00Z'
    }
    get.mockResolvedValue({ data: { data: [row], total: 41 } })

    const result = await listAgencyApplications({ limit: 20, offset: 0 })

    expect(result.items).toEqual([row])
    expect(result.total).toBe(41)
  })

  it('puts the status update to the row-scoped url', async () => {
    put.mockResolvedValue({ data: null })

    await updateAgencyApplication(12, { status: 'contacted', admin_note: '已电话联系' })

    expect(put).toHaveBeenCalledWith('/admin/agency-applications/12', {
      status: 'contacted',
      admin_note: '已电话联系'
    })
  })
})
