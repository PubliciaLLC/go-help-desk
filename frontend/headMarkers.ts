// The Go server replaces everything between these two comments in index.html
// on every request (#708), and finds them by exact text. Shared by
// vite.config.ts, which fails the build without them, and by
// src/index-html.test.ts, which checks the source page.
const OPEN = '<!-- ghd:head -->'
const CLOSE = '<!-- /ghd:head -->'

// The only link relations the region may carry: the icon the dev server shows.
const ICON_RELS = new Set(['icon', 'shortcut', 'apple-touch-icon'])

function count(haystack: string, needle: string): number {
  return haystack.split(needle).length - 1
}

function isIconLink(tag: string): boolean {
  const m = /\brel\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i.exec(tag)
  if (!m) return false
  const tokens = (m[1] ?? m[2] ?? m[3] ?? '').toLowerCase().split(/\s+/).filter(Boolean)
  return tokens.length > 0 && tokens.every((t) => ICON_RELS.has(t))
}

/** Why the page cannot be rewritten by the server, or null when it can. */
export function headMarkersProblem(html: string): string | null {
  const opens = count(html, OPEN)
  const closes = count(html, CLOSE)
  if (opens !== 1) return `expected exactly one opening marker ${OPEN}, found ${opens}`
  if (closes !== 1) return `expected exactly one closing marker ${CLOSE}, found ${closes}`
  const start = html.indexOf(OPEN)
  const end = html.indexOf(CLOSE)
  if (start > end) return `the markers are out of order: ${OPEN} must come first`

  // The server discards this region and writes its own. A script or a
  // stylesheet in it (Vite puts them in the head) would load in `vite dev`
  // and silently not in the built page. Only the icon link and the title are
  // allowed: take those out, and what is left must be blank.
  const left = html
    .slice(start + OPEN.length, end)
    .replace(/<title>[^<]*<\/title>/gi, '')
    .replace(/<link\b[^>]*>/gi, (tag) => (isIconLink(tag) ? '' : tag))
    .trim()
  if (left !== '') {
    const shown = left.length > 80 ? `${left.slice(0, 80)}…` : left
    return `between the markers only the icon <link> and <title> may stand, because the server replaces that region; found ${shown}`
  }
  return null
}
