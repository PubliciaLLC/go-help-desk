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

  // A service user is never an administrator (409 service_account, #434 Q2):
  // its dropdown offers staff and user only; a person's offers all three.
  it('does not offer a service user the admin role', async () => {
    const row = (id: string, name: string, service: boolean) => ({
      id, email: `${id}@example.com`, display_name: name, role: 'staff', disabled: false,
      service_account: service, created_at: '2026-01-01T00:00:00Z',
    })
    vi.spyOn(api, 'get').mockResolvedValue({ data: [row('p1', 'Pat Person', false), row('s1', 'Nagios', true)] })
    renderWithQuery(<RolesPage />)

    await waitFor(() => expect(screen.getByText('Nagios')).toBeTruthy())
    const selects = screen.getAllByRole('combobox')
    expect(selects).toHaveLength(2)
    const options = (el: HTMLElement) => Array.from(el.querySelectorAll('option')).map((o) => o.value)
    expect(options(selects[0])).toEqual(['admin', 'staff', 'user'])
    expect(options(selects[1])).toEqual(['staff', 'user'])
  })
})
