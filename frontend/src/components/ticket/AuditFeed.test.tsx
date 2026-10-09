import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { AuditFeed } from './AuditFeed'
import * as ticketsApi from '@/api/tickets'
import type { TicketAuditEntry } from '@/api/types'

vi.mock('@/api/tickets', async () => {
  const actual = await vi.importActual<typeof import('@/api/tickets')>('@/api/tickets')
  return { ...actual, listTicketAudit: vi.fn() }
})

const entry = (overrides: Partial<TicketAuditEntry>): TicketAuditEntry => ({
  id: 'entry-1',
  action: 'created',
  actor_id: 'user-1',
  actor_name: 'Staff Member',
  created_at: '2026-01-01T12:00:00Z',
  ...overrides,
})

describe('AuditFeed', () => {
  it('shows a friendly label and the actor for each entry', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({ id: 'e1', action: 'created', actor_name: 'Staff Member' }),
      entry({ id: 'e2', action: 'closed', actor_name: 'Admin User' }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Ticket filed')).toBeTruthy())
    expect(screen.getByText('Closed')).toBeTruthy()
    expect(screen.getByText(/Staff Member/)).toBeTruthy()
    expect(screen.getByText(/Admin User/)).toBeTruthy()
  })

  it('falls back to a readable guess for an action it does not know', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({ action: 'duplicate_of_marked', actor_name: undefined }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Duplicate of marked')).toBeTruthy())
  })

  it('does not say "by" when there is no actor (a system-generated entry)', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({ actor_id: null, actor_name: undefined }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Ticket filed')).toBeTruthy())
    expect(screen.queryByText(/ by /)).toBeNull()
  })

  it('shows an empty state rather than a blank card', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Nothing recorded yet')).toBeTruthy())
  })

  it('shows an error state rather than looking like an empty feed', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockRejectedValue(new Error('network error'))

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Could not load activity')).toBeTruthy())
    expect(screen.queryByText('Nothing recorded yet')).toBeNull()
  })

  it('shows the field-level diff when the server sends one', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({
        action: 'resolved',
        before: { status_id: 'a', priority: 'low' },
        after: { status_id: 'b', priority: 'low' },
      }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    expect(screen.getByText(/status_id/)).toBeTruthy()
    expect(screen.getByText(/a → b/)).toBeTruthy()
    // priority did not change, so it must not appear as a diff line — only
    // what actually changed belongs here, not every field the snapshot holds.
    expect(screen.queryByText(/priority/)).toBeNull()
  })

  it('shows nothing extra when the server withholds the diff', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({ action: 'resolved', before: undefined, after: undefined }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    expect(screen.queryByText(/→/)).toBeNull()
  })

  it('does not show a diff for a create action, whose before is null', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({ action: 'created', before: null, after: { status_id: 'a' } }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getByText('Ticket filed')).toBeTruthy())
    expect(screen.queryByText(/→/)).toBeNull()
  })

  it('requests the audit feed for the ticket it was given', () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([])

    renderWithQuery(<AuditFeed ticketId="tkt-42" />)

    expect(ticketsApi.listTicketAudit).toHaveBeenCalledWith('tkt-42')
  })

  it('shows a masked requester distinctly from an account named "Requester"', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({ id: 'e1', action: 'created', actor_name: 'Requester', actor_masked: true }),
      entry({ id: 'e2', action: 'closed', actor_name: 'Requester' }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getAllByText('Requester (name hidden)')).toHaveLength(1))
    expect(screen.getAllByText('Requester')).toHaveLength(1)
  })

  // #382: the label is text a display name can copy, so the marker is an icon,
  // and there is no hover-only tooltip a keyboard or touch user cannot reach.
  it('marks a masked requester with an icon a display name cannot copy', async () => {
    vi.mocked(ticketsApi.listTicketAudit).mockResolvedValue([
      entry({ id: 'e1', action: 'created', actor_name: 'Requester', actor_masked: true }),
      entry({ id: 'e2', action: 'closed', actor_name: 'Requester (name hidden)' }),
    ])

    renderWithQuery(<AuditFeed ticketId="tkt-1" />)

    await waitFor(() => expect(screen.getAllByText('Requester (name hidden)')).toHaveLength(2))
    const [masked, named] = screen.getAllByText('Requester (name hidden)')
    expect(masked.querySelector('svg')).not.toBeNull()
    // Decorative: the words already say it, and a screen reader should not
    // hear the icon announce itself as well.
    expect(masked.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
    expect(masked.getAttribute('title')).toBeNull()
    expect(named.querySelector('svg')).toBeNull()
  })
})
