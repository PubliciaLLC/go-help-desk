import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { getKpi, type KpiParams } from './reports'

const body = { tz: 'UTC' }

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('getKpi', () => {
  const cases: Array<{ name: string; params: KpiParams }> = [
    { name: 'a preset range', params: { tz: 'Europe/London', from: '2026-10-26', to: '2026-11-24' } },
    {
      name: 'every filter, with repeated priorities',
      params: {
        tz: 'America/New_York',
        from: '2026-11-01',
        to: '2026-11-01',
        category_id: 'c1',
        priority: ['critical', 'low'],
        assignee_group_id: 'g1',
      },
    },
  ]

  it.each(cases)('sends $name as the query and returns the body', async ({ params }) => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: body })
    expect(await getKpi(params)).toEqual(body)
    // Arrays are a repeated key (priority=critical&priority=low), as the list sends them.
    expect(get).toHaveBeenCalledWith('/reports/kpi', {
      params,
      paramsSerializer: { indexes: null },
    })
  })
})
