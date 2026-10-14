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
