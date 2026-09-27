import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
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
