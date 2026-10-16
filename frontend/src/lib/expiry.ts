/**
 * "2027-03-04" to the instant that day ends in the person's own time zone, or
 * undefined for empty (a key that never expires).
 */
export function expiryToISO(date: string): string | undefined {
  if (!date) return undefined
  return new Date(`${date}T23:59:59`).toISOString()
}
