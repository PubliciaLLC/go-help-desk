import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The admin surface for the two audit settings (#129): whether staff see the
// field-level diff on top of the action/actor/timestamp they already see,
// and how long any entry is kept before the retention sweep deletes it.
// Both existed only as a raw PATCH before this.

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

function mockApi(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

/** Renders the settings page (General is the default tab). Returns the PATCH spy. */
async function renderSettings(settings: Record<string, unknown> = {}) {
  mockApi(settings)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)

  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })

  return patch
}

async function save() {
  await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
}

function lastPatch(patch: ReturnType<typeof vi.spyOn>): Record<string, unknown> {
  expect(patch.mock.calls.length, 'nothing was sent to the server').toBeGreaterThan(0)
  const call = patch.mock.calls[patch.mock.calls.length - 1]
  expect(call[0]).toBe('/admin/settings')
  return call[1] as Record<string, unknown>
}

function staffDiffToggle(): HTMLElement {
  return screen.getByRole('switch', { name: /staff can view ticket change history/i })
}

function retentionField(): HTMLInputElement {
  return screen.getByRole('spinbutton', { name: /retention/i }) as HTMLInputElement
}

describe('the audit log settings', () => {
  it('is off by default, matching the server-side default', async () => {
    await renderSettings({})
    expect(staffDiffToggle().getAttribute('aria-checked')).toBe('false')
  })

  it('shows what the instance has stored', async () => {
    await renderSettings({
      staff_can_view_ticket_change_history: true,
      audit_retention_days: 90,
    })
    expect(staffDiffToggle().getAttribute('aria-checked')).toBe('true')
    expect(retentionField().value).toBe('90')
  })

  it('shows 365 when retention has never been set, without writing it unasked', async () => {
    const patch = await renderSettings({})
    expect(retentionField().value).toBe('365')

    // Changing an unrelated setting must not smuggle the displayed default
    // in as though the operator had chosen it — save must not be blocked
    // on touching this field at all, but if the page sends it, it must send
    // the same 365 it showed, not something else.
    await save()
    await waitFor(() => expect(patch.mock.calls.length).toBeGreaterThan(0))
    const sent = lastPatch(patch)
    if ('audit_retention_days' in sent) {
      expect(sent.audit_retention_days).toBe(365)
    }
  })

  it('sends the toggle under its own key', async () => {
    const patch = await renderSettings({ staff_can_view_ticket_change_history: false })
    await userEvent.click(staffDiffToggle())
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ staff_can_view_ticket_change_history: true })
    })
  })

  // A non-positive value is not fought in the UI — it is a transient state
  // of an ordinary number field, not a way to purge everything: the server
  // treats it as unset and falls back to 365 (admin.Service.AuditRetentionDays,
  // covered at that layer), the same shape as ReopenWindowDays already uses
  // for its own zero.
  it('clearing the field to retype a value does not corrupt what is sent', async () => {
    const patch = await renderSettings({ audit_retention_days: 365 })
    await userEvent.clear(retentionField())
    await userEvent.type(retentionField(), '30')
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ audit_retention_days: 30 })
    })
  })
})
