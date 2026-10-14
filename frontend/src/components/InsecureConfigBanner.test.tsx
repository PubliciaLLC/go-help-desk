import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { InsecureConfigBanner } from './InsecureConfigBanner'

// GET /admin/security-warnings has always carried a live reachability check
// against the configured scanner (backend/internal/server/handler_admin_settings.go,
// scanStatus), but nothing in the UI read it (#176) until this banner grew a
// second alert. An administrator whose ClamAV was down found out only when
// uploads started failing — or, on a permissive policy, did not find out at
// all, because files were being stored unscanned and the admin screen said
// nothing.

function mockWarnings(data: unknown) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/security-warnings') return Promise.resolve({ data })
    return Promise.resolve({ data: {} })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

describe('the attachment scanning warning', () => {
  it('says nothing when the scanner is reachable', async () => {
    mockWarnings({
      insecure_secrets: [],
      attachment_scanning: {
        policy: 'required',
        configured: true,
        reachable: true,
        effect: 'Attachments are scanned before being accepted.',
      },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('says nothing when the operator has deliberately turned scanning off', async () => {
    mockWarnings({
      insecure_secrets: [],
      attachment_scanning: {
        policy: 'off',
        configured: false,
        reachable: false,
        effect: 'Attachments are not scanned. Uploads are accepted without being checked.',
      },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    // Deliberately off is a choice already visible in Settings, not a hidden
    // failure — this banner is for the case nobody would otherwise notice.
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('warns when the policy is required and the scanner cannot be reached', async () => {
    const effect = 'The scanner is unreachable, so attachment uploads are being refused until it returns.'
    mockWarnings({
      insecure_secrets: [],
      attachment_scanning: { policy: 'required', configured: true, reachable: false, effect },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('alert')
    expect(alert.textContent ?? '').toContain(effect)
  })

  // The case the issue calls out as the worst one: permissive plus
  // unreachable means files are being stored WITHOUT being scanned, silently.
  it('warns when the policy is permissive and the scanner cannot be reached', async () => {
    const effect =
      'The scanner is unreachable and the policy is permissive, so attachments are being accepted WITHOUT being scanned.'
    mockWarnings({
      insecure_secrets: [],
      attachment_scanning: { policy: 'permissive', configured: true, reachable: false, effect },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('alert')
    expect(alert.textContent ?? '').toContain(effect)
  })

  it('shows both warnings at once when both apply', async () => {
    mockWarnings({
      insecure_secrets: ['SESSION_SECRET'],
      attachment_scanning: {
        policy: 'permissive',
        configured: true,
        reachable: false,
        effect: 'accepted WITHOUT being scanned',
      },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(screen.getAllByRole('alert')).toHaveLength(2))
    const alerts = screen.getAllByRole('alert')
    expect(alerts[0].textContent ?? '').toMatch(/insecure configuration/i)
    expect(alerts[1].textContent ?? '').toMatch(/accepted WITHOUT being scanned/i)
  })

  it('never fires the query for a non-admin', () => {
    mockWarnings({ insecure_secrets: [], attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' } })
    renderWithQuery(<InsecureConfigBanner isAdmin={false} />)

    expect(api.get).not.toHaveBeenCalled()
  })
})

// #459: scanning that is off because nobody chose it. The backend sets
// stale_off only when no policy was ever saved, no scanner address exists and
// attachments are already stored; a deliberate "Do not scan" never sets it.
describe('the scanning-is-off-by-default warning', () => {
  const off = {
    policy: 'off',
    configured: false,
    reachable: false,
    effect: 'Attachments are not scanned. Uploads are accepted without being checked.',
  }

  it('gives the exact fix when stale_off is true', async () => {
    mockWarnings({
      insecure_secrets: [],
      attachment_scanning: { ...off, policy_explicit: false, stale_off: true },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('alert')
    const text = alert.textContent ?? ''
    expect(text).toMatch(/attachments are not being scanned/i)
    expect(text).toContain('COMPOSE_PROFILES=antivirus')
    expect(text).toContain('CLAMAV_ADDR=tcp://clamav:3310')
    expect(text).toContain('docker/.env')
    expect(text).toContain('docker compose up -d')
    expect(text).toMatch(/Settings.*Attachments/)
    expect(text).toContain('Do not scan')
  })

  it('says nothing when stale_off is false', async () => {
    mockWarnings({
      insecure_secrets: [],
      attachment_scanning: { ...off, policy_explicit: true, stale_off: false },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('says nothing to a non-admin', () => {
    mockWarnings({
      insecure_secrets: [],
      attachment_scanning: { ...off, policy_explicit: false, stale_off: true },
    })
    renderWithQuery(<InsecureConfigBanner isAdmin={false} />)

    expect(api.get).not.toHaveBeenCalled()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

// #420: with ticket scope enforcement on, an OAuth client whose service user is
// in no group sees no tickets, and nothing else says so. The backend lists the
// names; the banner keeps naming them until each has a group or is disabled.
describe('the OAuth clients without groups warning', () => {
  const healthy = {
    insecure_secrets: [],
    attachment_scanning: { policy: 'required', configured: true, reachable: true, effect: 'ok' },
  }

  it('names the clients and links to the page that fixes them', async () => {
    mockWarnings({ ...healthy, oauth_clients_without_groups: ['JIRA sync', 'CI pipeline'] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    const alert = await screen.findByRole('alert')
    const text = alert.textContent ?? ''
    expect(text).toContain('JIRA sync')
    expect(text).toContain('CI pipeline')
    expect(text).toMatch(/no tickets/i)
    expect(within(alert).getByRole('link').getAttribute('href')).toBe('/admin/oauth-clients')
  })

  it('says nothing when the list is empty', async () => {
    mockWarnings({ ...healthy, oauth_clients_without_groups: [] })
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('says nothing from a server that predates the field', async () => {
    mockWarnings(healthy)
    renderWithQuery(<InsecureConfigBanner isAdmin />)

    await waitFor(() => expect(api.get).toHaveBeenCalledWith('/admin/security-warnings'))
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
