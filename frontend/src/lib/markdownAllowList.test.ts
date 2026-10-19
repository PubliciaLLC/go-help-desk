import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { createElement } from 'react'
import {
  ALLOWED_ELEMENTS,
  ALLOWED_PROTOCOLS,
  CODE_LANGUAGES,
  MARKDOWN_ALLOW_LIST,
  markdownSanitizeSchema,
} from './markdownAllowList'
import { markdownToHast } from './markdownCore'
import { markdownComponents, renderHast } from './markdownComponents'
import { TicketText } from '@/components/TicketText'

// #443: one constant, three consumers (the sanitizer, the renderer, the
// editor's schema). These tests are what stops them drifting apart: adding an
// element in one place fails until it is added in all of them.

// The Markdown path is the real pipeline (core, then the page's component map),
// rendered to static markup. TicketText itself formats asynchronously, so it is
// used here only for the plain path, which has nothing to wait for.
const html = (text: string, format: 'markdown' | 'plain' = 'markdown') =>
  format === 'plain'
    ? renderToStaticMarkup(createElement(TicketText, { text, format }))
    : renderToStaticMarkup(createElement('div', { 'data-md-body': '' }, renderHast(markdownToHast(text))))

// Every construct the allow-list admits, once.
const FIXTURE = [
  '# h1', '## h2', '###### h6', '',
  'para with *em* **strong** ~~del~~ `code` and a [link](https://a.example) and https://auto.example',
  'second line  ', 'hard break above', '',
  '> quote', '',
  '- a', '- b', '', '3. c', '4. d', '',
  '---', '',
  '```bash', 'echo "hi" # c', '```', '',
  '| a | b |', '|---|---|', '| 1 | 2 |',
].join('\n')

describe('markdown allow-list agreement', () => {
  it('the sanitize schema, the renderer and the constant name the same elements', () => {
    const fromConstant = [...ALLOWED_ELEMENTS].sort()
    expect([...(markdownSanitizeSchema.tagNames ?? [])].sort()).toEqual(fromConstant)
    expect(Object.keys(markdownComponents).sort()).toEqual(fromConstant)
  })

  it('the sanitize schema allows exactly the attributes in the constant', () => {
    const attrs = markdownSanitizeSchema.attributes ?? {}
    for (const [tag, allowed] of Object.entries(MARKDOWN_ALLOW_LIST.elements)) {
      const names = (attrs[tag] ?? []).map((a) => (Array.isArray(a) ? a[0] : a)).sort()
      expect(names, tag).toEqual([...allowed.attributes].sort())
    }
    // Nothing is allowed on every element ('*'): that is where style, id and on* would slip in.
    expect(attrs['*'] ?? []).toEqual([])
    expect(markdownSanitizeSchema.clobber).toEqual([])
    expect(markdownSanitizeSchema.strip).toEqual(['script', 'style'])
    expect(markdownSanitizeSchema.protocols?.href).toEqual([...ALLOWED_PROTOCOLS])
  })

  it('allows only http, https and mailto, and the 13 languages', () => {
    expect([...ALLOWED_PROTOCOLS].sort()).toEqual(['http', 'https', 'mailto'])
    expect([...CODE_LANGUAGES].sort()).toEqual(
      ['bash', 'diff', 'go', 'ini', 'javascript', 'json', 'plaintext', 'powershell', 'python', 'sql', 'typescript', 'xml', 'yaml'],
    )
  })

  it('a fixture using every construct renders exactly the allowed set of tags', () => {
    const doc = new DOMParser().parseFromString(html(FIXTURE), 'text/html')
    const seen = new Set<string>()
    doc.body.querySelectorAll('[data-md-body] *').forEach((el) => seen.add(el.tagName.toLowerCase()))
    // div is the scroll box our table component puts around a table.
    const allowed = new Set<string>([...ALLOWED_ELEMENTS, 'div'])
    for (const tag of seen) expect(allowed.has(tag), tag).toBe(true)
    // And the fixture does exercise them (headings all become h4, so h1-h3, h5, h6 are absent).
    for (const tag of ['p', 'blockquote', 'ul', 'ol', 'li', 'pre', 'hr', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'em', 'strong', 'del', 'code', 'br', 'a', 'span', 'h4']) {
      expect(seen.has(tag), `${tag} should appear`).toBe(true)
    }
  })

  it('all six heading levels render as one small h4', () => {
    const doc = new DOMParser().parseFromString(html('# a\n\n## b\n\n###### f'), 'text/html')
    const hs = Array.from(doc.body.querySelectorAll('h1,h2,h3,h4,h5,h6'))
    expect(hs.map((h) => h.tagName)).toEqual(['H4', 'H4', 'H4'])
    expect(hs[0].getAttribute('class')).toContain('text-sm')
  })

  it('<server01> and <b>x</b> survive as text: the raw-to-text step runs before sanitize', () => {
    const doc = new DOMParser().parseFromString(html('hello <server01> world and <b>x</b>'), 'text/html')
    const body = doc.querySelector('[data-md-body]')!
    expect(body.textContent).toContain('hello <server01> world')
    expect(body.textContent).toContain('<b>x</b>')
    expect(body.querySelector('b, server01')).toBeNull()
  })

  it('a two-line paragraph renders on two lines (soft breaks survive)', () => {
    const doc = new DOMParser().parseFromString(html('line one\nline two'), 'text/html')
    const p = doc.querySelector('[data-md-body] p')!
    expect(p.textContent).toBe('line one\nline two')
    expect(p.getAttribute('class')).toContain('whitespace-pre-line')
  })

  it('an image renders as the link "[image: a]" and no img', () => {
    const doc = new DOMParser().parseFromString(html('![a](https://t.example/p.gif)'), 'text/html')
    expect(doc.querySelector('img')).toBeNull()
    expect(doc.querySelector('a')!.textContent).toBe('[image: a]')
  })

  it('a link definition line is consumed and not shown (known loss, pinned)', () => {
    const doc = new DOMParser().parseFromString(html('before\n\n[1]: https://a.example\n\nafter'), 'text/html')
    expect(doc.querySelector('[data-md-body]')!.textContent).not.toContain('https://a.example')
  })

  it('highlighting yields hljs-* spans and keeps only the language class on code', () => {
    const doc = new DOMParser().parseFromString(html('```bash\necho "hi" # c\n```'), 'text/html')
    const code = doc.querySelector('pre code')!
    expect(code.getAttribute('class')).toContain('language-bash')
    const spans = Array.from(code.querySelectorAll('span'))
    expect(spans.length).toBeGreaterThan(0)
    for (const s of spans) expect(s.getAttribute('class')).toMatch(/^hljs-[a-z_]+$/)
  })

  it('an unknown language is left unhighlighted; no language is not guessed', () => {
    const unknown = new DOMParser().parseFromString(html('```brainfuck\n+++\n```'), 'text/html')
    expect(unknown.querySelectorAll('pre code span')).toHaveLength(0)
    const none = new DOMParser().parseFromString(html('```\nfunction f() { return "x" }\n```'), 'text/html')
    expect(none.querySelectorAll('pre code span')).toHaveLength(0)
  })

  it('a plain record renders a single whitespace-pre-wrap paragraph with no link, even for Markdown text', () => {
    const doc = new DOMParser().parseFromString(html('# not a heading\n\n[x](https://a.example)', 'plain'), 'text/html')
    expect(doc.querySelectorAll('a, h1, h4')).toHaveLength(0)
    const ps = doc.querySelectorAll('p')
    expect(ps).toHaveLength(1)
    expect(ps[0].getAttribute('class')).toContain('whitespace-pre-wrap')
  })
})
