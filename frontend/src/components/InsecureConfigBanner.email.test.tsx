import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #455, #456: email that is not set up, or not being delivered, is shown to
// administrators instead of failing silently.

const scanning = {
  policy: 'required',
  configured: true,
  reachable: true,
  effect: 'Attachments are scanned before being accepted.',
  policy_explicit: true,
  stale_off: false,
}

const healthy = {
  enabled: true,
  missing: [],
  signup_without_email: false,
  guest_submission_without_email: false,
  failing: false,
  failing_count: 0,
  last_error: '',
}

function mockWarnings(email?: unknown) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: { insecure_secrets: [], attachment_scanning: scanning, attachment_storage: { writable: true, problem: '' }, email },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

describe('the email warning', () => {
  it('says email is not set up and which variable is missing', async () => {
    mockWarnings({ ...healthy, enabled: false, missing: ['SMTP_FROM'] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('alert')
    const text = alert.textContent ?? ''
    expect(text).toMatch(/email is not set up/i)
    expect(text).toContain('SMTP_FROM')
    expect(text).not.toContain('SMTP_HOST')
  })

  it('names both variables when neither is set', async () => {
    mockWarnings({ ...healthy, enabled: false, missing: ['SMTP_HOST', 'SMTP_FROM'] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const text = (await screen.findByRole('alert')).textContent ?? ''
    expect(text).toContain('SMTP_HOST')
    expect(text).toContain('SMTP_FROM')
  })

  it('warns that signup and guest submission are on while email is off', async () => {
    mockWarnings({
      ...healthy,
      enabled: false,
      missing: ['SMTP_HOST', 'SMTP_FROM'],
      signup_without_email: true,
      guest_submission_without_email: true,
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('alert')).textContent ?? ''
    expect(text).toMatch(/self-service signup is on/i)
    expect(text).toMatch(/guest submission is on/i)
    expect(text).toMatch(/no email can be sent/i)
  })

  it('does not mention signup or guest submission when they are off', async () => {
    mockWarnings({ ...healthy, enabled: false, missing: ['SMTP_HOST', 'SMTP_FROM'] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const text = (await screen.findByRole('alert')).textContent ?? ''
    expect(text).not.toMatch(/signup is on/i)
    expect(text).not.toMatch(/guest submission is on/i)
  })

  it('shows failing deliveries with the newest error', async () => {
    mockWarnings({
      ...healthy,
      failing: true,
      failing_count: 3,
      last_error: 'SMTP server does not offer AUTH but SMTP_USER is set',
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const text = (await screen.findByRole('alert')).textContent ?? ''
    expect(text).toMatch(/email is not being delivered/i)
    expect(text).toContain('3')
    expect(text).toContain('SMTP server does not offer AUTH but SMTP_USER is set')
  })

  it('says nothing when email is configured and delivering', async () => {
    mockWarnings(healthy)
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('says nothing when the server predates the email field', async () => {
    mockWarnings(undefined)
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
