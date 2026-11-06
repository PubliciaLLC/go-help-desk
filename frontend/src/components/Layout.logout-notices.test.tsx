import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { DISMISS_PREFIX } from '@/lib/noticeDismissal'

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/dashboard' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { Layout } from './Layout'

const original = window.location

beforeEach(() => {
  vi.restoreAllMocks()
  // The page navigates to /login after signing out; jsdom cannot.
  Object.defineProperty(window, 'location', { configurable: true, writable: true, value: { pathname: '/dashboard', href: '' } })
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(api, 'get').mockImplementation((() => Promise.resolve({ data: {} })) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(api, 'post').mockImplementation((() => Promise.resolve({ data: {} })) as any)
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'a@example.com', display_name: 'A', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

afterEach(() => {
  Object.defineProperty(window, 'location', { value: original, writable: true, configurable: true })
})

// A tab outlives a sign-out, so the next person to sign in there must see the
// notices again.
describe('signing out', () => {
  it('clears hidden server notices', async () => {
    sessionStorage.setItem(DISMISS_PREFIX + 'passkeys|config|', '1')
    renderWithQuery(<Layout>content</Layout>)

    fireEvent.click(screen.getAllByRole('button', { name: 'Sign out' })[0])

    await waitFor(() => expect(window.location.href).toBe('/login'))
    expect(sessionStorage.getItem(DISMISS_PREFIX + 'passkeys|config|')).toBeNull()
  })
})
