import { api } from './client'
import type { KpiResponse, Priority } from './types'

// The query of GET /reports/kpi. `from` and `to` go together; both absent is
// the server's last 30 days. Every field the server reads is named here, and
// nothing else is sent.
export interface KpiParams {
  tz: string
  from?: string
  to?: string
  category_id?: string
  priority?: Priority[]
  assignee_group_id?: string
}

// The Reports page's numbers. A repeated priority is a repeated key
// (priority=high&priority=low), which is what the API reads.
export async function getKpi(params: KpiParams): Promise<KpiResponse> {
  const res = await api.get<KpiResponse>('/reports/kpi', {
    params,
    paramsSerializer: { indexes: null },
  })
  return res.data
}
