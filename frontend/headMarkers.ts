// The Go server replaces everything between these two comments in index.html
// on every request (#708), and finds them by exact text. Shared by
// vite.config.ts, which fails the build without them, and by
// src/index-html.test.ts, which checks the source page.
const OPEN = '<!-- ghd:head -->'
const CLOSE = '<!-- /ghd:head -->'

function count(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1
}

/** Why the page cannot be rewritten by the server, or null when it can. */
export function headMarkersProblem(html: string): string | null {
  const opens = count(html, OPEN)
  const closes = count(html, CLOSE)
  if (opens !== 1) return `expected exactly one opening marker ${OPEN}, found ${opens}`
  if (closes !== 1) return `expected exactly one closing marker ${CLOSE}, found ${closes}`
  if (html.indexOf(OPEN) > html.indexOf(CLOSE)) return `the markers are out of order: ${OPEN} must come first`
  return null
}
