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
})
