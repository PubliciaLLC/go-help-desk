import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The administrator's override of the three staff-email switches (#441), on
// Settings -> Email. Each is user (leave it to each person, the default), on or
// off for everyone.

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

function serve(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

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
  serve({})
})
afterEach(() => vi.restoreAllMocks())

async function openEmailTab() {
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: 'Email' }))
}

const NAMES = ['Tickets assigned to me', 'Activity on my tickets', "My groups' tickets"]

function overrideSelect(name: string) {
  return screen.getByRole('combobox', { name: `${name}: administrator override` }) as HTMLSelectElement
}

describe('Settings -> Email', () => {
  it('has an Email tab with one override per switch', async () => {
    await openEmailTab()
    for (const name of NAMES) expect(overrideSelect(name)).toBeTruthy()
  })

  it('leaves every switch to the user when nothing is stored, and offers the three modes', async () => {
    await openEmailTab()
    for (const name of NAMES) {
      const select = overrideSelect(name)
      expect(select.value).toBe('user')
      expect(within(select).getAllByRole('option').map((o) => (o as HTMLOptionElement).value)).toEqual(['user', 'on', 'off'])
    }
  })

  it('shows what is stored', async () => {
    serve({
      staff_email_assigned_override: 'off',
      staff_email_activity_override: 'on',
      staff_email_group_override: 'user',
    })
    await openEmailTab()
    expect(overrideSelect(NAMES[0]).value).toBe('off')
    expect(overrideSelect(NAMES[1]).value).toBe('on')
    expect(overrideSelect(NAMES[2]).value).toBe('user')
  })

  it('says who is mailed about what, and that nobody is mailed about their own action', async () => {
    await openEmailTab()
    const text = document.body.textContent ?? ''
    expect(text).toMatch(/nobody is emailed about their own action/i)
    expect(text).toMatch(/requester replies/i)
  })

  it('sends only the override that was changed', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    await openEmailTab()

    await userEvent.selectOptions(overrideSelect(NAMES[1]), 'off')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings', { staff_email_activity_override: 'off' })
  })
})
