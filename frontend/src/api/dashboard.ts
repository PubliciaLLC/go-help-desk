import { api } from './client'
import type { Dashboard } from './types'

// The strip, the needs-attention list and the 30-day trend in one request.
// `tz` is the zone the trend's days are counted in; absent is UTC.
export async function getDashboard(tz?: string): Promise<Dashboard> {
  const res = await api.get<Dashboard>('/dashboard', { params: tz ? { tz } : undefined })
  return res.data
}
