import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, within, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// An administrator chooses which of a category's fields the public form offers
// (#627). The choice is its own checkbox, off by default, on the category's own
// attachments only: a guest picks a category and nothing below it.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/categories' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, search, children, ...rest }: any) => (
    <a href={search ? `${to}?${new URLSearchParams(search).toString()}` : to} {...rest}>
      {children}
    </a>
  ),
}))

import { CategoriesPage } from './CategoriesPage'

function assignment(over: Record<string, unknown>) {
  return {
    id: 'a-1', field_def_id: 'f-1', scope_type: 'category', scope_id: 'c-1', sort_order: 1,
    visible_on_new: true, required_on_new: false, visible_to_guests: false,
    field_def: { id: 'f-1', name: 'Region', field_type: 'text', sort_order: 1, active: true, created_at: '' },
    ...over,
  }
}

function mockApi(category: object[], type: object[] = []) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/categories') return Promise.resolve({ data: [{ id: 'c-1', name: 'Hardware', sort_order: 1, active: true }] })
    if (url === '/admin/categories/c-1/fields') return Promise.resolve({ data: category })
    if (url === '/admin/categories/c-1/types') return Promise.resolve({ data: [{ id: 't-1', category_id: 'c-1', name: 'Laptop', sort_order: 1, active: true }] })
    if (url === '/admin/categories/c-1/types/t-1/fields') return Promise.resolve({ data: type })
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

describe('Categories page: offering a field to guests', () => {
  it('shows an unchecked box on a category field', async () => {
    mockApi([assignment({})])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    const box = (await screen.findByRole('checkbox', { name: 'Offer to guests for Region' })) as HTMLInputElement
    expect(box.checked).toBe(false)
    expect(screen.getByText('Offer to guests')).toBeTruthy()
  })

  it('checks it when the field is offered', async () => {
    mockApi([assignment({ visible_to_guests: true })])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    expect(((await screen.findByRole('checkbox', { name: 'Offer to guests for Region' })) as HTMLInputElement).checked).toBe(true)
  })

  it('sends visible_to_guests when it is toggled', async () => {
    mockApi([assignment({})])
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: assignment({ visible_to_guests: true }) })
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    await userEvent.click(await screen.findByRole('checkbox', { name: 'Offer to guests for Region' }))
    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/categories/c-1/fields/a-1', { visible_to_guests: true })
  })

  it('is disabled while the field is not shown on the new-ticket form', async () => {
    mockApi([assignment({ visible_on_new: false })])
    renderWithQuery(<CategoriesPage />)
    await openCategory()

    expect(((await screen.findByRole('checkbox', { name: 'Offer to guests for Region' })) as HTMLInputElement).disabled).toBe(true)
  })

  it('is not offered on a type, which a guest cannot pick', async () => {
    mockApi([], [assignment({ id: 'a-2', scope_type: 'type', scope_id: 't-1' })])
    renderWithQuery(<CategoriesPage />)
    await openCategory()
    const typeRow = (await screen.findByText('Laptop')).closest('div') as HTMLElement
    await userEvent.click(within(typeRow.parentElement as HTMLElement).getAllByRole('button', { name: 'Expand' })[0])

    await screen.findByRole('checkbox', { name: 'Show on new-ticket form for Region' })
    expect(screen.queryByRole('checkbox', { name: /Offer to guests/ })).toBeNull()
  })
})
