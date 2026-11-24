// Pure display helpers shared by the ticket and settings pages.
//
// priorityVariant was defined identically in TicketDetailPage.tsx and
// TicketListPage.tsx — the same four-branch mapping, copied. Identical today,
// free to drift tomorrow, and it decides the colour of a severity badge, so a
// drift shows up as the same ticket looking urgent on one screen and routine
// on another.
//
// fmtMin lived inside SettingsPage.tsx, where it renders SLA response and
// resolution targets. Both are pure, both have branches worth pinning, and
// neither needs a DOM to test.

/** Badge variant for a ticket priority. Unknown priorities fall back to secondary. */
export function priorityVariant(p: string): 'destructive' | 'warning' | 'default' | 'secondary' {
  if (p === 'critical') return 'destructive'
  if (p === 'high') return 'warning'
  if (p === 'medium') return 'default'
  return 'secondary'
}

/**
 * Formats a duration in minutes for display: 45 -> "45m", 120 -> "2h",
 * 90 -> "1h 30m".
 *
 * Whole hours deliberately omit the minutes rather than reading "2h 0m".
 */
export function fmtMin(m: number): string {
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  const rem = m % 60
  return rem ? `${h}h ${rem}m` : `${h}h`
}
