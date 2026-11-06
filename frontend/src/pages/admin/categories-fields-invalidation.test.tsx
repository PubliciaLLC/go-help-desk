import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The new-ticket form and the ticket page cache a classification's resolved
// fields under ['ctiFieldsResolved', ...]. Changing an attachment on this page
// has to clear that cache too, or a form opened within the next 30 seconds
// still shows the old fields.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/categories' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { CategoriesPage } from './CategoriesPage'

const ASSIGNMENT = {
  id: 'a-1',
  field_def_id: 'f-1',
  field_def: { id: 'f-1', name: 'Region', field_type: 'text', sort_order: 1, active: true, created_at: '' },
  scope_type: 'category',
  scope_id: 'c-1',
  sort_order: 1,
  visible_on_new: true,
  required_on_new: false,
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

describe('Categories page, field attachments', () => {
  it('clears the resolved-fields cache as well as its own list', async () => {
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/categories') {
        return Promise.resolve({ data: [{ id: 'c-1', name: 'Hardware', sort_order: 1, active: true }] })
      }
      if (url === '/admin/categories/c-1/fields') return Promise.resolve({ data: [ASSIGNMENT] })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: ASSIGNMENT } as any)

    const { queryClient } = renderWithQuery(<CategoriesPage />)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await userEvent.click(await screen.findByRole('button', { name: 'Expand' }))
    await userEvent.click(await screen.findByLabelText('visible'))

    await waitFor(() => expect(patch).toHaveBeenCalled())
    await waitFor(() => {
      const keys = invalidate.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey))
      expect(keys).toContain(JSON.stringify(['ctiFields', 'c-1']))
      expect(keys).toContain(JSON.stringify(['ctiFieldsResolved']))
    })
  })

  // The Custom Fields page keeps each field's place count and places under
  // ['admin', 'custom-fields'], fresh for 30 s; a change made here has to clear them.
  it('clears the Custom Fields page\'s place counts and places too', async () => {
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/categories') {
        return Promise.resolve({ data: [{ id: 'c-1', name: 'Hardware', sort_order: 1, active: true }] })
      }
      if (url === '/admin/categories/c-1/fields') return Promise.resolve({ data: [ASSIGNMENT] })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(api, 'delete').mockResolvedValue({ data: {} } as any)

    const { queryClient } = renderWithQuery(<CategoriesPage />)
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')

    await userEvent.click(await screen.findByRole('button', { name: 'Expand' }))
    await userEvent.click(await screen.findByTitle('Remove assignment'))

    await waitFor(() => {
      const keys = invalidate.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey))
      expect(keys).toContain(JSON.stringify(['admin', 'custom-fields']))
    })
  })
})
