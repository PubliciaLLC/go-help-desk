import type { SecurityWarnings } from '@/api/admin'

// The admin health card as data (docs/DESIGN.md, "Admin health card", #675).
// Each item carries its address and the place name of where it is fixed, so a
// page that moves is one row to change. Built after #674, so the three settings
// addresses are the pages' own.

export interface HealthItem {
  id: string
  severity: 'info' | 'amber' | 'red'
  text: string
  href: string
  /** Where the link goes, in the words the pages use. */
  place: string
  /** Opens in a new tab: only the release notes leave the app. */
  external: boolean
}

/**
 * What an administrator should know about right now, in the order of the
 * design's table. Reads only what the banner and the settings pages already
 * load. A key that is absent (a server older than this frontend) gives no item:
 * a missing field is never read as a problem.
 */
export function adminHealthItems(
  warnings?: SecurityWarnings,
  settings?: Record<string, unknown>,
): HealthItem[] {
  const items: HealthItem[] = []

  const update = warnings?.update
  if (update) {
    items.push({
      id: 'update',
      severity: 'info',
      text: `Version ${update.latest} is available (you run ${update.current}).`,
      href: update.url,
      place: 'Release notes',
      external: true,
    })
  }

  if (settings?.sla_enabled === true && settings.sla_alerts_enabled === false) {
    items.push({
      id: 'sla-alerts-off',
      severity: 'amber',
      text: 'SLA tracking is on, but breach and at-risk alerts are off.',
      href: '/admin/settings/email',
      place: 'Email → Notifications → SLA alerts',
      external: false,
    })
  }

  const passkeys = warnings?.passkeys
  const suspended = passkeys?.suspended_roles ?? []
  if (passkeys && suspended.length > 0) {
    items.push({
      id: 'passkey-suspended',
      severity: 'amber',
      text: `The passkey requirement is suspended for ${suspended.join(', ')}: passkeys are unavailable (${passkeys.reason}).`,
      href: '/admin/settings/authentication',
      place: 'Authentication → Multi-factor authentication',
      external: false,
    })
  }

  // The banner's own test: an operator who chose "off" does not get an alarm.
  const scanning = warnings?.attachment_scanning
  if (scanning && scanning.policy !== 'off' && !scanning.reachable) {
    items.push({
      id: 'scanner-down',
      severity: 'red',
      text: `The attachment scanner is unreachable. ${scanning.effect}`.trim(),
      href: '/admin/settings/attachments',
      place: 'File Attachments → Malware scanning',
      external: false,
    })
  }

  return items
}
