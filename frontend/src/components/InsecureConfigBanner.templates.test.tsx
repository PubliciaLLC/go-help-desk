import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// #158: an edited email template that no longer works fails the mail that uses
// it, so the admin is told in red, once per template, with a one-click revert.

const healthy = {
  enabled: true, missing: [], signup_without_email: false, guest_submission_without_email: false,
  failing: false, failing_count: 0, last_error: '',
}

const BROKEN = [
  { name: 'password_reset', label: 'Reset your password', reason: 'body: must include {{.ResetURL}}', updated_at: '2026-10-25T10:00:00Z' },
  { name: '_header', label: 'Header', reason: 'body, line 1 column 3: .TicketURL is not available', updated_at: '2026-10-25T11:00:00Z' },
]

function mockWarnings(broken: unknown[]) {
  const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') {
      return Promise.resolve({
        data: {
          insecure_secrets: [],
          attachment_scanning: { policy: 'off', configured: false, reachable: true, effect: '', policy_explicit: true, stale_off: false },
          attachment_storage: { writable: true, problem: '' },
          email: { ...healthy, broken_templates: broken },
        },
      })
    }
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
  return get
}

async function openNotices() {
  const region = await screen.findByRole('region', { name: 'Server notices' })
  const details = within(region).queryByRole('button', { name: /details/i })
  if (details) await userEvent.click(details)
  return region
}

describe('the broken email template notice', () => {
  it('is a red failure notice for each broken template, naming it and saying the mail is not being sent', async () => {
    mockWarnings(BROKEN)
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const region = await openNotices()
    const text = region.textContent ?? ''
    expect(text).toContain('Email template "Reset your password" is broken: this mail is not being sent')
    expect(text).toContain('Email template "Header" is broken: no email is being sent until it is fixed or reverted')
    expect(text).toContain('must include {{.ResetURL}}')
    expect(region.className).toMatch(/red/)
  })

  it('says that NO email is sent when the broken one is the header or the footer', async () => {
    mockWarnings([
      { name: '_header', label: 'Header', reason: 'r', updated_at: '2026-10-25T11:00:00Z' },
      { name: '_footer', label: 'Footer', reason: 'r', updated_at: '2026-10-25T11:00:00Z' },
      BROKEN[0],
    ])
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const text = (await openNotices()).textContent ?? ''
    expect(text).toContain('Email template "Header" is broken: no email is being sent until it is fixed or reverted')
    expect(text).toContain('Email template "Footer" is broken: no email is being sent until it is fixed or reverted')
    expect(text).not.toContain('"Header" is broken: this mail')
    // A single template still only stops its own mail.
    expect(text).toContain('Email template "Reset your password" is broken: this mail is not being sent')
  })

  it('has an Edit link to that template', async () => {
    mockWarnings(BROKEN)
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const region = await openNotices()
    const links = within(region).getAllByRole('link', { name: 'Edit' })
    expect(links.map((l) => l.getAttribute('href'))).toEqual([
      '/admin/email-templates?t=password_reset',
      '/admin/email-templates?t=_header',
    ])
  })

  it('reverts with one click, no confirmation, and asks again for the warnings', async () => {
    const get = mockWarnings(BROKEN)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const del = vi.spyOn(api, 'delete').mockResolvedValue({ data: undefined } as any)
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const region = await openNotices()

    const buttons = within(region).getAllByRole('button', { name: 'Revert to default' })
    expect(buttons).toHaveLength(2)
    const before = get.mock.calls.filter((c) => c[0] === '/admin/security-warnings').length
    await userEvent.click(buttons[0])

    await waitFor(() => expect(del).toHaveBeenCalledWith('/admin/email-templates/password_reset'))
    await waitFor(() =>
      expect(get.mock.calls.filter((c) => c[0] === '/admin/security-warnings').length).toBeGreaterThan(before),
    )
  })

  it('says why when the revert fails', async () => {
    mockWarnings(BROKEN)
    vi.spyOn(api, 'delete').mockRejectedValue(new Error('network down'))
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    const region = await openNotices()
    await userEvent.click(within(region).getAllByRole('button', { name: 'Revert to default' })[0])
    expect(await within(region).findByText(/network down/)).toBeTruthy()
  })

  it('is absent when nothing is broken', async () => {
    mockWarnings([])
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull()
  })

  it('comes back after a hide when the template is saved broken again', async () => {
    mockWarnings([BROKEN[0]])
    const first = renderWithQuery(<InsecureConfigBanner isAdmin />)
    const region = await openNotices()
    await userEvent.click(within(region).getByRole('button', { name: /hide/i }))
    await waitFor(() => expect(screen.queryByRole('region', { name: 'Server notices' })).toBeNull())
    first.unmount()

    mockWarnings([{ ...BROKEN[0], updated_at: '2026-10-26T09:00:00Z' }])
    renderWithQuery(<InsecureConfigBanner isAdmin />)
    expect(await screen.findByRole('region', { name: 'Server notices' })).toBeTruthy()
  })
})
