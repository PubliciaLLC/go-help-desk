import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { getDashboard } from './dashboard'

const body = { queues: [], attention: null, volume: null }

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('getDashboard', () => {
  const cases = [
    { name: 'sends the time zone', tz: 'Europe/London', params: { tz: 'Europe/London' } },
    { name: 'sends nothing when there is none', tz: undefined, params: undefined },
    { name: 'sends nothing for an empty one', tz: '', params: undefined },
  ]
  it.each(cases)('$name', async ({ tz, params }) => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: body })
    expect(await getDashboard(tz)).toEqual(body)
    expect(get).toHaveBeenCalledWith('/dashboard', { params })
  })
})
