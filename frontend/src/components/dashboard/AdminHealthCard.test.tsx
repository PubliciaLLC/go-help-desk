import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'

// The admin health card (#675, built after #674 so its links are final): four
// things the app already knows, from the two queries an administrator's page
// already makes.

vi.mock('@tanstack/react-router', () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { AdminHealthCard } from './AdminHealthCard'

const QUIET_WARNINGS = {
  insecure_secrets: [],
  attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '', policy_explicit: false, stale_off: false },
}
const QUIET_SETTINGS = { sla_enabled: true, sla_alerts_enabled: true }

type Answer = { data: unknown } | Error

let warnings: Answer[]
let settings: Answer[]
let get: ReturnType<typeof vi.spyOn>

const next = (q: Answer[]) => {
  const a = q.length > 1 ? q.shift()! : q[0]
  return a instanceof Error ? Promise.reject(a) : Promise.resolve(a)
}

function serve(w: Answer | Answer[], s: Answer | Answer[]) {
  warnings = Array.isArray(w) ? w : [w]
  settings = Array.isArray(s) ? s : [s]
}

const calls = (url: string) => get.mock.calls.filter((c: unknown[]) => c[0] === url).length

beforeEach(() => {
  vi.restoreAllMocks()
  get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') return next(warnings)
    if (url === '/admin/settings') return next(settings)
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
})

describe('AdminHealthCard', () => {
  it('is one muted line when there is nothing to report', async () => {
    serve({ data: QUIET_WARNINGS }, { data: QUIET_SETTINGS })
    renderWithQuery(<AdminHealthCard />)

    expect(await screen.findByText('No problems to report.')).toBeTruthy()
    expect(screen.queryByRole('link')).toBeNull()
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('says nothing while the two queries are in flight', () => {
    get.mockImplementation((() => new Promise(() => {})) as never)
    renderWithQuery(<AdminHealthCard />)
    expect(screen.queryByText('No problems to report.')).toBeNull()
    expect(screen.queryByText('Health checks could not be loaded.')).toBeNull()
  })

  it('lists each item with its text, its place and a link to where it is fixed', async () => {
    serve(
      {
        data: {
          ...QUIET_WARNINGS,
          update: { current: '1.3.0', latest: '1.3.1', url: 'https://example.com/releases/1.3.1' },
          passkeys: { available: false, reason: 'BASE_URL is an IP address', suspended_roles: ['admin'] },
          saml_required_not_loaded: { reason: 'not_loaded' },
          attachment_scanning: { ...QUIET_WARNINGS.attachment_scanning, policy: 'required', configured: true, effect: 'Uploads are refused.' },
        },
      },
      { data: { sla_enabled: true, sla_alerts_enabled: false } },
    )
    renderWithQuery(<AdminHealthCard />)

    expect(await screen.findByText('Version 1.3.1 is available (you run 1.3.0).')).toBeTruthy()
    expect(screen.getByText('SLA tracking is on, but breach and at-risk alerts are off.')).toBeTruthy()
    expect(screen.getByText(/The passkey requirement is suspended for admin/)).toBeTruthy()
    expect(screen.getByText('SAML sign-in is required for staff and users, but SAML is not set up, so they cannot sign in.')).toBeTruthy()
    expect(screen.getByText(/The attachment scanner is unreachable\. Uploads are refused\./)).toBeTruthy()
    expect(screen.queryByText('No problems to report.')).toBeNull()

    const release = screen.getByRole('link', { name: /Release notes/ })
    expect(release.getAttribute('href')).toBe('https://example.com/releases/1.3.1')
    expect(release.getAttribute('target')).toBe('_blank')
    expect(release.getAttribute('rel')).toContain('noopener')

    const targets = [
      ['Email → Notifications → SLA alerts', '/admin/settings/email'],
      ['Authentication → Multi-factor authentication', '/admin/settings/authentication'],
      ['Authentication → SAML', '/admin/settings/authentication'],
      ['File Attachments → Malware scanning', '/admin/settings/attachments'],
    ]
    for (const [place, href] of targets) {
      const link = screen.getByRole('link', { name: new RegExp(place) })
      expect(link.getAttribute('href')).toBe(href)
      expect(link.getAttribute('target')).toBeNull()
    }
  })

  it('has no Hide', async () => {
    serve({ data: { ...QUIET_WARNINGS, update: { current: '1', latest: '2', url: 'https://example.com/r' } } }, { data: {} })
    renderWithQuery(<AdminHealthCard />)
    await screen.findByText('Version 2 is available (you run 1).')
    expect(screen.queryByRole('button')).toBeNull()
  })

  it.each([
    ['the warnings', new Error('boom'), { data: QUIET_SETTINGS }],
    ['the settings', { data: QUIET_WARNINGS }, new Error('boom')],
  ] as [string, Answer, Answer][])('says it could not load when %s failed, never "No problems"', async (_n, w, s) => {
    serve(w, s)
    renderWithQuery(<AdminHealthCard />)

    expect(await screen.findByText('Health checks could not be loaded.')).toBeTruthy()
    expect(screen.queryByText('No problems to report.')).toBeNull()
  })

  it('Retry asks for both again and shows the result', async () => {
    serve(
      [new Error('boom'), { data: QUIET_WARNINGS }],
      { data: { sla_enabled: true, sla_alerts_enabled: false } },
    )
    const user = userEvent.setup()
    renderWithQuery(<AdminHealthCard />)

    await user.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('SLA tracking is on, but breach and at-risk alerts are off.')).toBeTruthy()
    await waitFor(() => expect(screen.queryByText('Health checks could not be loaded.')).toBeNull())
    expect(calls('/admin/security-warnings')).toBe(2)
    expect(calls('/admin/settings')).toBe(2)
  })
})
