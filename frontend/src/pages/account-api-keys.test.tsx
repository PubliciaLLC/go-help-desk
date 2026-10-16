import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #450: staff manage their own API keys under Account, when an administrator
// has allowed it; administrators always can; requesters never see the section.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useRouterState: () => ({ location: { pathname: '/account' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

vi.mock('@/api/passkeys', async (orig) => {
  const actual = await orig<typeof import('@/api/passkeys')>()
  return {
    ...actual,
    listPasskeys: vi.fn().mockResolvedValue([]),
    browserSupportsPasskeys: vi.fn(() => true),
  }
})

vi.mock('@/hooks/useSiteBranding', () => ({ useSiteBranding: () => ({ name: 'Help Desk', logoURL: null }) }))

import { AccountPage } from './AccountPage'

function signedInAs(role: 'staff' | 'admin' | 'user', canCreate: boolean) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'sam@example.com', display_name: 'Sam', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
      can_create_api_keys: canCreate,
    },
  })
}

const SCOPES = [
  { scope: 'tickets:read', resource: 'tickets', action: 'read' },
  { scope: 'tickets:write', resource: 'tickets', action: 'write' },
]

const KEY = {
  id: 'k-1', name: 'my script', user_id: 'u-1', created_by: 'u-1', scopes: ['tickets:read'],
  created_at: '2026-01-01T00:00:00Z',
}

let keys: unknown[]
let get: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  keys = []
  get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/me/api-keys') return Promise.resolve({ data: keys })
    if (url === '/me/api-keys/scopes') return Promise.resolve({ data: SCOPES })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})
afterEach(() => vi.restoreAllMocks())

const section = () => screen.findByRole('heading', { name: 'API keys' })

describe('API keys on the account page', () => {
  it('lets staff create one when allowed, with an expiry, and shows the token once', async () => {
    signedInAs('staff', true)
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { id: 'k-2', name: 'ci', token: 'GHD_secret' } } as never)
    renderWithQuery(<AccountPage />)
    await section()

    await userEvent.type(screen.getByPlaceholderText(/key name/i), 'ci')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'tickets:read' }))
    await userEvent.type(screen.getByLabelText(/expires/i), '2099-03-04')
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }))

    await waitFor(() => expect(post).toHaveBeenCalled())
    expect(post.mock.calls[0][0]).toBe('/me/api-keys')
    const body = post.mock.calls[0][1] as { name: string; scopes: string[]; expires_at: string }
    expect(body.name).toBe('ci')
    expect(body.scopes).toEqual(['tickets:read'])
    // The end of that day in the person's own time zone, as an instant.
    expect(body.expires_at).toBe(new Date('2099-03-04T23:59:59').toISOString())
    expect(await screen.findByText('GHD_secret')).toBeTruthy()
  })

  it('sends no expiry when the field is left empty', async () => {
    signedInAs('staff', true)
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { id: 'k-2', name: 'ci', token: 'GHD_secret' } } as never)
    renderWithQuery(<AccountPage />)
    await section()
    await userEvent.type(screen.getByPlaceholderText(/key name/i), 'ci')
    await userEvent.click(await screen.findByRole('checkbox', { name: 'tickets:read' }))
    await userEvent.click(screen.getByRole('button', { name: 'Create key' }))
    await waitFor(() => expect(post).toHaveBeenCalled())
    // What goes over the wire: undefined is dropped by JSON.
    expect(JSON.parse(JSON.stringify(post.mock.calls[0][1]))).not.toHaveProperty('expires_at')
  })

  it('offers only the scopes the server lists for this role', async () => {
    signedInAs('staff', true)
    renderWithQuery(<AccountPage />)
    await section()
    expect(await screen.findByRole('checkbox', { name: 'tickets:write' })).toBeTruthy()
    expect(screen.queryByRole('checkbox', { name: 'users:write' })).toBeNull()
    expect(get).toHaveBeenCalledWith('/me/api-keys/scopes')
  })

  it('lists existing keys and revokes one after confirming', async () => {
    signedInAs('staff', true)
    keys = [KEY]
    const del = vi.spyOn(api, 'delete').mockResolvedValue({ data: undefined } as never)
    renderWithQuery(<AccountPage />)
    await section()

    expect(await screen.findByText('my script')).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: 'Revoke' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/me/api-keys/k-1'))
  })

  it('says who issued a key that an administrator issued for the person', async () => {
    signedInAs('staff', false)
    keys = [{ ...KEY, created_by: 'someone-else' }]
    renderWithQuery(<AccountPage />)
    await section()
    expect(await screen.findByText(/issued for you by an administrator/i)).toBeTruthy()
  })

  it('shows staff who may not create keys the list, a reason, and no form', async () => {
    signedInAs('staff', false)
    keys = [{ ...KEY, created_by: 'someone-else' }]
    renderWithQuery(<AccountPage />)
    await section()
    expect(await screen.findByText(/has not allowed staff to create/i)).toBeTruthy()
    expect(screen.queryByRole('button', { name: 'Create key' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Revoke' })).toBeTruthy()
  })

  it('shows staff who may not create keys and have none no section at all', async () => {
    signedInAs('staff', false)
    renderWithQuery(<AccountPage />)
    await screen.findByRole('heading', { name: 'Your account' })
    await waitFor(() => expect(get).toHaveBeenCalledWith('/me/api-keys'))
    expect(screen.queryByRole('heading', { name: 'API keys' })).toBeNull()
  })

  it('is shown to an administrator', async () => {
    signedInAs('admin', true)
    renderWithQuery(<AccountPage />)
    expect(await section()).toBeTruthy()
  })

  it('is never shown to, or requested for, a requester', async () => {
    signedInAs('user', false)
    renderWithQuery(<AccountPage />)
    await screen.findByRole('heading', { name: 'Your account' })
    expect(screen.queryByRole('heading', { name: 'API keys' })).toBeNull()
    expect(get).not.toHaveBeenCalledWith('/me/api-keys')
  })
})
