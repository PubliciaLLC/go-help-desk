import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import { useQuery } from '@tanstack/react-query'
import { renderWithQuery } from '@/test/render'
import { PriorityControl } from './PriorityControl'
import * as ticketsApi from '@/api/tickets'
import type { Ticket } from '@/api/types'

// #443: staff and admin change a ticket's priority from the ticket page. The
// server re-matches the SLA policy and refuses a Resolved or Closed ticket
// (409 ticket_closed), so the control is shown disabled, with the reason, there.

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

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('PriorityControl', () => {
  it('lists the four priorities with the current one selected', () => {
    renderWithQuery(<PriorityControl ticketId="t1" priority="high" />)

    const select = screen.getByLabelText('Ticket priority') as HTMLSelectElement
    expect(select.value).toBe('high')
    expect(Array.from(select.options).map((o) => o.value)).toEqual(['critical', 'high', 'medium', 'low'])
    expect(select.disabled).toBe(false)
  })

  it('sends PATCH {priority} and refreshes the ticket, its audit feed and the lists', async () => {
    const update = vi.spyOn(ticketsApi, 'updateTicket').mockResolvedValue({} as Ticket)
    const { queryClient } = renderWithQuery(<PriorityControl ticketId="t1" priority="high" />)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await userEvent.selectOptions(screen.getByLabelText('Ticket priority'), 'critical')

    await waitFor(() => expect(update).toHaveBeenCalledTimes(1))
    expect(update).toHaveBeenCalledWith('t1', { priority: 'critical' })
    await waitFor(() => {
      const keys = invalidate.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey))
      expect(keys).toContain(JSON.stringify(['ticket', 't1']))
      expect(keys).toContain(JSON.stringify(['ticketAudit', 't1']))
      expect(keys).toContain(JSON.stringify(['tickets']))
    })
  })

  it('shows the server\'s message on a refusal and keeps the old value', async () => {
    vi.spyOn(ticketsApi, 'updateTicket').mockRejectedValue(
      refusal(403, 'forbidden', 'only staff can change priority'),
    )
    renderWithQuery(<PriorityControl ticketId="t1" priority="high" />)

    await userEvent.selectOptions(screen.getByLabelText('Ticket priority'), 'low')

    expect((await screen.findByRole('alert')).textContent).toBe('only staff can change priority')
    expect((screen.getByLabelText('Ticket priority') as HTMLSelectElement).value).toBe('high')
  })

  it('is disabled with an explanation on a Resolved or Closed ticket, and sends nothing', async () => {
    const update = vi.spyOn(ticketsApi, 'updateTicket').mockResolvedValue({} as Ticket)
    renderWithQuery(<PriorityControl ticketId="t1" priority="high" locked />)

    const select = screen.getByLabelText('Ticket priority') as HTMLSelectElement
    expect(select.disabled).toBe(true)
    expect(screen.getByText(/priority cannot be changed on a resolved or closed ticket/i)).toBeDefined()
    await userEvent.selectOptions(select, 'low').catch(() => {})
    expect(update).not.toHaveBeenCalled()
  })
})

// The page feeds the control from the ticket query. After a successful change
// the select must keep the chosen value on screen until that query has
// reloaded; it used to fall back to the old value for the length of the
// refetch, which read as the change having failed.
function Harness() {
  const { data } = useQuery({ queryKey: ['ticket', 't1'], queryFn: () => ticketsApi.getTicket('t1') })
  if (!data) return null
  return <PriorityControl ticketId="t1" priority={data.priority} />
}

const select = () => screen.getByLabelText('Ticket priority') as HTMLSelectElement

describe('PriorityControl while the ticket reloads', () => {
  it('keeps the chosen value on screen until the refetch finishes', async () => {
    let finishRefetch: (t: Ticket) => void = () => {}
    vi.spyOn(ticketsApi, 'getTicket')
      .mockResolvedValueOnce({ id: 't1', priority: 'high' } as Ticket)
      .mockImplementationOnce(() => new Promise<Ticket>((resolve) => { finishRefetch = resolve }))
    vi.spyOn(ticketsApi, 'updateTicket').mockResolvedValue({} as Ticket)
    renderWithQuery(<Harness />)
    await screen.findByLabelText('Ticket priority')

    await userEvent.selectOptions(select(), 'critical')

    // The refetch has started and has not answered: the old value must not be back.
    await waitFor(() => expect(ticketsApi.getTicket).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(ticketsApi.updateTicket).toHaveBeenCalledTimes(1))
    expect(select().value).toBe('critical')

    finishRefetch({ id: 't1', priority: 'critical' } as Ticket)
    await waitFor(() => expect(select().value).toBe('critical'))
    // And it is the server's value now, so a later change from elsewhere shows.
    await waitFor(() => expect(select().disabled).toBe(false))
  })

  it('shows what the server says if the reload disagrees with the change', async () => {
    vi.spyOn(ticketsApi, 'getTicket')
      .mockResolvedValueOnce({ id: 't1', priority: 'high' } as Ticket)
      .mockResolvedValue({ id: 't1', priority: 'low' } as Ticket)
    vi.spyOn(ticketsApi, 'updateTicket').mockResolvedValue({} as Ticket)
    renderWithQuery(<Harness />)
    await screen.findByLabelText('Ticket priority')

    await userEvent.selectOptions(select(), 'critical')

    await waitFor(() => expect(select().value).toBe('low'))
  })

  it('goes back to the old value and shows the message when the change is refused', async () => {
    vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue({ id: 't1', priority: 'high' } as Ticket)
    vi.spyOn(ticketsApi, 'updateTicket').mockRejectedValue(
      refusal(409, 'ticket_closed', 'a resolved or closed ticket\'s priority cannot be changed'),
    )
    renderWithQuery(<Harness />)
    await screen.findByLabelText('Ticket priority')

    await userEvent.selectOptions(select(), 'low')

    expect((await screen.findByRole('alert')).textContent).toContain('cannot be changed')
    expect(select().value).toBe('high')
  })
})
