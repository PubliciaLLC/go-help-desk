import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { headMarkersProblem } from '../headMarkers'

// The Go server rewrites everything between the two ghd:head markers of the
// page it serves, so each request gets the instance's icon and name before any
// script runs (#708). It finds them by exact text. Lose either one, or the
// order, and the server serves the page unchanged: the default icon, silently.
//
// Two checks hold this. This test reads the source page. vite.config.ts runs
// the same function over the page the build emits and fails the build, which
// is what CI's `npm run build` and the e2e build both run; the backend's
// TestBuiltIndexHasMarkers then reads the embedded copy.

const OPEN = '<!-- ghd:head -->'
const CLOSE = '<!-- /ghd:head -->'

describe('frontend/index.html', () => {
  const html = readFileSync('index.html', 'utf8')

  it('has the ghd:head markers once each, in order', () => {
    expect(headMarkersProblem(html)).toBeNull()
  })

  it('holds the icon link and the title between them, and the rest of the head outside', () => {
    const between = html.slice(html.indexOf(OPEN), html.indexOf(CLOSE))
    expect(between).toContain('<link rel="icon" type="image/svg+xml" href="/favicon.svg" />')
    expect(between).toContain('<title>Go Help Desk</title>')
    expect(between).not.toContain('<meta charset')
    expect(between).not.toContain('name="viewport"')
  })
})

describe('headMarkersProblem', () => {
  const ok = `<head>${OPEN}<title>x</title>${CLOSE}</head>`

  it.each([
    ['both markers in order', ok, null],
    ['no markers', '<head></head>', 'opening'],
    ['no closing marker', `<head>${OPEN}</head>`, 'closing'],
    ['no opening marker', `<head>${CLOSE}</head>`, 'opening'],
    ['markers out of order', `<head>${CLOSE}${OPEN}</head>`, 'order'],
    ['a repeated opening marker', ok + OPEN, 'opening'],
    ['a repeated closing marker', ok + CLOSE, 'closing'],
    ['markers with the spaces removed', ok.replaceAll('<!-- ', '<!--').replaceAll(' -->', '-->'), 'opening'],
  ])('%s', (_name, input, want) => {
    const got = headMarkersProblem(input)
    if (want === null) expect(got).toBeNull()
    else expect(got).toContain(want)
  })
})
