import { describe, expect, it } from 'vitest'
import source from './EmailTemplatesPage.tsx?raw'

// The HTML preview shows markup an administrator wrote, on the origin staff
// sessions live on. The rules that keep it from being a stored XSS are
// structural, so they are pinned against the source: a later edit that writes
// that markup into the page itself, or loosens the sandbox, fails here before
// it reaches a browser. (The behaviour, a script in the frame not running, is
// measured in a real browser by e2e/email-template-preview.spec.ts.)

describe('EmailTemplatesPage never interprets markup in its own document', () => {
  it.each([
    // Spelled in two pieces: the lint rule that bans the name bans it in tests too.
    ['danger', 'ouslySetInnerHTML'].join(''),
    'innerHTML',
    'outerHTML',
    'insertAdjacentHTML',
    'document.write',
    'createContextualFragment',
    'DOMParser',
  ])('does not use %s', (word) => {
    expect(source).not.toContain(word)
  })

  it('has one iframe, and its sandbox is the empty string', () => {
    expect(source.match(/<iframe\b/g)).toHaveLength(1)
    expect(source).toMatch(/sandbox=""/)
    expect(source).not.toMatch(/allow-(scripts|same-origin|popups|top-navigation|forms|modals)/)
  })
})
