import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #453: how many days an account may stay on a password saved in the older,
// weaker form before sign-in asks for a reset. Lives on the Authentication tab.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { SettingsPage } from './SettingsPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'admin@example.com',
      display_name: 'Ada Admin',
      role: 'admin',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function renderAuthTab(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: 'Authentication' }))
  return patch
}

function graceField(): HTMLInputElement {
  return screen.getByRole('spinbutton', { name: /days before an old password must be reset/i }) as HTMLInputElement
}

describe('the password reset grace period setting', () => {
  it('shows 30 when nothing is stored, as the server does', async () => {
    await renderAuthTab({})
    expect(graceField().value).toBe('30')
  })

  it('shows what is stored', async () => {
    await renderAuthTab({ password_rehash_grace_days: 7 })
    expect(graceField().value).toBe('7')
  })

  it('limits the field to what the server accepts', async () => {
    await renderAuthTab({})
    expect(graceField().min).toBe('1')
    expect(graceField().max).toBe('365')
  })

  it('sends a changed value under its own key', async () => {
    const patch = await renderAuthTab({ password_rehash_grace_days: 30 })
    await userEvent.clear(graceField())
    await userEvent.type(graceField(), '14')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)

    await waitFor(() => {
      expect(patch).toHaveBeenCalled()
      const sent = patch.mock.calls[patch.mock.calls.length - 1][1] as Record<string, unknown>
      expect(sent).toMatchObject({ password_rehash_grace_days: 14 })
    })
  })

  it('holds the field at the least the server accepts once the operator leaves it', async () => {
    const patch = await renderAuthTab({ password_rehash_grace_days: 30 })
    await userEvent.clear(graceField())
    await userEvent.type(graceField(), '0')
    await userEvent.tab()
    expect(graceField().value).toBe('1')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)

    await waitFor(() => {
      const sent = patch.mock.calls[patch.mock.calls.length - 1][1] as Record<string, unknown>
      expect(sent).toMatchObject({ password_rehash_grace_days: 1 })
    })
  })

  it('does not put a minimum between digits while the operator is still typing', async () => {
    await renderAuthTab({ password_rehash_grace_days: 30 })
    await userEvent.clear(graceField())
    expect(graceField().value).toBe('0')
    await userEvent.type(graceField(), '14')
    expect(graceField().value).toBe('14')
  })

  it('leaves an unstored value alone when the field is only visited', async () => {
    const patch = await renderAuthTab({})
    graceField().focus()
    await userEvent.tab()
    expect(graceField().value).toBe('30')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
    await waitFor(() => {
      const calls = patch.mock.calls
      for (const c of calls) expect(c[1]).not.toHaveProperty('password_rehash_grace_days')
    })
  })

  it('holds the field at the most the server accepts', async () => {
    const patch = await renderAuthTab({ password_rehash_grace_days: 30 })
    await userEvent.clear(graceField())
    await userEvent.type(graceField(), '9999')
    expect(graceField().value).toBe('365')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)

    await waitFor(() => {
      const sent = patch.mock.calls[patch.mock.calls.length - 1][1] as Record<string, unknown>
      expect(sent).toMatchObject({ password_rehash_grace_days: 365 })
    })
  })
})
