import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #711: Users may choose the Item (General -> Tickets) and Guests may choose the
// Type and Item (Guest Access -> Submissions). Both off by default; the guest
// one depends on guest submission, as Guests may attach files does.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  useBlocker: () => ({ status: 'idle' }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { GeneralSettingsPage } from './settings/GeneralSettingsPage'
import { GuestAccessSettingsPage } from './settings/GuestAccessSettingsPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})
afterEach(() => vi.restoreAllMocks())

function stubSettings(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
}

async function waitLoaded() {
  await waitFor(() => expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull())
}

const save = () => screen.getAllByRole('button', { name: /save changes/i }).at(-1)!

describe('Users may choose the Item', () => {
  async function render(settings: Record<string, unknown>) {
    const patch = stubSettings(settings)
    renderWithQuery(<GeneralSettingsPage />)
    await screen.findByRole('heading', { name: 'General' })
    await waitLoaded()
    return patch
  }
  const toggle = () => screen.getByRole('switch', { name: 'Users may choose the Item' }) as HTMLButtonElement

  it('is off when the setting has never been saved, with its help text', async () => {
    await render({})
    expect(toggle().getAttribute('aria-checked')).toBe('false')
    expect(screen.getByText(/Item selector under Type on the new-ticket page/i)).toBeTruthy()
  })

  it('reads on when stored on', async () => {
    await render({ users_can_choose_item: true })
    expect(toggle().getAttribute('aria-checked')).toBe('true')
  })

  it('saves only that key, as a boolean', async () => {
    const patch = await render({})
    await userEvent.click(toggle())
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls[0][1]).toEqual({ users_can_choose_item: true })
  })
})

describe('Guests may choose the Type and Item', () => {
  async function render(settings: Record<string, unknown>) {
    const patch = stubSettings(settings)
    renderWithQuery(<GuestAccessSettingsPage />)
    await screen.findByRole('heading', { name: 'Guest Access' })
    await waitLoaded()
    return patch
  }
  const toggle = () => screen.getByRole('switch', { name: 'Guests may choose the Type and Item' }) as HTMLButtonElement

  it('is off when the setting has never been saved, with its help text', async () => {
    await render({ guest_submission_enabled: true })
    expect(toggle().getAttribute('aria-checked')).toBe('false')
    expect(toggle().disabled).toBe(false)
    expect(screen.getByText(/Type and an Item selector under Category on the guest form/i)).toBeTruthy()
    expect(screen.getByText(/public to anyone who can open the form/i)).toBeTruthy()
  })

  it('is unavailable while guest submission is off, and says why', async () => {
    await render({ guest_submission_enabled: false, guests_can_choose_type_item: true })
    expect(toggle().disabled).toBe(true)
    expect(screen.getAllByText(/only takes effect while guest submission is on/i).length).toBeGreaterThan(0)
  })

  it('saves only that key, as a boolean, and does not touch the user switch', async () => {
    const patch = await render({ guest_submission_enabled: true })
    await userEvent.click(toggle())
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls[0][1]).toEqual({ guests_can_choose_type_item: true })
  })
})
