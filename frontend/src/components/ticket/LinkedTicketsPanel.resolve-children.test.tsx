import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import { renderWithQuery } from '@/test/render'
import { LinkedTicketsPanel } from './LinkedTicketsPanel'
import * as ticketsApi from '@/api/tickets'
import * as adminApi from '@/api/admin'
import type { Ticket, TicketLink, LinkType } from '@/api/types'

// #443: "Resolve all children" on a parent ticket. Client-side, one child at a
// time through the ordinary Resolve, so the closed-ticket rules, the per-ticket
// audit and notifications all apply. A child that is already Resolved or
// Closed is skipped, never re-resolved: a second Resolve with different notes
// overwrites the child's notes (#225).

vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, params, children, ...rest }: { to: string; params: Record<string, string>; children: React.ReactNode }) => (
    <a href={to.replace('$id', params.id)} {...rest}>
      {children}
    </a>
  ),
}))

const PARENT = 'tkt-parent'

function ticket(id: string, statusId: string): Ticket {
  return {
    id,
    tracking_number: `GHD-${id}`,
    subject: `Subject of ${id}`,
    description: '',
    category_id: 'cat-1',
    priority: 'medium',
    status_id: statusId,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

function link(source: string, target: string, type: LinkType = 'parent_child'): TicketLink {
  return { source_id: source, target_id: target, link_type: type }
}

function refusal(status: number, code: string, message: string) {
  return new AxiosError('failed', String(status), undefined, undefined, {
    status,
    data: { error: { code, message } },
    statusText: '',
    headers: {},
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    config: {} as any,
  })
}

// What the server would say about each ticket right now.
function serve(links: TicketLink[], tickets: Ticket[]) {
  vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue(links)
  vi.spyOn(ticketsApi, 'getTicket').mockImplementation(async (id: string) => {
    const t = tickets.find((x) => x.id === id)
    if (!t) throw refusal(404, 'not_found', 'ticket not found')
    return t
  })
}

beforeEach(() => {
  vi.restoreAllMocks()
  vi.spyOn(adminApi, 'listStatuses').mockResolvedValue([
    { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#3b82f6', active: true, ticket_count: 0 },
    { id: 'st-resolved', name: 'Resolved', kind: 'system', sort_order: 2, color: '#4caf50', active: true, ticket_count: 0 },
    { id: 'st-closed', name: 'Closed', kind: 'system', sort_order: 3, color: '#999', active: true, ticket_count: 0 },
  ])
})

async function openForm(notes?: string) {
  await userEvent.click(await screen.findByRole('button', { name: 'Resolve all children' }))
  if (notes !== undefined) {
    await userEvent.type(screen.getByLabelText('Resolution notes for all children'), notes)
  }
}

const confirm = () => screen.getByRole('button', { name: /^Resolve \d+ child/ })

describe('Resolve all children: when it is offered', () => {
  const cases: Array<{ name: string; links: TicketLink[]; shown: boolean }> = [
    { name: 'a parent_child link where this ticket is the source (the parent)', links: [link(PARENT, 'c1')], shown: true },
    { name: 'no links', links: [], shown: false },
    { name: 'only a related_to link', links: [link(PARENT, 'c1', 'related_to')], shown: false },
    { name: 'only a duplicate_of link it is the source of', links: [link(PARENT, 'c1', 'duplicate_of')], shown: false },
    { name: 'a parent_child link where this ticket is the target (it is the child)', links: [link('c1', PARENT)], shown: false },
  ]
  cases.forEach(({ name, links, shown }) => {
    it(`${shown ? 'shows' : 'hides'} it for ${name}`, async () => {
      serve(links, [ticket('c1', 'st-new')])
      renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

      await waitFor(() => expect(ticketsApi.listLinks).toHaveBeenCalled())
      if (shown) {
        expect(await screen.findByRole('button', { name: 'Resolve all children' })).toBeDefined()
      } else {
        // Let the list render first, so an absent button is not just "not yet".
        await screen.findByText('Linked Tickets')
        await waitFor(() => expect(ticketsApi.getTicket).toHaveBeenCalledTimes(links.length))
        expect(screen.queryByRole('button', { name: 'Resolve all children' })).toBeNull()
      }
    })
  })
})

describe('Resolve all children: the notes', () => {
  it('asks for resolution notes once and will not go ahead without them', async () => {
    serve([link(PARENT, 'c1'), link(PARENT, 'c2')], [ticket('c1', 'st-new'), ticket('c2', 'st-new')])
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockResolvedValue(ticket('c1', 'st-resolved'))
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm()
    expect((confirm() as HTMLButtonElement).disabled).toBe(true)

    await userEvent.type(screen.getByLabelText('Resolution notes for all children'), '   ')
    expect((confirm() as HTMLButtonElement).disabled).toBe(true)
    expect(resolve).not.toHaveBeenCalled()
  })
})

describe('Resolve all children: the loop', () => {
  it('resolves each child with the same notes, one request at a time', async () => {
    serve(
      [link(PARENT, 'c1'), link(PARENT, 'c2'), link(PARENT, 'c3')],
      [ticket('c1', 'st-new'), ticket('c2', 'st-new'), ticket('c3', 'st-new')],
    )
    let inFlight = 0
    let maxInFlight = 0
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockImplementation(async (id: string) => {
      inFlight++
      maxInFlight = Math.max(maxInFlight, inFlight)
      await new Promise((r) => setTimeout(r, 5))
      inFlight--
      return ticket(id, 'st-resolved')
    })
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('Fixed upstream')
    await userEvent.click(confirm())

    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(3))
    expect(resolve.mock.calls.map((c) => c[0]).sort()).toEqual(['c1', 'c2', 'c3'])
    expect(resolve.mock.calls.every((c) => c[1] === 'Fixed upstream')).toBe(true)
    expect(maxInFlight).toBe(1)
    expect((await screen.findByRole('status')).textContent).toMatch(/3 of 3 resolved/)
  })

  it('touches only the children: not the parent, not related or duplicate links', async () => {
    serve(
      [link(PARENT, 'c1'), link('gp', PARENT), link(PARENT, 'rel', 'related_to'), link(PARENT, 'dup', 'duplicate_of')],
      [ticket('c1', 'st-new'), ticket('gp', 'st-new'), ticket('rel', 'st-new'), ticket('dup', 'st-new')],
    )
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockResolvedValue(ticket('c1', 'st-resolved'))
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('done')
    await userEvent.click(confirm())

    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(1))
    expect(resolve).toHaveBeenCalledWith('c1', 'done')
  })

  it('skips children that are already Resolved or Closed, and says so', async () => {
    serve(
      [link(PARENT, 'c1'), link(PARENT, 'c2'), link(PARENT, 'c3')],
      [ticket('c1', 'st-new'), ticket('c2', 'st-resolved'), ticket('c3', 'st-closed')],
    )
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockResolvedValue(ticket('c1', 'st-resolved'))
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('done')
    await userEvent.click(confirm())

    const report = await screen.findByRole('status')
    expect(report.textContent).toMatch(/1 of 3 resolved, 2 already resolved/)
    // A second Resolve would overwrite the notes those two already carry.
    expect(resolve).toHaveBeenCalledTimes(1)
    expect(resolve).toHaveBeenCalledWith('c1', 'done')
  })

  it('reports a partial failure with the server\'s reason per child, and carries on past it', async () => {
    serve(
      [link(PARENT, 'c1'), link(PARENT, 'c2'), link(PARENT, 'c3'), link(PARENT, 'c4')],
      [ticket('c1', 'st-new'), ticket('c2', 'st-new'), ticket('c3', 'st-new'), ticket('c4', 'st-resolved')],
    )
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockImplementation(async (id: string) => {
      if (id === 'c2') throw refusal(403, 'forbidden', 'ticket is outside your scope')
      return ticket(id, 'st-resolved')
    })
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('done')
    await userEvent.click(confirm())

    const report = await screen.findByRole('alert')
    expect(report.textContent).toMatch(/2 of 4 resolved, 1 already resolved, 1 refused/)
    const refused = within(report).getByRole('listitem')
    expect(refused.textContent).toContain('GHD-c2')
    expect(refused.textContent).toContain('ticket is outside your scope')
    // c3 comes after the refused c2 and is still resolved.
    expect(resolve.mock.calls.map((c) => c[0])).toEqual(['c1', 'c2', 'c3'])
  })

  it('counts a child it cannot read as refused, with the server\'s reason, and does not resolve it', async () => {
    serve([link(PARENT, 'c1'), link(PARENT, 'gone')], [ticket('c1', 'st-new')])
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockResolvedValue(ticket('c1', 'st-resolved'))
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('done')
    await userEvent.click(confirm())

    const report = await screen.findByRole('alert')
    expect(report.textContent).toMatch(/1 of 2 resolved, 1 refused/)
    expect(within(report).getByRole('listitem').textContent).toContain('ticket not found')
    expect(resolve).toHaveBeenCalledTimes(1)
  })
})

// Like the server's GET /tickets/{id}/links: the links that involve the ticket.
function serveByTicket(links: TicketLink[], tickets: Ticket[], opts: { noLinks?: string[] } = {}) {
  vi.spyOn(ticketsApi, 'listLinks').mockImplementation(async (id: string) => {
    if (opts.noLinks?.includes(id)) throw refusal(403, 'forbidden', 'not yours')
    return links.filter((l) => l.source_id === id || l.target_id === id)
  })
  vi.spyOn(ticketsApi, 'getTicket').mockImplementation(async (id: string) => {
    const t = tickets.find((x) => x.id === id)
    if (!t) throw refusal(404, 'not_found', 'ticket not found')
    return t
  })
}

describe('Resolve all children: the button counts only the open children', () => {
  it('counts the children that are not Resolved or Closed, and says how many are skipped', async () => {
    serveByTicket(
      [link(PARENT, 'c1'), link(PARENT, 'c2'), link(PARENT, 'c3'), link(PARENT, 'c4')],
      [ticket('c1', 'st-new'), ticket('c2', 'st-resolved'), ticket('c3', 'st-closed'), ticket('c4', 'st-new')],
    )
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm()

    // 4 children, 2 of them already done: "Resolve 4 children" would be wrong.
    expect(await screen.findByRole('button', { name: 'Resolve 2 children' })).toBeDefined()
    expect(screen.getByText(/2 open children will be resolved \(2 already resolved or closed are skipped\)/)).toBeDefined()
  })

  it('uses the singular for one open child', async () => {
    serveByTicket([link(PARENT, 'c1'), link(PARENT, 'c2')], [ticket('c1', 'st-new'), ticket('c2', 'st-closed')])
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm()

    expect(await screen.findByRole('button', { name: 'Resolve 1 child' })).toBeDefined()
  })

  it('is disabled, with the reason, when every child is already Resolved or Closed', async () => {
    serveByTicket([link(PARENT, 'c1'), link(PARENT, 'c2')], [ticket('c1', 'st-resolved'), ticket('c2', 'st-closed')])
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockResolvedValue(ticket('c1', 'st-resolved'))
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('notes that would be sent')

    expect(await screen.findByText('All children are already resolved.')).toBeDefined()
    await waitFor(() => {
      expect((screen.getByRole('button', { name: 'Resolve 0 children' }) as HTMLButtonElement).disabled).toBe(true)
    })
    expect(resolve).not.toHaveBeenCalled()
  })
})

describe('Resolve all children: children that have children of their own', () => {
  const grandchildLinks = [link(PARENT, 'c1'), link(PARENT, 'c2'), link(PARENT, 'c3'), link('c1', 'g1'), link('c3', 'g2')]
  const tickets = () => ['c1', 'c2', 'c3', 'g1', 'g2'].map((id) => ticket(id, 'st-new'))
  const linkReads = () => vi.mocked(ticketsApi.listLinks).mock.calls.map((c) => c[0]).filter((id) => id !== PARENT)

  it('says how many have children, and which, once the form is open; nothing below them is resolved', async () => {
    serveByTicket(grandchildLinks, tickets())
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockResolvedValue(ticket('c1', 'st-resolved'))
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('done')

    const notice = await screen.findByText(/2 of these children have children of their own/)
    expect(notice.textContent).toContain('GHD-c1')
    expect(notice.textContent).toContain('GHD-c3')
    expect(notice.textContent).not.toContain('GHD-c2')
    expect(notice.textContent).toMatch(/resolve those from their own ticket/)

    await userEvent.click(screen.getByRole('button', { name: 'Resolve 3 children' }))
    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(3))
    expect(resolve.mock.calls.map((c) => c[0]).sort()).toEqual(['c1', 'c2', 'c3'])
  })

  it('is not shown when none of the children has children', async () => {
    serveByTicket([link(PARENT, 'c1'), link(PARENT, 'c2')], [ticket('c1', 'st-new'), ticket('c2', 'st-new')])
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm()
    await screen.findByRole('button', { name: 'Resolve 2 children' })
    await waitFor(() => expect(linkReads().sort()).toEqual(['c1', 'c2']))

    expect(screen.queryByText(/children of their own/)).toBeNull()
  })

  it('does not count a child that is only a parent through a related_to or a link pointing up', async () => {
    serveByTicket(
      [link(PARENT, 'c1'), link('c1', 'x', 'related_to'), link('y', 'c1')],
      [ticket('c1', 'st-new'), ticket('x', 'st-new'), ticket('y', 'st-new')],
    )
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm()
    await waitFor(() => expect(linkReads()).toEqual(['c1']))

    expect(screen.queryByText(/children of their own/)).toBeNull()
  })

  it('reads nothing about the children\'s own links until the form is opened, and one read per child at most', async () => {
    serveByTicket(grandchildLinks, tickets())
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await screen.findByRole('button', { name: 'Resolve all children' })
    expect(linkReads()).toEqual([])

    await userEvent.click(screen.getByRole('button', { name: 'Resolve all children' }))
    await screen.findByText(/2 of these children have children of their own/)
    expect(linkReads().sort()).toEqual(['c1', 'c2', 'c3'])
  })

  it('names the tickets only up to five, otherwise just the count', async () => {
    const ids = ['c1', 'c2', 'c3', 'c4', 'c5', 'c6']
    serveByTicket(
      [...ids.map((id) => link(PARENT, id)), ...ids.map((id) => link(id, `g-${id}`))],
      ids.map((id) => ticket(id, 'st-new')),
    )
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm()

    const notice = await screen.findByText(/6 of these children have children of their own/)
    expect(notice.textContent).not.toContain('GHD-c1')
  })

  it('does not break when a child\'s links cannot be read: no notice for that child, the rest works', async () => {
    serveByTicket(grandchildLinks, tickets(), { noLinks: ['c1'] })
    const resolve = vi.spyOn(ticketsApi, 'resolveTicket').mockResolvedValue(ticket('c1', 'st-resolved'))
    renderWithQuery(<LinkedTicketsPanel ticketId={PARENT} />)

    await openForm('done')

    // c1 is unknown, so it is not named; c3 still is.
    const notice = await screen.findByText(/1 of these children has children of their own/)
    expect(notice.textContent).toContain('GHD-c3')
    expect(notice.textContent).not.toContain('GHD-c1')
    await userEvent.click(screen.getByRole('button', { name: 'Resolve 3 children' }))
    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(3))
  })
})
