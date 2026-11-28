import { beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// #709: the appearance switch sits at the right end of the Sign out row, under
// "Your account", so the sidebar is no taller (the 768-pixel budget).

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/dashboard', search: {} } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { Layout } from './Layout'

function signInAs(role: User['role']) {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'someone@example.com', display_name: 'Someone', role, mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
}

beforeEach(() => {
  vi.restoreAllMocks()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(api, 'get').mockImplementation((() => Promise.resolve({ data: {} })) as any)
})

describe.each(['user', 'staff', 'admin'] as const)('the sidebar footer for a %s', (role) => {
  it('has Your account, Sign out and the appearance switch', () => {
    signInAs(role)
    renderWithQuery(<Layout>content</Layout>)

    expect(screen.getByRole('link', { name: 'Your account' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeTruthy()
    const group = screen.getByRole('group', { name: 'Appearance' })
    expect(within(group).getAllByRole('radio')).toHaveLength(3)
  })

  it('puts the switch in the same row as Sign out, after it', () => {
    signInAs(role)
    renderWithQuery(<Layout>content</Layout>)

    const signOut = screen.getByRole('button', { name: 'Sign out' })
    const row = signOut.parentElement!
    const group = screen.getByRole('group', { name: 'Appearance' })
    expect(row.contains(group)).toBe(true)
    expect(signOut.compareDocumentPosition(group) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // The button gives up its full width to the switch.
    expect(signOut.className).not.toMatch(/\bw-full\b/)
  })
})
