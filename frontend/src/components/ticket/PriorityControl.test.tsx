import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
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
