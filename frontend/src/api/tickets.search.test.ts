import { describe, expect, it, vi, beforeEach } from 'vitest'
import type { AxiosAdapter, InternalAxiosRequestConfig } from 'axios'
import { api } from './client'
import { lookupRequesterById, searchTickets } from './tickets'

// The request the page makes, as it goes on the wire. The adapter replaces the
// network and records the URL axios built; a spy on api.get would show the
// params object and miss how it is written out.
function wire(response: { data?: unknown; status?: number; headers?: Record<string, string> } = {}) {
  const seen: string[] = []
  const adapter: AxiosAdapter = async (config: InternalAxiosRequestConfig) => {
    seen.push(api.getUri(config))
    const status = response.status ?? 200
    const res = {
      data: 'data' in response ? response.data : [],
      status,
      statusText: '',
      headers: response.headers ?? {},
      config,
    }
    if (status >= 400) throw Object.assign(new Error('refused'), { isAxiosError: true, response: res })
    return res
  }
  api.defaults.adapter = adapter
  return seen
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('searchTickets', () => {
  it('writes a repeated filter as a repeated key, not status_id[]=', async () => {
    const seen = wire()
    await searchTickets({ status_id: ['a', 'b'], priority: ['high', 'low'], limit: 50, offset: 0 })

    const q = new URL(seen[0], 'http://x').searchParams
    expect(q.getAll('status_id')).toEqual(['a', 'b'])
    expect(q.getAll('priority')).toEqual(['high', 'low'])
    expect(seen[0]).not.toContain('%5B')
  })

  it('leaves an unset parameter out of the address', async () => {
    const seen = wire()
    await searchTickets({ q: undefined, scope: 'mine', limit: 50, offset: 0 })
    expect(new URL(seen[0], 'http://x').searchParams.has('q')).toBe(false)
  })

  it('returns the tickets, with an empty list for a null body', async () => {
    wire({ data: null })
    await expect(searchTickets({ limit: 50, offset: 0 })).resolves.toMatchObject({ tickets: [] })
  })

  it('a plain answer has nothing to say about the reply search', async () => {
    wire()
    await expect(searchTickets({ limit: 50, offset: 0 })).resolves.toEqual({
      tickets: [],
      repliesSearched: true,
      replyIndexReady: true,
    })
  })

  // The wire shape of these two is the one thing this PR could not read from
  // the backend (the REST handler is being built in parallel). They are read in
  // one place, here, so reconciling them is a change to this function and its
  // test only.
  it('reads "replies were not fully searched" from X-Replies-Searched', async () => {
    wire({ headers: { 'x-replies-searched': 'false' } })
    await expect(searchTickets({ limit: 50, offset: 0 })).resolves.toMatchObject({ repliesSearched: false })
  })

  it('reads "the reply index is not ready" from X-Reply-Index', async () => {
    wire({ headers: { 'x-reply-index': 'building' } })
    await expect(searchTickets({ limit: 50, offset: 0 })).resolves.toMatchObject({ replyIndexReady: false })
  })
})

describe('lookupRequesterById', () => {
  it('asks for the one account and returns the object', async () => {
    const seen = wire({ data: { id: 'u1', display_name: 'Dana Whitfield', email: 'dana@example.com' } })
    await expect(lookupRequesterById('u1')).resolves.toEqual({ id: 'u1', display_name: 'Dana Whitfield', email: 'dana@example.com' })
    expect(seen[0]).toContain('/users/lookup?id=u1')
  })

  // The server answers 404 alike for an id that names nobody and for a
  // disabled, deleted or service account, on purpose. The page cannot tell them
  // apart either, so it is one answer: null.
  it('is null for a 404', async () => {
    wire({ status: 404, data: { error: { code: 'not_found', message: 'not found' } } })
    await expect(lookupRequesterById('u1')).resolves.toBeNull()
  })
})
