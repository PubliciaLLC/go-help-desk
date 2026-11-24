import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { User } from '@/api/types'

// #131: Reports sits between Tickets and Admin in the sidebar, for staff and
// administrators. A requester has no Reports item.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/dashboard' } }),
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

describe('the Reports navigation entry', () => {
  it.each(['admin', 'staff'] as const)('is there for %s and points at /reports', (role) => {
    signInAs(role)
    renderWithQuery(<Layout>content</Layout>)
    const links = screen.getAllByRole('link', { name: 'Reports' })
    expect(links.length).toBeGreaterThan(0)
    for (const l of links) expect(l.getAttribute('href')).toBe('/reports')
  })

  it('is not offered to a requester', () => {
    signInAs('user')
    renderWithQuery(<Layout>content</Layout>)
    expect(screen.queryByRole('link', { name: 'Reports' })).toBeNull()
  })

  it('sits after the Tickets group and before Admin', () => {
    signInAs('admin')
    renderWithQuery(<Layout>content</Layout>)
    const aside = document.querySelector('aside') as HTMLElement
    const order = Array.from(aside.querySelectorAll('nav a, nav button, nav [id$="-tickets-label"]')).map((e) => e.textContent?.trim())
    const tickets = order.indexOf('Tickets')
    const reports = order.indexOf('Reports')
    const admin = order.indexOf('Admin')
    expect(tickets).toBeGreaterThanOrEqual(0)
    expect(reports).toBeGreaterThan(tickets)
    expect(admin).toBeGreaterThan(reports)
  })
})
