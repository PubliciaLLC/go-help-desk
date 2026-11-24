import type { SLATargetStatus } from '@/api/types'

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

/**
 * Tooltip text for one SLA target: "48m left", "1h 20m over", "Met with 12m
 * to spare", "Met 1h 5m late".
 *
 * Outstanding vs. met is decided by met_at, not by remaining_min's sign — a
 * met target can still read "over" (a late response) and an outstanding one
 * can already be past its target while the sweep has not stamped a breach
 * yet, which is exactly the live-vs-stamp distinction the color itself draws.
 *
 * remaining_min is truncated toward zero by the server, so it is 0 for
 * anything within a minute of the target on either side, and the sign cannot
 * say which. The color can: red means elapsed has reached the target, and
 * green/amber mean it has not. A red zero therefore reads "<1m" late/over
 * rather than "0m" to spare/left, which would call a missed target on time.
 */
export function slaTargetText(t: SLATargetStatus): string {
  const over = t.remaining_min < 0 || (t.remaining_min === 0 && t.color === 'red')
  const remaining = t.remaining_min === 0 && over ? '<1m' : fmtMin(Math.abs(t.remaining_min))
  if (t.met_at == null) {
    return over ? `${remaining} over` : `${remaining} left`
  }
  return over ? `Met ${remaining} late` : `Met with ${remaining} to spare`
}

/**
 * A duration in whole seconds for the Reports page: "under 1 min", "59 min",
 * "1 h 1 min", "23 h 59 min", "1 d 1 h", "400 d". Minutes are dropped once the
 * figure is in days, and zero parts are dropped throughout.
 */
export function fmtDuration(seconds: number): string {
  if (seconds < 60) return 'under 1 min'
  const mins = Math.floor(seconds / 60)
  if (mins < 60) return `${mins} min`
  const hours = Math.floor(mins / 60)
  if (hours < 24) {
    const rem = mins % 60
    return rem ? `${hours} h ${rem} min` : `${hours} h`
  }
  const days = Math.floor(hours / 24)
  const rem = hours % 24
  return rem ? `${days} d ${rem} h` : `${days} d`
}

/**
 * How long ago an instant was, floored to one unit: "just now" under a minute
 * (and for an instant in the future, which clock skew makes possible), then
 * "N minute(s) ago", "N hour(s) ago" and "N day(s) ago". There are no weeks,
 * months or years: "120 days ago" is exact and short. English only, like every
 * other string on the page.
 */
export function fmtAgo(iso: string, now: Date = new Date()): string {
  const secs = Math.floor((now.getTime() - new Date(iso).getTime()) / 1000)
  // Written so that an unreadable date (NaN) is "just now" rather than "NaN days".
  if (!(secs >= 60)) return 'just now'
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins} ${mins === 1 ? 'minute' : 'minutes'} ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours} ${hours === 1 ? 'hour' : 'hours'} ago`
  const days = Math.floor(hours / 24)
  return `${days} ${days === 1 ? 'day' : 'days'} ago`
}
