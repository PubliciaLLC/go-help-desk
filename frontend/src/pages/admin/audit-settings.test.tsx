import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
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

  // The field must agree with what the server actually does when the setting
  // has never been set, which is keep everything. It used to show 365 while
  // the server kept everything, so an operator read a one-year window off a
  // screen describing an unbounded table — and this test pinned the wrong
  // number, which is why nothing caught it.
  it('shows 0 — keep forever — when retention has never been set', async () => {
    const patch = await renderSettings({})
    expect(retentionField().value).toBe('0')
    // Scoped to the retention row: the hint once rendered under Reopen window,
    // where an unscoped query still found it.
    expect(within(retentionField().parentElement!).getByText(/0 keeps everything/)).toBeTruthy()

    // Changing an unrelated setting must not smuggle the displayed default in
    // as though the operator had chosen it; if the page sends it at all, it
    // must send what it showed.
    await save()
    await waitFor(() => expect(patch.mock.calls.length).toBeGreaterThan(0))
    const sent = lastPatch(patch)
    if ('audit_retention_days' in sent) {
      expect(sent.audit_retention_days).toBe(0)
    }
  })

  // And a configured window is shown as itself, so the fallback above cannot
  // quietly swallow a real value.
  it('shows a configured window rather than the forever default', async () => {
    await renderSettings({ audit_retention_days: 90 })
    expect(retentionField().value).toBe('90')
    expect(within(retentionField().parentElement!).queryByText(/0 keeps everything/)).toBeNull()
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
  // treats it as forever (admin.Service.AuditRetentionDays, covered at that
  // layer).
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

// #362: where audit views show a requester as "Requester" instead of by name.
describe('the requester-name masking setting', () => {
  function maskSelect(): HTMLSelectElement {
    return screen.getByRole('combobox', { name: /mask requester names in audit trail/i }) as HTMLSelectElement
  }

  it('is in the Privacy section and defaults to Everywhere, as the server does', async () => {
    await renderSettings({})
    expect(screen.getByRole('heading', { name: 'Privacy' })).toBeTruthy()
    expect(maskSelect().value).toBe('everywhere')
  })

  it('offers exactly the three choices', async () => {
    await renderSettings({})
    const options = Array.from(maskSelect().options).map((o) => [o.value, o.textContent])
    expect(options).toEqual([
      ['admin_log', 'Admin Audit Log Only'],
      ['ticket_log', 'Ticket Audit Log'],
      ['everywhere', 'Everywhere'],
    ])
  })

  it('shows what the instance has stored and sends a change under its own key', async () => {
    const patch = await renderSettings({ audit_mask_requester_names: 'admin_log' })
    expect(maskSelect().value).toBe('admin_log')
    await userEvent.selectOptions(maskSelect(), 'ticket_log')
    await save()
    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ audit_mask_requester_names: 'ticket_log' })
    })
  })
})
