import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// #447: the admin guide sends an administrator to a Routing page. It is
// admin-only, like the settings it edits.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/dashboard' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { Layout } from './Layout'

function signInAs(role: User['role']) {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'someone@example.com',
      display_name: 'Someone',
      role,
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(api, 'get').mockImplementation((() => Promise.resolve({ data: {} })) as any)
})

describe('the Routing navigation entry', () => {
  it('is there for an administrator and points at /admin/routing', () => {
    signInAs('admin')
    renderWithQuery(<Layout>content</Layout>)

    const links = screen.getAllByRole('link', { name: 'Routing' })
    expect(links.length).toBeGreaterThan(0)
    for (const l of links) expect(l.getAttribute('href')).toBe('/admin/routing')
  })

  it.each(['staff', 'user'] as const)('is not offered to a %s', (role) => {
    signInAs(role)
    renderWithQuery(<Layout>content</Layout>)

    expect(screen.queryByRole('link', { name: 'Routing' })).toBeNull()
  })
})
