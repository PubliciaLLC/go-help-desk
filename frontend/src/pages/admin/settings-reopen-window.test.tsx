import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #349: reopen_window_days is also when a Resolved ticket closes, and a closed
// ticket is read-only for everyone who filed it. The screen has to say so,
// including what 0 means, because 0 reads as "off" and is not: it closes a
// Resolved ticket on the next sweep.

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
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { reopen_window_days: 7 } })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

describe('the reopen window setting', () => {
  it('says it is also when the ticket closes and becomes read-only', async () => {
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    expect(screen.getByText('Reopen window and auto-close')).toBeTruthy()
    const help = screen.getByText(/also when the ticket closes/i)
    expect(help.textContent).toMatch(/read-only/i)
    expect(help.textContent).toMatch(/cannot be reopened by a requester/i)
  })

  it('says what 0 means: closed and read-only on the next sweep, not "off"', async () => {
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    const help = screen.getByText(/also when the ticket closes/i)
    expect(help.textContent).toMatch(/Set to 0 for no reopening/i)
    expect(help.textContent).toMatch(/closed and read-only on the next automatic sweep/i)
    // The old text promised something 0 never delivered.
    expect(help.textContent).not.toMatch(/prevent reopening entirely/i)
  })
})

// closed_reopen_policy (#349): who may force-reopen a Closed ticket.
describe('the closed-ticket reopen policy setting', () => {
  function policySelect() {
    return screen.getByRole('combobox', { name: 'Reopening closed tickets' }) as HTMLSelectElement
  }

  it('defaults to off when nothing is stored, and offers the three values', async () => {
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    const select = policySelect()
    expect(select.value).toBe('off')
    expect(within(select).getAllByRole('option').map((o) => (o as HTMLOptionElement).value)).toEqual([
      'off',
      'admin',
      'staff_admin',
    ])
  })

  it('says what each value means and that requesters can never reopen', async () => {
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    const help = screen.getByText(/Whether a Closed ticket can be reopened at all/i)
    expect(help.textContent).toMatch(/Off \(the default\)/)
    expect(help.textContent).toMatch(/Admins only/)
    expect(help.textContent).toMatch(/Staff and admins/)
    expect(help.textContent).toMatch(/can never reopen a closed ticket, whatever this is set to/i)
    expect(help.textContent).toMatch(/follow-up is always available/i)
  })

  it('shows what is stored', async () => {
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/settings') return Promise.resolve({ data: { closed_reopen_policy: 'staff_admin' } })
      if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
      if (url === '/site') return Promise.resolve({ data: {} })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })
    expect(policySelect().value).toBe('staff_admin')
  })

  it('sends the chosen value on save', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    await userEvent.selectOptions(policySelect(), 'admin')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(patch.mock.calls.length).toBeGreaterThan(0))
    const body = patch.mock.calls[patch.mock.calls.length - 1][1] as Record<string, unknown>
    expect(body.closed_reopen_policy).toBe('admin')
  })
})
