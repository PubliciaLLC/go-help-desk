import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'

// #349: a closed ticket is an archive. The guest's link still reads it, and
// the page must not offer a reply box that could only fail.

const { getGuestTicket, createGuestFollowUp } = vi.hoisted(() => ({
  getGuestTicket: vi.fn(),
  createGuestFollowUp: vi.fn(),
}))
vi.mock('@/api/guest', async (orig) => ({
  ...(await orig<typeof import('@/api/guest')>()),
  getGuestTicket,
  createGuestFollowUp,
}))

import { GuestTicketViewPage } from './GuestTicketViewPage'

function ticket(status: string) {
  return {
    tracking_number: 'GHD-2026-000042',
    subject: 'Printer jammed',
    description: 'again',
    status,
    created_at: '2026-09-20T10:00:00Z',
    updated_at: '2026-09-20T10:00:00Z',
    replies: [{ id: 'r1', body: 'Replaced the drum.', created_at: '2026-09-21T10:00:00Z', from_you: false }],
  }
}

beforeEach(() => {
  getGuestTicket.mockReset()
  createGuestFollowUp.mockReset()
  window.location.hash = '#some-token'
})

describe('the guest view of a ticket', () => {
  it('shows a closed ticket, including its last reply, and no reply box', async () => {
    getGuestTicket.mockResolvedValue(ticket('Closed'))
    renderWithQuery(<GuestTicketViewPage />)

    await waitFor(() => expect(document.body.textContent).toContain('Replaced the drum.'))
    expect(screen.queryByRole('textbox', { name: 'Add a reply' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Send reply' })).toBeNull()
    expect(screen.getByRole('note').textContent).toMatch(/closed/i)
    expect(screen.getByRole('note').textContent).toMatch(/follow-up/i)
  })

  it('still offers a reply box on an open ticket', async () => {
    getGuestTicket.mockResolvedValue(ticket('In Progress'))
    renderWithQuery(<GuestTicketViewPage />)

    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Add a reply' })).not.toBeNull())
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('does not tell a visitor with a dead link that closing killed it', async () => {
    const { GuestLinkInvalid } = await import('@/api/guest')
    getGuestTicket.mockRejectedValue(new GuestLinkInvalid())
    renderWithQuery(<GuestTicketViewPage />)

    await waitFor(() => expect(document.body.textContent).toContain('This link no longer works'))
    expect(document.body.textContent).not.toMatch(/once a ticket is closed/i)
    expect(document.body.textContent).toMatch(/30 days/)
  })

  it('offers a follow-up on a closed ticket and says a link was emailed, without showing one', async () => {
    getGuestTicket.mockResolvedValue(ticket('Closed'))
    createGuestFollowUp.mockResolvedValue('GHD-2026-000099')
    renderWithQuery(<GuestTicketViewPage />)
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Create follow-up' })).not.toBeNull())

    await userEvent.click(screen.getByRole('button', { name: 'Create follow-up' }))

    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('GHD-2026-000099'))
    expect(screen.getByRole('status').textContent).toMatch(/emailed you a link/i)
    expect(createGuestFollowUp).toHaveBeenCalledWith('some-token')
    expect(screen.queryByRole('button', { name: 'Create follow-up' })).toBeNull()
  })

  it('says it could not, without saying why, on any refusal', async () => {
    const { GuestFollowUpRefused } = await import('@/api/guest')
    getGuestTicket.mockResolvedValue(ticket('Closed'))
    createGuestFollowUp.mockRejectedValue(new GuestFollowUpRefused())
    renderWithQuery(<GuestTicketViewPage />)
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Create follow-up' })).not.toBeNull())

    await userEvent.click(screen.getByRole('button', { name: 'Create follow-up' }))

    await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/could not create a follow-up/i))
  })

  it('offers no follow-up on an open ticket', async () => {
    getGuestTicket.mockResolvedValue(ticket('In Progress'))
    renderWithQuery(<GuestTicketViewPage />)
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Add a reply' })).not.toBeNull())
    expect(screen.queryByRole('button', { name: 'Create follow-up' })).toBeNull()
  })
})
