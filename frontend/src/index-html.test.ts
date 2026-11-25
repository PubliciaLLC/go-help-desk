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

// The server throws the region away, so anything in it other than the icon and
// the title is something the built page would lose without a word: a script or
// stylesheet that Vite put there would not load.
describe('headMarkersProblem: what may sit between the markers', () => {
  const region = (inner: string) => `<head><meta charset="UTF-8" />${OPEN}${inner}${CLOSE}</head>`
  const icon = '<link rel="icon" type="image/svg+xml" href="/favicon.svg" />'
  const title = '<title>Go Help Desk</title>'

  it.each([
    ['the icon link and the title', `\n${icon}\n${title}\n`],
    ['the title alone', title],
    ['an empty region', ''],
    ['an icon link in single quotes, any case', `<LINK REL='icon' HREF='/f.png'>${title}`],
    ['a shortcut icon', `<link rel="shortcut icon" href="/favicon.ico" />${title}`],
    ['an apple touch icon', `<link rel="apple-touch-icon" href="/a.png" />${title}`],
    ['markup around the region does not matter', `${icon}${title}`],
  ])('accepts %s', (_name, inner) => {
    expect(headMarkersProblem(region(inner))).toBeNull()
  })

  it.each([
    ['a script', `${icon}${title}<script type="module" src="/assets/index.js"></script>`, '<script'],
    ['an inline script', `${icon}${title}<script>alert(1)</script>`, '<script'],
    ['a stylesheet link', `${icon}${title}<link rel="stylesheet" href="/assets/index.css" />`, 'stylesheet'],
    ['a modulepreload link', `${icon}${title}<link rel="modulepreload" href="/assets/vendor.js" />`, 'modulepreload'],
    ['a link with no rel', `${icon}${title}<link href="/x" />`, '<link href="/x" />'],
    ['an icon link that is also a stylesheet', `<link rel="icon stylesheet" href="/x.css" />${title}`, 'stylesheet'],
    ['a meta tag', `${icon}${title}<meta name="theme-color" content="#fff" />`, '<meta'],
    ['a style element', `${icon}${title}<style>body{}</style>`, '<style'],
    ['a stray comment', `${icon}${title}<!-- note -->`, '<!-- note -->'],
    ['loose text', `${icon}${title}hello`, 'hello'],
    ['a script in capitals', `${icon}${title}<SCRIPT SRC="/x.js"></SCRIPT>`, '<SCRIPT'],
  ])('refuses %s', (_name, inner, mention) => {
    const got = headMarkersProblem(region(inner))
    expect(got).not.toBeNull()
    expect(got).toContain('between the markers')
    expect(got).toContain(mention)
  })

  it('does not look outside the markers: a script after the closing marker is fine', () => {
    const html = `<head>${OPEN}${icon}${title}${CLOSE}<script type="module" src="/a.js"></script></head>`
    expect(headMarkersProblem(html)).toBeNull()
  })

  it('reports a marker problem before a content problem', () => {
    expect(headMarkersProblem(`${OPEN}<script></script>`)).toContain('closing')
  })
})
