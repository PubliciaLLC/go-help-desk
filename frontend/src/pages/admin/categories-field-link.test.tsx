import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Custom Fields A: the Fields subsection of a category, type or item points at
// the Custom Fields page, which now owns the order and the details.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/categories' } }),
  // The real Link builds the query string from `search`; so does this one.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, search, children, ...rest }: any) => (
    <a href={search ? `${to}?${new URLSearchParams(search).toString()}` : to} {...rest}>
      {children}
    </a>
  ),
}))

import { CategoriesPage } from './CategoriesPage'

const CAT = { id: 'c-1', name: 'Hardware', sort_order: 1, active: true }

// The server sends these by field number, then name (the same query the ticket
// forms use), so Zeta (number 1) comes before Alpha (number 2).
const ASSIGNMENTS = [
  {
    id: 'a-1', field_def_id: 'f-zeta', scope_type: 'category', scope_id: 'c-1', sort_order: 2,
    visible_on_new: true, required_on_new: false,
    field_def: { id: 'f-zeta', name: 'Zeta', field_type: 'text', sort_order: 1, active: true, created_at: '2026-01-01T00:00:00Z' },
  },
  {
    id: 'a-2', field_def_id: 'f-alpha', scope_type: 'category', scope_id: 'c-1', sort_order: 1,
    visible_on_new: true, required_on_new: false,
    field_def: { id: 'f-alpha', name: 'Alpha', field_type: 'text', sort_order: 2, active: true, created_at: '2026-01-01T00:00:00Z' },
  },
]

function mockApi(assignments: object[] = ASSIGNMENTS) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/categories') return Promise.resolve({ data: [CAT] })
    if (url === '/admin/categories/c-1/fields') return Promise.resolve({ data: assignments })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'a@example.com', display_name: 'Ada', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function openCategory() {
  const row = (await screen.findByText('Hardware')).closest('[data-archived]') as HTMLElement
  await userEvent.click(within(row).getByRole('button', { name: 'Expand' }))
}

describe('Categories page: Fields subsection', () => {
  it('links each field name to that field on the Custom Fields page', async () => {
    mockApi()
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    const link = await screen.findByRole('link', { name: 'Zeta' })
    expect(link.getAttribute('href')).toBe('/admin/custom-fields?field=f-zeta')
    expect(screen.getByRole('link', { name: 'Alpha' }).getAttribute('href')).toBe('/admin/custom-fields?field=f-alpha')
  })

  it('says where the order is set', async () => {
    mockApi()
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    expect(await screen.findByText('Order is set on Custom Fields.')).toBeTruthy()
  })

  it('does not say it when the node has no fields', async () => {
    mockApi([])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    await screen.findByRole('button', { name: /Assign field/ })
    expect(screen.queryByText('Order is set on Custom Fields.')).toBeNull()
  })

  it('lists the fields in the order the server sends them, which is by field number', async () => {
    mockApi()
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    await screen.findByRole('link', { name: 'Zeta' })
    const names = screen.getAllByRole('link').map((a) => a.textContent)
    expect(names.indexOf('Zeta')).toBeLessThan(names.indexOf('Alpha'))
  })
})
