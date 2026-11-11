import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor } from '@testing-library/react'
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
})
