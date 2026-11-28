import { describe, expect, it } from 'vitest'
import type { SecurityWarnings } from '@/api/admin'
import { adminHealthItems } from './adminHealth'

// The admin health card as data (docs/DESIGN.md, "Admin health card"): the five
// rows, in order, each with the address and the place name of where to fix it.

const quiet = {
  insecure_secrets: [],
  attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '', policy_explicit: false, stale_off: false },
} as unknown as SecurityWarnings

const warn = (over: Partial<SecurityWarnings>): SecurityWarnings => ({ ...quiet, ...over })

const update = { current: '1.3.0', latest: '1.3.1', url: 'https://example.com/releases/1.3.1' }
const scanner = {
  policy: 'required', configured: true, reachable: false,
  effect: 'Uploads are refused until it is back.', policy_explicit: true, stale_off: false,
}
const passkeys = { available: false, reason: 'BASE_URL is an IP address', suspended_roles: ['admin', 'staff'] }

describe('adminHealthItems', () => {
  it('has nothing to say when all is well, and when nothing has loaded', () => {
    expect(adminHealthItems(quiet, { sla_enabled: true, sla_alerts_enabled: true })).toEqual([])
    expect(adminHealthItems(undefined, undefined)).toEqual([])
  })

  const cases = [
    {
      name: 'a newer release',
      warnings: warn({ update }),
      settings: {},
      want: {
        id: 'update', severity: 'info', text: 'Version 1.3.1 is available (you run 1.3.0).',
        href: 'https://example.com/releases/1.3.1', place: 'Release notes', external: true,
      },
    },
    {
      name: 'SLA tracking on and its alerts off',
      warnings: quiet,
      settings: { sla_enabled: true, sla_alerts_enabled: false },
      want: {
        id: 'sla-alerts-off', severity: 'amber', text: 'SLA tracking is on, but breach and at-risk alerts are off.',
        href: '/admin/settings/email', place: 'Email → Notifications → SLA alerts', external: false,
      },
    },
    {
      name: 'a suspended passkey requirement',
      warnings: warn({ passkeys }),
      settings: {},
      want: {
        id: 'passkey-suspended', severity: 'amber',
        text: 'The passkey requirement is suspended for admin, staff: passkeys are unavailable (BASE_URL is an IP address).',
        href: '/admin/settings/authentication', place: 'Authentication → Multi-factor authentication', external: false,
      },
    },
    {
      name: 'SAML required but not loaded',
      warnings: warn({ saml_required_not_loaded: { reason: 'incomplete' } }),
      settings: {},
      want: {
        id: 'saml-required-not-loaded', severity: 'red',
        text: 'SAML is required for staff and users but is not working, so they cannot sign in. Administrators can still sign in with a password.',
        href: '/admin/settings/authentication', place: 'Authentication → SAML', external: false,
      },
    },
    {
      name: 'an unreachable scanner',
      warnings: warn({ attachment_scanning: scanner }),
      settings: {},
      want: {
        id: 'scanner-down', severity: 'red',
        text: 'The attachment scanner is unreachable. Uploads are refused until it is back.',
        href: '/admin/settings/attachments', place: 'File Attachments → Malware scanning', external: false,
      },
    },
  ]
  it.each(cases)('gives one item for $name', ({ warnings, settings, want }) => {
    expect(adminHealthItems(warnings, settings)).toEqual([want])
  })

  it('gives all five in the table order', () => {
    const items = adminHealthItems(
      warn({ update, passkeys, attachment_scanning: scanner, saml_required_not_loaded: { reason: 'not_loaded' } }),
      { sla_enabled: true, sla_alerts_enabled: false },
    )
    expect(items.map((i) => i.id)).toEqual(['update', 'sla-alerts-off', 'passkey-suspended', 'saml-required-not-loaded', 'scanner-down'])
  })

  const absent = [
    { name: 'SLA tracking off', settings: { sla_enabled: false, sla_alerts_enabled: false } },
    { name: 'SLA alerts on', settings: { sla_enabled: true, sla_alerts_enabled: true } },
    { name: 'sla_alerts_enabled missing (an older server)', settings: { sla_enabled: true } },
    { name: 'sla_enabled missing', settings: { sla_alerts_enabled: false } },
    { name: 'sla_alerts_enabled not a boolean', settings: { sla_enabled: true, sla_alerts_enabled: 'false' } },
  ]
  it.each(absent)('shows no SLA item with $name', ({ settings }) => {
    expect(adminHealthItems(quiet, settings)).toEqual([])
  })

  it('shows no passkey item for no suspended roles, or a missing key', () => {
    expect(adminHealthItems(warn({ passkeys: { available: false, reason: 'x', suspended_roles: [] } }), {})).toEqual([])
    expect(adminHealthItems(warn({ passkeys: { available: false, reason: 'x' } }), {})).toEqual([])
    expect(adminHealthItems(warn({ passkeys: undefined }), {})).toEqual([])
  })

  it('shows no SAML item when the server sends no such field, whatever the settings say', () => {
    expect(adminHealthItems(quiet, { saml_enabled: true })).toEqual([])
  })

  it('shows no scanner item when scanning is off, or the scanner answers', () => {
    expect(adminHealthItems(warn({ attachment_scanning: { ...scanner, policy: 'off' } }), {})).toEqual([])
    expect(adminHealthItems(warn({ attachment_scanning: { ...scanner, reachable: true } }), {})).toEqual([])
  })

  it('does not infer the SLA item from settings that have not loaded', () => {
    expect(adminHealthItems(warn({ update }), undefined).map((i) => i.id)).toEqual(['update'])
  })
})
