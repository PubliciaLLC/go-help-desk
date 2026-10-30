import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { AxiosError } from 'axios'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #450: an administrator resetting someone's password can revoke their API
// keys in the same step. Unticked by default.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/users/u-2' } }),
  useNavigate: () => vi.fn(),
  useParams: () => ({ id: 'u-2' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { UserDetailPage } from './UserDetailPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'An Admin', role: 'admin', mfa_enabled: true,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/users/u-2') {
      return Promise.resolve({
        data: {
          id: 'u-2', email: 'someone@example.com', display_name: 'Someone', role: 'staff', disabled: false,
          mfa_enabled: false, has_password: true, auth_type: 'local', groups: [],
          created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
        },
      })
    }
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})
afterEach(() => vi.restoreAllMocks())

async function reset(tick: boolean) {
  const post = vi.spyOn(api, 'post').mockResolvedValue({ data: undefined } as never)
  renderWithQuery(<UserDetailPage />)
  await userEvent.type(await screen.findByPlaceholderText('New password'), 'a-brand-new-password')
  if (tick) await userEvent.click(screen.getByRole('checkbox', { name: /also revoke this person's api keys/i }))
  await userEvent.click(screen.getByRole('button', { name: 'Set password' }))
  await waitFor(() => expect(post).toHaveBeenCalled())
  return post
}

describe('the admin password reset', () => {
  it('offers to revoke the keys, unticked', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({ data: undefined } as never)
    renderWithQuery(<UserDetailPage />)
    const box = (await screen.findByRole('checkbox', { name: /also revoke this person's api keys/i })) as HTMLInputElement
    expect(box.checked).toBe(false)
  })

  it('leaves the keys alone when it is not ticked', async () => {
    const post = await reset(false)
    expect(post.mock.calls[0][0]).toBe('/admin/users/u-2/password')
    expect(post.mock.calls[0][1]).toEqual({ new_password: 'a-brand-new-password', revoke_api_keys: false })
  })

  it('asks for them to be revoked when it is', async () => {
    const post = await reset(true)
    expect(post.mock.calls[0][1]).toEqual({ new_password: 'a-brand-new-password', revoke_api_keys: true })
  })

  it('goes back to unticked after a reset', async () => {
    await reset(true)
    await waitFor(() =>
      expect((screen.getByRole('checkbox', { name: /also revoke/i }) as HTMLInputElement).checked).toBe(false))
  })

  // The reset happened and the keys are still there. The page says exactly
  // that, in the server's words, and does not claim the password was not set.
  it('shows the server message when the password was reset but the keys were not revoked', async () => {
    const message = 'The password was reset, but the API keys were NOT revoked. Revoke them from Admin → API Keys.'
    vi.spyOn(api, 'post').mockRejectedValue(new AxiosError('x', '500', undefined, undefined, {
      status: 500, statusText: '', headers: {}, config: {} as never,
      data: { error: { code: 'keys_not_revoked', message } },
    }))
    renderWithQuery(<UserDetailPage />)
    await userEvent.type(await screen.findByPlaceholderText('New password'), 'a-brand-new-password')
    await userEvent.click(screen.getByRole('checkbox', { name: /also revoke/i }))
    await userEvent.click(screen.getByRole('button', { name: 'Set password' }))
    expect((await screen.findByRole('alert')).textContent).toBe(message)
    expect(screen.queryByText('Password updated.')).toBeNull()
  })
})
