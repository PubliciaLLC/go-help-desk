import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// #456: turning signup or guest submission on while email is off is refused by
// the server (400 email_required). The page has to say why in words an admin
// can act on, not show the raw setting name.

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

function refusal(key: string) {
  const err = new AxiosError('Request failed with status code 400')
  err.response = {
    status: 400,
    data: { error: { code: 'email_required', message: `${key} cannot be turned on while email is not configured` } },
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  } as any
  return err
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
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: {} })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

function switchInRow(label: string) {
  const row = screen.getByText(label).parentElement?.parentElement as HTMLElement
  return row.querySelector('[role="switch"]') as HTMLElement
}

async function expectExplained() {
  const alert = await screen.findByText(/email is not set up/i)
  const text = alert.textContent ?? ''
  expect(text).toContain('SMTP_HOST')
  expect(text).toContain('SMTP_FROM')
  expect(text).toMatch(/restart/i)
  // Not the raw setting name from the server's message.
  expect(text).not.toContain('guest_submission_enabled')
  expect(text).not.toContain('self_signup_enabled')
}

describe('turning on a feature that needs email', () => {
  it('explains the refusal for guest submission', async () => {
    const patch = vi.spyOn(api, 'patch').mockRejectedValue(refusal('guest_submission_enabled'))
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    await userEvent.click(switchInRow('Guest submission'))
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    await waitFor(() => expect(patch).toHaveBeenCalled())
    await expectExplained()
  })

  it('explains the refusal for self-service signup', async () => {
    const patch = vi.spyOn(api, 'patch').mockRejectedValue(refusal('self_signup_enabled'))
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    await userEvent.click(screen.getByRole('button', { name: 'Authentication' }))
    await userEvent.click(switchInRow('Allow self-service signup'))
    // The Authentication tab has other save buttons (SAML, OIDC); the
    // settings page's own is the last on the page.
    const saves = screen.getAllByRole('button', { name: /save changes/i })
    await userEvent.click(saves[saves.length - 1])

    await waitFor(() => expect(patch).toHaveBeenCalled())
    await expectExplained()
  })

  it('still shows the server message for any other refusal', async () => {
    const err = new AxiosError('x')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    err.response = { status: 400, data: { error: { code: 'invalid_ticket_prefix', message: 'prefix is bad' } } } as any
    vi.spyOn(api, 'patch').mockRejectedValue(err)
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    await userEvent.click(switchInRow('Guest submission'))
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))

    expect(await screen.findByText('prefix is bad')).toBeTruthy()
  })
})

// The banner stays quiet for an install that runs without email on purpose, so
// the status lives here, next to the two toggles that need it.
describe('the email status note', () => {
  function withEmail(email: unknown) {
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/settings') return Promise.resolve({ data: {} })
      if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [], email } })
      if (url === '/site') return Promise.resolve({ data: {} })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
  }

  it('says email is not set up, and what is missing, next to the guest toggle', async () => {
    withEmail({ enabled: false, missing: ['SMTP_HOST', 'SMTP_FROM'] })
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    const note = await screen.findByText(/email is not set up/i)
    expect(note.textContent).toContain('SMTP_HOST')
    expect(note.textContent).toContain('SMTP_FROM')
    expect(note.textContent).toMatch(/cannot be turned on/i)
  })

  it('says people are not getting their mail when the feature is already on', async () => {
    vi.spyOn(api, 'get').mockImplementation(((url: string) => {
      if (url === '/admin/settings') return Promise.resolve({ data: { guest_submission_enabled: true } })
      if (url === '/admin/security-warnings') {
        return Promise.resolve({ data: { insecure_secrets: [], email: { enabled: false, missing: ['SMTP_HOST'] } } })
      }
      if (url === '/site') return Promise.resolve({ data: {} })
      return Promise.resolve({ data: [] })
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    }) as any)
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })

    const note = await screen.findByText(/email is not set up/i)
    expect(note.textContent).toMatch(/aren't getting their mail/i)
    expect(note.textContent).not.toMatch(/cannot be turned on/i)
  })

  it('is absent when email is set up', async () => {
    withEmail({ enabled: true, missing: [] })
    renderWithQuery(<SettingsPage />)
    await screen.findByRole('heading', { name: 'Settings' })
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByText(/email is not set up/i)).toBeNull()
  })
})
