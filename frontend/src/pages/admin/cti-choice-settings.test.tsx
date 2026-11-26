import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #711: Show Items to registered users (General -> Tickets), and Show Types to
// guests and Show Items to guests (Guest Access -> Submissions). All off by
// default. The guest ones depend on guest submission, and Items on Types.

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

describe('Show Items to registered users', () => {
  async function render(settings: Record<string, unknown>) {
    const patch = stubSettings(settings)
    renderWithQuery(<GeneralSettingsPage />)
    await screen.findByRole('heading', { name: 'General' })
    await waitLoaded()
    return patch
  }
  const toggle = () => screen.getByRole('switch', { name: 'Show Items to registered users' }) as HTMLButtonElement

  it('is off when the setting has never been saved, with its help text', async () => {
    await render({})
    expect(toggle().getAttribute('aria-checked')).toBe('false')
    expect(screen.getByText(/Item selector on the new-ticket page and the Item on their tickets/i)).toBeTruthy()
  })

  it('reads on when stored on', async () => {
    await render({ users_show_items: true })
    expect(toggle().getAttribute('aria-checked')).toBe('true')
  })

  it('saves only that key, as a boolean', async () => {
    const patch = await render({})
    await userEvent.click(toggle())
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls[0][1]).toEqual({ users_show_items: true })
  })
})

describe('Show Types to guests and Show Items to guests', () => {
  async function render(settings: Record<string, unknown>) {
    const patch = stubSettings(settings)
    renderWithQuery(<GuestAccessSettingsPage />)
    await screen.findByRole('heading', { name: 'Guest Access' })
    await waitLoaded()
    return patch
  }
  const types = () => screen.getByRole('switch', { name: 'Show Types to guests' }) as HTMLButtonElement
  const items = () => screen.getByRole('switch', { name: 'Show Items to guests' }) as HTMLButtonElement

  it('are off when never saved, with their help text', async () => {
    await render({ guest_submission_enabled: true })
    expect(types().getAttribute('aria-checked')).toBe('false')
    expect(items().getAttribute('aria-checked')).toBe('false')
    expect(types().disabled).toBe(false)
    expect(screen.getByText(/Type selector under Category on the guest form/i)).toBeTruthy()
    expect(screen.getByText(/names and descriptions of every active type public/i)).toBeTruthy()
    expect(screen.getByText(/Needs Show Types to guests/i)).toBeTruthy()
  })

  it('Items is unavailable while Types is off, and Types while guest submission is off', async () => {
    await render({ guest_submission_enabled: true })
    expect(items().disabled).toBe(true)
    await userEvent.click(types())
    expect(items().disabled).toBe(false)
  })

  it('both are unavailable while guest submission is off, whatever is stored', async () => {
    await render({ guest_submission_enabled: false, guests_show_types: true, guests_show_items: true })
    expect(types().disabled).toBe(true)
    expect(items().disabled).toBe(true)
  })

  it('saves only the keys changed, as booleans', async () => {
    const patch = await render({ guest_submission_enabled: true })
    await userEvent.click(types())
    await userEvent.click(items())
    await userEvent.click(save())
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls[0][1]).toEqual({ guests_show_types: true, guests_show_items: true })
  })

  it('turning Types off while Items is on keeps the stored Items value, shown unavailable', async () => {
    await render({ guest_submission_enabled: true, guests_show_types: true, guests_show_items: true })
    await userEvent.click(types())
    expect(types().getAttribute('aria-checked')).toBe('false')
    expect(items().disabled).toBe(true)
  })
})
