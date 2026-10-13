import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
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

// #459: Settings -> Attachments shows what the scanner is doing, from the
// same endpoint as the banner, so an administrator can see the state without
// waiting for an upload to fail.

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

async function openAttachments(scanning: unknown) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: { site_name: 'Acme' } })
    if (url === '/admin/security-warnings') {
      return Promise.resolve({ data: { insecure_secrets: [], attachment_scanning: scanning } })
    }
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })
  await userEvent.click(screen.getByRole('button', { name: /attachments/i }))
}

describe('saving settings refreshes the scanner status', () => {
  it('re-fetches security-warnings after a save, so "Do not scan" clears the banner at once', async () => {
    await openAttachments({
      policy: 'off',
      configured: false,
      reachable: false,
      effect: 'Attachments are not scanned. Uploads are accepted without being checked.',
      policy_explicit: false,
      stale_off: true,
    })
    await screen.findByRole('group', { name: /scanner status/i })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)
    const calls = () =>
      vi.mocked(api.get).mock.calls.filter(([url]) => url === '/admin/security-warnings').length
    const before = calls()

    await userEvent.selectOptions(screen.getByLabelText('Scan attachments for malware'), 'off')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(calls()).toBeGreaterThan(before))
  })
})

describe('scanner status on Settings -> Attachments', () => {
  it('shows policy, configured, reachable and the effect sentence', async () => {
    await openAttachments({
      policy: 'required',
      configured: true,
      reachable: false,
      effect: 'The scanner is unreachable, so attachment uploads are being refused until it returns.',
      policy_explicit: true,
      stale_off: false,
    })

    const status = await screen.findByRole('group', { name: /scanner status/i })
    const text = status.textContent ?? ''
    expect(within(status).getByText('required')).toBeTruthy()
    expect(text).toMatch(/configured\s*yes/i)
    expect(text).toMatch(/reachable\s*no/i)
    expect(text).toContain('attachment uploads are being refused until it returns')
  })

  it('shows a healthy scanner as reachable', async () => {
    await openAttachments({
      policy: 'required',
      configured: true,
      reachable: true,
      effect: 'Attachments are scanned before being accepted.',
      policy_explicit: true,
      stale_off: false,
    })

    const status = await screen.findByRole('group', { name: /scanner status/i })
    expect(status.textContent ?? '').toMatch(/reachable\s*yes/i)
    expect(status.textContent ?? '').toContain('Attachments are scanned before being accepted.')
  })
})
