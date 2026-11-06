import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'

// The staff-email switches sit on the profile page (#441). The behaviour of
// the switches is in EmailNotificationsSection's own tests; this is that the
// page shows them to staff and not to a requester.

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

function signedInAs(role: 'staff' | 'admin' | 'user') {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'sam@example.com',
      display_name: 'Sam',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

beforeEach(() => vi.clearAllMocks())

describe('the email switches on the account page', () => {
  it.each(['staff', 'admin'] as const)('are shown to %s', async (role) => {
    signedInAs(role)
    renderWithQuery(<AccountPage />)
    expect(await screen.findByRole('heading', { name: 'Email about tickets' })).toBeTruthy()
    expect(screen.getAllByRole('checkbox')).toHaveLength(3)
  })

  it('are not shown to a requester', async () => {
    signedInAs('user')
    renderWithQuery(<AccountPage />)
    await screen.findByRole('heading', { name: 'Your account' })
    expect(screen.queryByRole('heading', { name: 'Email about tickets' })).toBeNull()
  })
})
