import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #615: an administrator sees a user's passkeys and removes ONE, instead of
// Reset MFA's all-or-nothing. On their own account neither is offered: the
// server refuses both (409 own_account) and the Account page is the way.

let pageId = 'u-2'
vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/users/u-2' } }),
  useNavigate: () => vi.fn(),
  useParams: () => ({ id: pageId }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { UserDetailPage } from './UserDetailPage'

const keys = [
  { id: 'k-1', name: 'Desk key', transports: ['usb'], backup_eligible: false, backup_state: false, created_at: '2026-01-01T00:00:00Z', last_used_at: null },
  { id: 'k-2', name: '', transports: ['internal'], backup_eligible: true, backup_state: true, created_at: '2026-02-01T00:00:00Z', last_used_at: '2026-03-01T00:00:00Z' },
]

function mockApi(id: string) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/admin/users/${id}`) {
      return Promise.resolve({
        data: {
          id, email: 'someone@example.com', display_name: 'Someone', role: 'staff', disabled: false,
          mfa_enabled: false, passkey_count: 2, has_password: true, auth_type: 'local', groups: [],
          created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
        },
      })
    }
    if (url === `/admin/users/${id}/passkeys`) return Promise.resolve({ data: keys })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  vi.restoreAllMocks()
  pageId = 'u-2'
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'An Admin', role: 'admin', mfa_enabled: true,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('Passkeys on the admin user page', () => {
  it('lists the keys and removes one after a confirmation', async () => {
    mockApi('u-2')
    const del = vi.spyOn(api, 'delete').mockResolvedValue({ data: undefined })
    renderWithQuery(<UserDetailPage />)

    expect(await screen.findByText('Desk key')).toBeTruthy()
    expect(screen.getByText('Unnamed key (internal)')).toBeTruthy()

    await userEvent.click(screen.getByRole('button', { name: 'Remove Desk key' }))
    expect(del).not.toHaveBeenCalled()
    // The dialog says what it costs: every session of that person ends.
    expect(await screen.findByText(/signed out everywhere/i)).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Remove' }))

    await waitFor(() => expect(del).toHaveBeenCalledWith('/admin/users/u-2/passkeys/k-1'))
    expect(del).toHaveBeenCalledTimes(1)
  })

  it('shows the server\'s reason when a removal is refused', async () => {
    mockApi('u-2')
    vi.spyOn(api, 'delete').mockRejectedValue({
      isAxiosError: true,
      response: { status: 404, data: { error: { code: 'not_found', message: 'no such passkey on this account' } } },
    })
    renderWithQuery(<UserDetailPage />)

    await userEvent.click(await screen.findByRole('button', { name: 'Remove Desk key' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Remove' }))

    expect(await screen.findByText(/no such passkey on this account/)).toBeTruthy()
  })

  it('offers nothing on your own account, and points to the Account page', async () => {
    pageId = 'u-1'
    mockApi('u-1')
    renderWithQuery(<UserDetailPage />)

    await screen.findByText('Someone')
    expect(await screen.findByText(/Account page/)).toBeTruthy()
    expect(screen.queryByRole('button', { name: /^Remove / })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Reset MFA' })).toBeNull()
  })

  it('asks for no list when the account has no passkey', async () => {
    const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/users/u-2') {
        return Promise.resolve({
          data: {
            id: 'u-2', email: 'x@example.com', display_name: 'Someone', role: 'staff', disabled: false,
            mfa_enabled: true, passkey_count: 0, has_password: true, auth_type: 'local', groups: [],
            created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
          },
        })
      }
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    renderWithQuery(<UserDetailPage />)

    await screen.findByText('Someone')
    expect(get).not.toHaveBeenCalledWith('/admin/users/u-2/passkeys')
  })
})
