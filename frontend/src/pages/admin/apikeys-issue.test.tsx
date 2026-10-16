import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #450: an administrator can put an expiry on a key and issue it on behalf of
// another account.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/api-keys' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { APIKeysPage } from './APIKeysPage'

const SCOPES = [{ scope: 'tickets:read', resource: 'tickets', action: 'read' }]
const USERS = [
  { id: 'u-2', email: 'sam@example.com', display_name: 'Sam Staff', role: 'staff', disabled: false, auth_type: 'local', has_password: true, mfa_enabled: false, created_at: '', updated_at: '' },
]

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'ada@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/scopes') return Promise.resolve({ data: SCOPES })
    if (url === '/admin/users') return Promise.resolve({ data: USERS, headers: { 'x-total-count': '1' } })
    if (url === '/admin/api-keys') {
      return Promise.resolve({
        data: [
          { id: 'k-1', name: 'for sam', user_id: 'u-2', created_by: 'u-1', owner_name: 'Sam Staff', owner_email: 'sam@example.com', scopes: ['tickets:read'], created_at: '2026-01-01T00:00:00Z' },
          { id: 'k-2', name: 'mine', user_id: 'u-1', created_by: 'u-1', owner_name: 'Ada Admin', owner_email: 'ada@example.com', scopes: ['tickets:read'], created_at: '2026-01-01T00:00:00Z' },
        ],
      })
    }
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})
afterEach(() => vi.restoreAllMocks())

async function fillBasics() {
  await userEvent.type(screen.getByPlaceholderText(/key name/i), 'ci')
  await userEvent.click(await screen.findByRole('checkbox', { name: 'tickets:read' }))
}

describe('creating a key as an administrator', () => {
  it('sends an expiry when one is chosen', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { id: 'k-3', name: 'ci', token: 'GHD_x' } } as never)
    renderWithQuery(<APIKeysPage />)
    await fillBasics()
    await userEvent.type(screen.getByLabelText(/expires/i), '2099-03-04')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0][0]).toBe('/admin/api-keys')
    expect(post.mock.calls[0][1]).toMatchObject({ expires_at: new Date('2099-03-04T23:59:59').toISOString() })
    expect(JSON.parse(JSON.stringify(post.mock.calls[0][1]))).not.toHaveProperty('user_id')
  })

  it('issues for the account that was searched for and picked', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { id: 'k-3', name: 'ci', token: 'GHD_x' } } as never)
    renderWithQuery(<APIKeysPage />)
    await fillBasics()

    await userEvent.type(screen.getByLabelText(/issue for/i), 'sam')
    await userEvent.click(await screen.findByRole('option', { name: /Sam Staff/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))

    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0][1]).toMatchObject({ name: 'ci', scopes: ['tickets:read'], user_id: 'u-2' })
  })

  it('marks a key that was issued on someone else behalf', async () => {
    renderWithQuery(<APIKeysPage />)
    const row = (await screen.findByText('for sam')).closest('tr')!
    expect(row.textContent).toMatch(/issued on their behalf/i)
    expect((await screen.findByText('mine')).closest('tr')!.textContent).not.toMatch(/on their behalf/i)
  })
})
