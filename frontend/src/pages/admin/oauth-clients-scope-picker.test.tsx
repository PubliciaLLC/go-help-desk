import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// An OAuth client acts as a staff service user, and the server refuses a scope
// beyond that role when the client is created. The picker must not offer one.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/oauth-clients' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { OAuthClientsPage } from './OAuthClientsPage'

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-0', email: 'admin@example.com', display_name: 'An Admin', role: 'admin',
      mfa_enabled: true, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('OAuthClientsPage scope picker', () => {
  it('asks for the scopes a staff account may carry', async () => {
    const get = vi.spyOn(api, 'get').mockResolvedValue({ data: [] })
    renderWithQuery(<OAuthClientsPage />)
    await waitFor(() =>
      expect(get).toHaveBeenCalledWith('/admin/scopes', { params: { role: 'staff' } }),
    )
  })

  // The staff list the server returns includes the free-text search scope
  // (#635 made the client's service user staff), so the picker offers it, with
  // help text saying what it does.
  it('offers requester_search:read with its help text', async () => {
    vi.spyOn(api, 'get').mockImplementation(((url: string) =>
      Promise.resolve({
        data: url === '/admin/scopes'
          ? [
              { scope: 'tickets:read', resource: 'tickets', action: 'read' },
              { scope: 'requester_search:read', resource: 'requester_search', action: 'read' },
            ]
          : [],
      })) as never)
    renderWithQuery(<OAuthClientsPage />)
    expect(await screen.findByRole('checkbox', { name: 'requester_search:read' })).toBeDefined()
    expect(screen.getByText('Search tickets by requester name or email (free text)')).toBeDefined()
  })
})
