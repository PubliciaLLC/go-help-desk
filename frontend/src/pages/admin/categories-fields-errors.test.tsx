import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Assigning a custom field to a category, and changing its visible/required
// flags, used to fail without a word: the checkbox sprang back and the Assign
// button re-enabled, and an administrator never learned why. (Removing an
// assignment already showed its error.)

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
const OTHER_DEF = { id: 'f-2', name: 'Asset tag', field_type: 'text', sort_order: 2, active: true, created_at: '' }

function refusal(status: number, code: string, message: string) {
  const headers = new AxiosHeaders()
  return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
    status,
    statusText: 'x',
    headers,
    config: { headers } as never,
    data: { error: { code, message } },
  } as never)
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'a@example.com', display_name: 'Ada', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/categories') {
      return Promise.resolve({ data: [{ id: 'c-1', name: 'Hardware', sort_order: 1, active: true }] })
    }
    if (url === '/admin/categories/c-1/fields') return Promise.resolve({ data: [ASSIGNMENT] })
    if (url === '/admin/custom-fields') return Promise.resolve({ data: [ASSIGNMENT.field_def, OTHER_DEF] })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

describe('Categories page, field assignment errors', () => {
  it('says why a visible/required toggle was refused', async () => {
    vi.spyOn(api, 'patch').mockRejectedValue(refusal(409, 'conflict', 'a required field cannot be hidden'))
    renderWithQuery(<CategoriesPage />)

    await userEvent.click(await screen.findByRole('button', { name: 'Expand' }))
    await userEvent.click(await screen.findByLabelText('visible'))

    expect((await screen.findByRole('alert')).textContent).toBe('a required field cannot be hidden')
  })

  it('says why assigning a field failed', async () => {
    vi.spyOn(api, 'post').mockRejectedValue(refusal(500, 'internal_error', 'database unavailable'))
    renderWithQuery(<CategoriesPage />)

    await userEvent.click(await screen.findByRole('button', { name: 'Expand' }))
    await userEvent.click(await screen.findByRole('button', { name: /Assign field/ }))
    await userEvent.selectOptions(await screen.findByRole('combobox'), 'f-2')
    await userEvent.click(screen.getByRole('button', { name: 'Assign' }))

    expect((await screen.findByRole('alert')).textContent).toBe('database unavailable')
  })
})
