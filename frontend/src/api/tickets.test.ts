import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { addReply, listTickets, updateTicket } from './tickets'

// tickets.ts is mostly thin wrappers, and those are deliberately not tested —
// asserting that `api.get` gets called proves nothing. What is covered here is
// the small amount of real behaviour: the empty-list guard, the camelCase to
// snake_case payload mapping, and the null-versus-undefined distinction that
// decides whether a field is cleared or left alone.

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('listTickets', () => {
  // The Go handlers return JSON null for an empty result in places rather than
  // []. Without the `?? []` guard the caller does data.map(...) on null and the
  // page throws, so this is load-bearing rather than defensive habit.
  it('returns an empty array when the server sends null', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: null })
    await expect(listTickets()).resolves.toEqual([])
  })

  it('returns an empty array when the server sends an empty array', async () => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: [] })
    await expect(listTickets()).resolves.toEqual([])
  })

  it('passes filters through as query params', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: [] })
    await listTickets({ scope: 'mine', q: 'printer' })

    expect(get).toHaveBeenCalledWith('/tickets', { params: { scope: 'mine', q: 'printer' } })
  })

  it('sends no params object contents when called with nothing', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: [] })
    await listTickets()

    expect(get).toHaveBeenCalledWith('/tickets', { params: undefined })
  })
})

describe('addReply', () => {
  // The defaults decide whether a customer is emailed and whether a note is
  // visible to them. Both are easy to invert by accident and neither fails
  // loudly: a wrong `internal` default leaks staff notes to the reporter.
  it('defaults to a public reply that notifies the customer', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} })
    await addReply('t-1', 'Have you tried restarting it?')

    expect(post).toHaveBeenCalledWith('/tickets/t-1/replies', {
      body: 'Have you tried restarting it?',
      internal: false,
      notify_customer: true,
    })
  })

  it('maps notifyCustomer to notify_customer', async () => {
    // The server reads notify_customer. A camelCase key would be ignored
    // silently and the server default would apply instead.
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} })
    await addReply('t-1', 'Internal note', true, false)

    const [, payload] = post.mock.calls[0]
    expect(payload).toHaveProperty('notify_customer', false)
    expect(payload).not.toHaveProperty('notifyCustomer')
    expect(payload).toHaveProperty('internal', true)
  })
})

describe('updateTicket', () => {
  // type_id and item_id are `string | null`: null clears the field, undefined
  // leaves it alone. If the client dropped nulls, clearing a ticket's type
  // would appear to work and silently do nothing.
  it('preserves an explicit null so a field can be cleared', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })
    await updateTicket('t-1', { type_id: null, item_id: null })

    const [, payload] = patch.mock.calls[0]
    expect(payload).toHaveProperty('type_id', null)
    expect(payload).toHaveProperty('item_id', null)
  })

  it('sends only the fields it was given', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })
    await updateTicket('t-1', { status_id: 's-9' })

    expect(patch).toHaveBeenCalledWith('/tickets/t-1', { status_id: 's-9' })
  })

  it('targets the ticket by id in the path', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })
    await updateTicket('abc-123', { status_id: 's-1' })

    expect(patch.mock.calls[0][0]).toBe('/tickets/abc-123')
  })
})
