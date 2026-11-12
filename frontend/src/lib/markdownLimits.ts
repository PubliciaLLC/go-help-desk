// #443: the Markdown parser is quadratic in the number of emphasis and bracket
// delimiters, and recursive in container depth. Measured on 64 KB of "a*"
// repeated: 8.7 seconds; ">" repeated: 7 seconds and a stack overflow;
// "1. " repeated: over a minute. Text like that is shown as plain text instead,
// so a requester cannot freeze the tab of the member of staff who opens the ticket.
//
// The limits are far above anything written by hand: 4000 delimiter runs parse
// in about 0.1 s, and nobody nests quotes or lists twenty deep.
/** Longer text is never formatted: shown as plain text. The description limit. */
export const MAX_FORMATTED_CHARS = 50_000
export const MAX_DELIMITER_RUNS = 4000
export const MAX_CONTAINER_DEPTH = 20
// Each "[" opens a possible link label even in a run, so these count one by one.
export const MAX_OPEN_BRACKETS = 3000

const DELIMITERS = new Set(['*', '_', '~', '[', ']', '!', '`', '<'])

/** True when rendering the text as Markdown could be slow or exhaust the stack. */
export function isTooComplexToFormat(text: string): boolean {
  let runs = 0
  let brackets = 0
  let prev = ''
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (DELIMITERS.has(c) && c !== prev) runs++
    if (c === '[') brackets++
    prev = c
  }
  if (runs > MAX_DELIMITER_RUNS || brackets > MAX_OPEN_BRACKETS) return true

  // Leading block markers per line: ">" quotes and "-", "*", "+", "1." list markers.
  for (const line of text.split('\n')) {
    let depth = 0
    let i = 0
    while (i < line.length && depth <= MAX_CONTAINER_DEPTH) {
      const c = line[i]
      if (c === ' ' || c === '\t') {
        i++
      } else if (c === '>') {
        depth++
        i++
      } else if ((c === '-' || c === '*' || c === '+') && (line[i + 1] === ' ' || line[i + 1] === '\t')) {
        depth++
        i += 2
      } else {
        const m = /^\d{1,9}[.)][ \t]/.exec(line.slice(i, i + 12))
        if (!m) break
        depth++
        i += m[0].length
      }
    }
    if (depth > MAX_CONTAINER_DEPTH) return true
  }
  return false
}
