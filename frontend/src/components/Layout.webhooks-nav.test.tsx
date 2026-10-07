import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// #157: the docs send an administrator to Admin -> Webhooks. The page is
// admin-only (the API route is behind the webhooks scope and the admin role),
// so the entry sits with the other admin-only items and nobody else sees it.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/dashboard' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
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
  vi.spyOn(api, 'get').mockImplementation((() =>
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    Promise.resolve({ data: {} })) as any)
})

describe('the Webhooks navigation entry', () => {
  it('is there for an administrator and points at /admin/webhooks', () => {
    signInAs('admin')
    renderWithQuery(<Layout>content</Layout>)

    const links = screen.getAllByRole('link', { name: 'Webhooks' })
    expect(links.length).toBeGreaterThan(0)
    for (const l of links) expect(l.getAttribute('href')).toBe('/admin/webhooks')
  })

  it.each(['staff', 'user'] as const)('is not offered to a %s', (role) => {
    signInAs(role)
    renderWithQuery(<Layout>content</Layout>)

    expect(screen.queryByRole('link', { name: 'Webhooks' })).toBeNull()
  })
})
