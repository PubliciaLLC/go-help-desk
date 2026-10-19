import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #445: "Guests may attach files" sits under guest submission and depends on
// it; its size slider is 1 to 100 MB, 5 by default.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => <a href={to} {...rest}>{children}</a>,
}))

import { SettingsPage } from './SettingsPage'

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1', email: 'admin@example.com', display_name: 'Ada Admin', role: 'admin', mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    },
  })
})
afterEach(() => vi.restoreAllMocks())

async function renderSettings(settings: Record<string, unknown>) {
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
  return patch
}

const toggle = () => screen.getByRole('switch', { name: /guests may attach files/i }) as HTMLButtonElement
const slider = () => screen.getByRole('slider', { name: /largest file a guest may attach/i }) as HTMLInputElement

describe('the guest attachments settings', () => {
  it('is off by default and the slider starts at 5 MB', async () => {
    await renderSettings({ guest_submission_enabled: true })
    expect(toggle().getAttribute('aria-checked')).toBe('false')
    expect(slider().value).toBe('5')
  })

  it('cannot be switched on while guest submission is off, and says why', async () => {
    await renderSettings({ guest_submission_enabled: false, guest_attachments_enabled: true })
    expect(toggle().disabled).toBe(true)
    expect(slider().disabled).toBe(true)
    expect(screen.getByText(/only takes effect while guest submission is on/i)).toBeTruthy()
  })

  it('is enabled once guest submission is on, and the slider follows the switch', async () => {
    await renderSettings({ guest_submission_enabled: true })
    expect(toggle().disabled).toBe(false)
    expect(slider().disabled).toBe(true) // nothing to size while attaching is off
    await userEvent.click(toggle())
    expect(slider().disabled).toBe(false)
  })

  it('saves the switch and the size under their setting names', async () => {
    const patch = await renderSettings({ guest_submission_enabled: true, guest_attachment_max_mb: 5 })
    await userEvent.click(toggle())
    slider().focus()
    await userEvent.keyboard('{ArrowRight}')
    await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
    await waitFor(() => expect(patch).toHaveBeenCalled())
    expect(patch.mock.calls.at(-1)![1]).toMatchObject({ guest_attachments_enabled: true, guest_attachment_max_mb: 6 })
  })

  describe('the per-ticket cap and the hourly rate', () => {
    const cap = () => screen.getByRole('spinbutton', { name: /most files a guest can attach to one ticket/i }) as HTMLInputElement
    const rate = () => screen.getByRole('spinbutton', { name: /uploads per guest link per hour/i }) as HTMLInputElement

    it('start at 5 and 3, with their ranges', async () => {
      await renderSettings({ guest_submission_enabled: true, guest_attachments_enabled: true })
      expect(cap().value).toBe('5')
      expect([cap().min, cap().max]).toEqual(['1', '50'])
      expect(rate().value).toBe('3')
      expect([rate().min, rate().max]).toEqual(['1', '30'])
    })

    it('are unavailable while guest uploads are off', async () => {
      await renderSettings({ guest_submission_enabled: true, guest_attachments_enabled: false })
      expect(cap().disabled).toBe(true)
      expect(rate().disabled).toBe(true)
      await userEvent.click(toggle())
      expect(cap().disabled).toBe(false)
      expect(rate().disabled).toBe(false)
    })

    it('save under their setting names, and stay inside their ranges', async () => {
      const patch = await renderSettings({ guest_submission_enabled: true, guest_attachments_enabled: true })
      await userEvent.clear(cap())
      await userEvent.type(cap(), '12')
      await userEvent.clear(rate())
      await userEvent.type(rate(), '99') // above 30: held at the top of the range
      await userEvent.click(screen.getAllByRole('button', { name: /save changes/i }).at(-1)!)
      await waitFor(() => expect(patch).toHaveBeenCalled())
      expect(patch.mock.calls.at(-1)![1]).toMatchObject({
        guest_attachment_max_per_ticket: 12,
        guest_attachment_uploads_per_hour: 30,
      })
    })
  })
})
