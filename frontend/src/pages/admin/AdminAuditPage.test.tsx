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

function mockList(response: AdminAuditListResponse) {
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
    mockList({ entries: [entry()], total: 1 })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    const row = screen.getByText('Resolved').closest('tr')!
    expect(within(row).getByText('ticket')).toBeTruthy()
    expect(within(row).getByText('Sam Staff')).toBeTruthy()
  })

  it('shows an entity-type filter for admin', async () => {
    mockList({ entries: [], total: 0 })
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Audit Log' })).toBeTruthy())

    expect(screen.getByLabelText(/entity type/i)).toBeTruthy()
  })

  // Staff are forced to entity_type=ticket server-side no matter what the
  // query string says — the control would let a staff member pick "user"
  // and see an unexplained empty page, so it is not offered at all.
  it('does not offer an entity-type filter to staff', async () => {
    asStaff()
    mockList({ entries: [], total: 0 })
    renderWithQuery(<AdminAuditPage />)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Audit Log' })).toBeTruthy())

    expect(screen.queryByLabelText(/entity type/i)).toBeNull()
  })

  it('sends an edited action filter to the server', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/audit') return Promise.resolve({ data: { entries: [], total: 0 } })
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
    })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText('Resolved')).toBeTruthy())
    expect(screen.getByText(/status_id/)).toBeTruthy()
    expect(screen.getByText(/a → b/)).toBeTruthy()
  })

  it('shows an empty state rather than a blank table', async () => {
    mockList({ entries: [], total: 0 })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByText(/nothing matches/i)).toBeTruthy())
  })

  it('disables Previous on the first page and Next when there is nothing more', async () => {
    mockList({ entries: [entry()], total: 1 })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /previous/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /previous/i }) as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('enables Next when more entries remain than this page shows', async () => {
    mockList({ entries: [entry()], total: 2 })
    renderWithQuery(<AdminAuditPage />)

    await waitFor(() => expect(screen.getByRole('button', { name: /next/i })).toBeTruthy())
    expect((screen.getByRole('button', { name: /next/i }) as HTMLButtonElement).disabled).toBe(false)
  })
})
