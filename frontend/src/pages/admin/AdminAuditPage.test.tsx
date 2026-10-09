import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { AdminAuditEntry, AdminAuditListResponse } from '@/api/types'

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/audit' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { AdminAuditPage } from './AdminAuditPage'

function asAdmin() {
  useAuthStore.setState({
    user: {
      id: 'admin-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function asStaff() {
  useAuthStore.setState({
    user: {
      id: 'staff-1', email: 'staff@example.com', display_name: 'Sam Staff', role: 'staff',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

function entry(overrides: Partial<AdminAuditEntry> = {}): AdminAuditEntry {
  return {
    id: 'e-1', entity_type: 'ticket', entity_id: 't-1', action: 'resolved',
    actor_id: 'u-1', actor_name: 'Sam Staff', created_at: '2026-01-01T12:00:00Z',
    ...overrides,
  }
}

// total_capped is always sent by the server; most tests do not care about it.
type ListFixture = Omit<AdminAuditListResponse, 'total_capped'> & { total_capped?: boolean }

function mockList({ total_capped = false, ...rest }: ListFixture) {
  const response: AdminAuditListResponse = { ...rest, total_capped }
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/audit') return Promise.resolve({ data: response })
    if (url === '/staff') return Promise.resolve({ data: [] })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  asAdmin()
})

describe('AdminAuditPage', () => {
  it('renders what the server sends', async () => {
    mockList({ entries: [entry()], total: 1, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    const row = screen.getByText('Resolved').closest('tr')!
    expect(within(row).getByText('ticket')).toBeTruthy()
    expect(within(row).getByText('Sam Staff')).toBeTruthy()
  })

  it('shows an entity-type filter for admin', async () => {
    mockList({ entries: [], total: 0, has_more: false })
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Audit Log' })).toBeTruthy())

    expect(screen.getByLabelText(/entity type/i)).toBeTruthy()
  })

  // Staff are forced to entity_type=ticket server-side no matter what the
  // query string says — the control would let a staff member pick "user"
  // and see an unexplained empty page, so it is not offered at all.
  it('does not offer an entity-type filter to staff', async () => {
    asStaff()
    mockList({ entries: [], total: 0, has_more: false })
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Audit Log' })).toBeTruthy())

    expect(screen.queryByLabelText(/entity type/i)).toBeNull()
  })

  it('sends an edited action filter to the server', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/audit') return Promise.resolve({ data: { entries: [], total: 0, total_capped: false, has_more: false } })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Audit Log' })).toBeTruthy())

    await userEvent.type(screen.getByLabelText(/^action$/i), 'resolved')

    await waitFor(() => {
      const call = get.mock.calls.find((c) => {
        const params = (c[1] as { params?: Record<string, unknown> } | undefined)?.params
        return c[0] === '/admin/audit' && params?.action === 'resolved'
      })
      expect(call, 'expected a request with action=resolved').toBeTruthy()
    })
  })

  it('shows the diff inline when the server sends one', async () => {
    mockList({
      entries: [entry({ before: { status_id: 'a' }, after: { status_id: 'b' } })],
      total: 1,
      has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    expect(screen.getByText(/status_id/)).toBeTruthy()
    expect(screen.getByText(/a → b/)).toBeTruthy()
  })

  it('shows an empty state rather than a blank table', async () => {
    mockList({ entries: [], total: 0, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText(/nothing matches/i)).toBeTruthy())
  })

  it('disables Previous on the first page and Next when there is nothing more', async () => {
    mockList({ entries: [entry()], total: 1, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /previous/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /previous/i }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('enables Next from has_more, not from the count', async () => {
    mockList({ entries: [entry()], total: 2, has_more: true })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(false)
  })

  // Staff are counted by the same predicate that picks their page, so the
  // total is what they can see and the range reads the same as it does for an
  // admin.
  it('shows the range and total for staff', async () => {
    asStaff()
    mockList({ entries: [entry()], total: 7, has_more: true })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 7')).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(false)
  })

  // The pager runs on has_more. The count is for display, and a page that has
  // already said it is the last must not offer a Next off the back of it.
  it('does not enable Next from a count when has_more is false', async () => {
    mockList({ entries: [entry()], total: 999, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  // #331: the server stops counting at 10,000 and says so. A capped total is a
  // floor, not a count, so it reads "10,000+"; an exact one never gets the plus,
  // including one that happens to equal the cap.
  it('shows a capped total as n+', async () => {
    mockList({ entries: [entry()], total: 10000, total_capped: true, has_more: true })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 10,000+')).toBeTruthy())
  })

  it('shows an uncapped total of exactly the cap without a plus', async () => {
    mockList({ entries: [entry()], total: 10000, total_capped: false, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 10,000')).toBeTruthy())
  })

  it('keeps paging on has_more when the total is capped', async () => {
    const seen: number[] = []
    vi.spyOn(api, 'get').mockImplementation(((url: string, config?: { params?: { offset?: number } }) => {
      if (url !== '/admin/audit') return Promise.resolve({ data: [] })
      const offset = config?.params?.offset ?? 0
      seen.push(offset)
      return Promise.resolve({
        data: { entries: [entry({ id: `e-${offset}` })], total: 10000, total_capped: true, has_more: true },
      })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    renderWithQuery(<AdminAuditPage />)

    const next = () => screen.getByRole('button', { name: /next/i }) as HTMLButtonElement
    await waitFor(() => expect(screen.getByText('1–1 of 10,000+')).toBeTruthy())
    await userEvent.click(next())
    await waitFor(() => expect(screen.getByText('51–51 of 10,000+')).toBeTruthy())
    expect(seen).toContain(50)
    expect(next().disabled).toBe(false)
    expect((screen.getByRole('button', { name: /previous/i }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('does not offer Next on the last page of a capped total', async () => {
    mockList({ entries: [entry()], total: 10000, total_capped: true, has_more: false })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 10,000+')).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('shows a capped total to staff the same way', async () => {
    asStaff()
    mockList({ entries: [entry()], total: 10000, total_capped: true, has_more: true })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('1–1 of 10,000+')).toBeTruthy())
  })

  it('shows a masked requester distinctly from an account named "Requester"', async () => {
    mockList({
      entries: [
        entry({ id: 'm', action: 'created', actor_name: 'Requester', actor_masked: true }),
        entry({ id: 'r', action: 'resolved', actor_name: 'Requester' }),
      ],
      total: 2,
      has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Created')).toBeTruthy())
    const masked = screen.getByText('Created').closest('tr')!
    expect(within(masked).getByText('Requester (name hidden)')).toBeTruthy()

    const named = screen.getByText('Resolved').closest('tr')!
    expect(within(named).getByText('Requester')).toBeTruthy()
    expect(within(named).queryByText(/name hidden/)).toBeNull()
  })

  // #382: the label is text a display name can copy, so the marker is an icon,
  // and there is no hover-only tooltip a keyboard or touch user cannot reach.
  it('marks a masked requester with an icon a display name cannot copy', async () => {
    mockList({
      entries: [
        entry({ id: 'm', action: 'created', actor_name: 'Requester', actor_masked: true }),
        entry({ id: 'r', action: 'resolved', actor_name: 'Requester (name hidden)' }),
      ],
      total: 2,
      has_more: false,
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Created')).toBeTruthy())
    const masked = within(screen.getByText('Created').closest('tr')!).getByText('Requester (name hidden)')
    expect(masked.querySelector('svg')).not.toBeNull()
    expect(masked.getAttribute('title')).toBeNull()

    const named = within(screen.getByText('Resolved').closest('tr')!).getByText('Requester (name hidden)')
    expect(named.querySelector('svg')).toBeNull()
  })
})
