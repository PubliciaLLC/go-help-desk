/** The "Never" stop: keys may be made with no expiry. */
export const NO_LIMIT = -1

/** The default longest lifetime in years when the server has not said. */
export const DEFAULT_MAX_YEARS = 2

const HOUR = 3600 * 1000

function ymd(d: Date): string {
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

function limitInstant(maxYears: number, now: Date): Date {
  const d = new Date(now)
  d.setFullYear(d.getFullYear() + maxYears)
  return d
}

/** The last day a key may expire on, as a date; empty when there is no limit. */
export function maxExpiryDate(maxYears: number, now: Date = new Date()): string {
  return maxYears === NO_LIMIT ? '' : ymd(limitInstant(maxYears, now))
}

/** Whether a chosen day is later than the limit allows. */
export function pastLimit(date: string, maxYears: number, now: Date = new Date()): boolean {
  if (!date || maxYears === NO_LIMIT) return false
  return date > maxExpiryDate(maxYears, now)
}

/**
 * "2027-03-04" to the instant that day ends in the person's own time zone, or
 * undefined for empty (a key that never expires).
 *
 * With a limit, never later than the limit itself less an hour. The last day
 * the form allows ends later than "N years from now" does, and the server
 * compares instants, so sending the end of that day would be refused for the
 * very date the form offered.
 */
export function expiryToISO(date: string, maxYears: number = NO_LIMIT, now: Date = new Date()): string | undefined {
  if (!date) return undefined
  let at = new Date(`${date}T23:59:59`)
  if (maxYears !== NO_LIMIT) {
    const cap = new Date(limitInstant(maxYears, now).getTime() - HOUR)
    if (at > cap) at = cap
  }
  return at.toISOString()
}
