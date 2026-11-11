/**
 * How many rows plain text takes when a row holds `perRow` characters. A stored
 * line is at least one row, and a longer one wraps. An estimate: it ignores
 * where a word would have broken, which is why callers should expect it to be a
 * little low, not exact.
 */
export function plainRows(text: string, perRow: number): number {
  const per = Math.max(1, Math.floor(perRow))
  let rows = 0
  let start = 0
  for (;;) {
    const end = text.indexOf('\n', start)
    const length = (end === -1 ? text.length : end) - start
    rows += Math.max(1, Math.ceil(length / per))
    if (end === -1) return rows
    start = end + 1
  }
}
