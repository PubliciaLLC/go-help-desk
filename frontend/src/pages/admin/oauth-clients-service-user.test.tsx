import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// Each OAuth client acts as a service user (#420), and the groups of that user
// are the client's ticket scope. The page shows both, so an administrator can
// see what a client can reach and click through to change it.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/oauth-clients' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, params, children, ...rest }: any) => (
    <a href={params?.id ? to.replace('$id', params.id) : to} {...rest}>{children}</a>
  ),
}))

import { OAuthClientsPage } from './OAuthClientsPage'

function client(over: Record<string, unknown>) {
  return {
    id: 'c-1',
    client_id: 'cid-1',
    name: 'JIRA sync',
    scopes: ['tickets:read'],
    created_at: '2026-01-01T00:00:00Z',
    user_id: 'u-9',
    groups: [],
    ...over,
  }
}

function mockClients(clients: unknown[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/oauth-clients') return Promise.resolve({ data: clients })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-0', email: 'admin@example.com', display_name: 'An Admin', role: 'admin',
      mfa_enabled: true, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

describe('OAuth clients page and service users', () => {
  it('links each client to its service user', async () => {
    mockClients([client({})])
    renderWithQuery(<OAuthClientsPage />)
    const tr = (await screen.findByText('cid-1')).closest('tr')!
    const link = within(tr).getByRole('link', { name: /service user/i })
    expect(link.getAttribute('href')).toBe('/admin/users/u-9')
  })

  it('lists the groups the client acts through', async () => {
    mockClients([client({ groups: [{ id: 'g-1', name: 'Service Desk' }, { id: 'g-2', name: 'Networks' }] })])
    renderWithQuery(<OAuthClientsPage />)
    const tr = (await screen.findByText('cid-1')).closest('tr')!
    expect(within(tr).getByText('Service Desk')).toBeTruthy()
    expect(within(tr).getByText('Networks')).toBeTruthy()
  })

  it('says so when the client has no groups', async () => {
    mockClients([client({ groups: [] })])
    renderWithQuery(<OAuthClientsPage />)
    const tr = (await screen.findByText('cid-1')).closest('tr')!
    expect(within(tr).getByText(/no groups/i)).toBeTruthy()
  })

  it('copes with a server that sends no groups or user at all', async () => {
    mockClients([client({ groups: undefined, user_id: undefined })])
    renderWithQuery(<OAuthClientsPage />)
    const tr = (await screen.findByText('cid-1')).closest('tr')!
    expect(within(tr).queryByRole('link', { name: /service user/i })).toBeNull()
  })
})
