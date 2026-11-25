import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { useThemeStore } from '@/store/theme'
import type { Ticket } from '@/api/types'

// A status colour an administrator chose is shown as chosen in light and
// lightened in dark until it reads on the dark card (#709, lib/statusColor.ts).

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useSearch: () => ({ status: undefined, reporter: undefined }),
  useRouterState: () => ({ location: { pathname: '/tickets' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketListPage } from './TicketListPage'

const NAVY = '#1e3a8a'
const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: NAVY, active: true, ticket_count: 1 },
]
const TICKET: Ticket = {
  id: 'tk-1',
  tracking_number: 'TKT-1',
  subject: 'Navy status',
  description: '',
  description_format: 'plain',
  category_id: 'cat-1',
  priority: 'medium',
  status_id: 'st-new',
  created_at: '2026-09-20T10:00:00Z',
  updated_at: '2026-09-20T10:00:00Z',
}

function chipColor(): string {
  const chip = Array.from(document.querySelectorAll<HTMLElement>('tbody tr span[style]')).find(
    (el) => el.textContent === 'New',
  )
  expect(chip, 'no status chip in the row').toBeDefined()
  return chip!.style.color
}

beforeEach(() => {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/tickets') return Promise.resolve({ data: [TICKET] })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'staff@example.com',
      display_name: 'Sam Staff',
      role: 'staff',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

afterEach(() => {
  useThemeStore.setState({ preference: 'light', prefersDark: false })
})

describe('the status chip colour', () => {
  it('is the administrator-chosen colour in light', async () => {
    useThemeStore.setState({ preference: 'light', prefersDark: false })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Navy status'))
    expect(chipColor()).toBe('rgb(30, 58, 138)')
  })

  it('is lightened in dark, where the navy would not read', async () => {
    useThemeStore.setState({ preference: 'dark', prefersDark: false })
    renderWithQuery(<TicketListPage />)
    await waitFor(() => expect(document.body.textContent).toContain('Navy status'))
    expect(chipColor()).not.toBe('rgb(30, 58, 138)')
  })
})
