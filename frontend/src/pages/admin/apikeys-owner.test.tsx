import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #419 / #466: a signed-in administrator is shown every key, so each row says
// whose it is.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/api-keys' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { APIKeysPage } from './APIKeysPage'

const KEYS = [
  {
    id: 'k-1', name: 'ci-key', user_id: 'u-1', scopes: ['tickets:read'], created_at: '2026-01-01T00:00:00Z',
    owner_name: 'Ada Admin', owner_email: 'ada@example.com',
  },
  {
    id: 'k-2', name: 'bridge', user_id: 'u-2', scopes: ['tickets:read'], created_at: '2026-01-02T00:00:00Z',
    owner_name: 'Sam Staff', owner_email: 'sam@example.com',
  },
]

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'ada@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('the API keys list', () => {
  it('shows the owner of every key', async () => {
    vi.spyOn(api, 'get').mockImplementation(((url: string) =>
      Promise.resolve({ data: url === '/admin/api-keys' ? KEYS : [] })) as never)
    renderWithQuery(<APIKeysPage />)

    expect(await screen.findByRole('columnheader', { name: 'Owner' })).toBeTruthy()
    expect(await screen.findByText('Sam Staff')).toBeTruthy()
    expect(screen.getByText('sam@example.com')).toBeTruthy()
    expect(screen.getByText('Ada Admin')).toBeTruthy()
  })
})
