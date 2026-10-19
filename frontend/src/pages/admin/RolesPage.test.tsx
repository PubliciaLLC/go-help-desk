import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/roles' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { RolesPage } from './RolesPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'admin-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin',
      mfa_enabled: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
  vi.spyOn(api, 'get').mockResolvedValue({ data: [] })
})

describe('RolesPage', () => {
  // 1.x has no working plugin system (planned for v2); the Admin role must not promise it.
  it('does not claim administrators manage plugins', async () => {
    renderWithQuery(<RolesPage />)

    await waitFor(() => expect(screen.getByText(/Full system access/)).toBeTruthy())
    expect(screen.queryByText(/plugin/i)).toBeNull()
  })

  // The server refuses a role change on a service user (409 service_account,
  // #434), so the row offers none: a person's row keeps its dropdown.
  it('gives a service user no role dropdown', async () => {
    const row = (id: string, name: string, service: boolean) => ({
      id, email: `${id}@example.com`, display_name: name, role: 'staff', disabled: false,
      service_account: service, created_at: '2026-01-01T00:00:00Z',
    })
    vi.spyOn(api, 'get').mockResolvedValue({ data: [row('p1', 'Pat Person', false), row('s1', 'Nagios', true)] })
    renderWithQuery(<RolesPage />)

    await waitFor(() => expect(screen.getByText('Nagios')).toBeTruthy())
    expect(screen.getAllByRole('combobox')).toHaveLength(1)
    expect(screen.getByText('staff (service)')).toBeTruthy()
  })
})
