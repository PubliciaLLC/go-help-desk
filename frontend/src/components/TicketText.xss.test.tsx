import { describe, expect, it } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { renderNow } from '@/test/formatted'
import { TicketText } from './TicketText'
import { ALLOWED_ELEMENTS, ALLOWED_PROTOCOLS } from '@/lib/markdownAllowList'

// #443: requesters, guests included, write text that staff read on the origin
// that holds staff sessions. These tests are the guarantee that the renderer
// cannot be talked into script, a live element it did not make, or a URL
// scheme outside the allow-list. The check is general (every element, every
// attribute, every href), so a hostile input needs only to be added to the list.

// The attributes an element in the rendered body may carry. href, start and
// class are the only ones that can be influenced by the input; the rest are
// ours and are never read from the Markdown.
const OK_ATTRS = new Set(['href', 'rel', 'target', 'tabindex', 'start', 'class', 'dir'])
const OK_CLASS = /^(hljs-[a-z_]+|language-[a-z0-9]+)$/

function assertSafe(body: Element, input: string) {
  const allowedTags = new Set([...ALLOWED_ELEMENTS, 'div'])
  for (const el of Array.from(body.querySelectorAll('*'))) {
    const tag = el.tagName.toLowerCase()
    expect(allowedTags.has(tag), `<${tag}> from ${JSON.stringify(input)}`).toBe(true)
    for (const attr of Array.from(el.attributes)) {
      const ok = OK_ATTRS.has(attr.name) || attr.name.startsWith('data-')
      expect(ok, `${tag}[${attr.name}] from ${JSON.stringify(input)}`).toBe(true)
      expect(attr.name.startsWith('on')).toBe(false)
      if (attr.name === 'href') {
        let url: URL | null = null
        try {
          url = new URL(attr.value)
        } catch {
          /* not a URL */
        }
        expect(url, `href ${attr.value} from ${JSON.stringify(input)}`).not.toBeNull()
        expect(ALLOWED_PROTOCOLS).toContain(url!.protocol.replace(':', ''))
      }
      if (attr.name === 'class' && (tag === 'code' || tag === 'span')) {
        // Class on code and span is the closed highlight list, nothing else.
        for (const c of attr.value.split(/\s+/).filter(Boolean)) {
          expect(OK_CLASS.test(c) || /^[a-z0-9-:[\]/.]+$/.test(c), `class ${c}`).toBe(true)
          expect(c).not.toMatch(/onclick|=|"/)
        }
      }
    }
  }
}

// The formatted body, which must exist: a silent fall-back to plain text would
// make every "nothing dangerous in here" check below pass for the wrong reason.
async function renderBody(text: string, format: 'markdown' | 'plain' = 'markdown') {
  const { container } = await renderNow(<TicketText text={text} format={format} />)
  const body = container.querySelector('[data-md-body]')
  expect(body, `no formatted body for ${JSON.stringify(text.slice(0, 40))}`).not.toBeNull()
  return body!
}

// Whatever it ends up as, for inputs that are expected to be refused.
async function renderAny(text: string) {
  const { container } = await renderNow(<TicketText text={text} format="markdown" />)
  return container
}

const long = (s: string, n: number) => s.repeat(n)

// [name, input, literal text that must still be visible (if any)]
const corpus: [string, string, string?][] = [
  ['javascript: scheme', '[x](javascript:alert(1))'],
  ['javascript: mixed case', '[x](JaVaScRiPt:alert(1))'],
  ['javascript: entity-encoded', '[x](&#106;avascript:alert(1))'],
  ['javascript: entity-encoded hex', '[x](&#x6A;avascript:alert(1))'],
  ['javascript: with a tab inside', '[x](java\tscript:alert(1))'],
  ['javascript: with a newline inside', '[x](java\nscript:alert(1))'],
  ['javascript: percent-encoded newline', '[x](java%0ascript:alert(1))'],
  ['javascript: with leading spaces', '[x](   javascript:alert(1))'],
  ['javascript: in angle brackets', '[x](<javascript:alert(1)>)'],
  ['javascript: autolink', '<javascript:alert(1)>'],
  ['data:text/html', '[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)'],
  ['data:image/svg+xml', '[x](data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+)'],
  ['vbscript:', '[x](vbscript:msgbox(1))'],
  ['file:', '[x](file:///etc/passwd)'],
  ['tel:', '[x](tel:+15551234567)'],
  ['relative url', '[x](/admin/users)'],
  ['fragment', '[x](#top)'],
  ['protocol-relative', '[x](//evil.example/x)'],
  ['<script>', '<script>alert(1)</script>', '<script>alert(1)</script>'],
  ['<img onerror>', '<img src=x onerror=alert(1)>', '<img src=x onerror=alert(1)>'],
  ['<svg onload>', '<svg onload=alert(1)>', '<svg onload=alert(1)>'],
  ['<iframe>', '<iframe src="https://evil.example"></iframe>', '<iframe src="https://evil.example"></iframe>'],
  ['<style>', '<style>body{display:none}</style>', '<style>body{display:none}</style>'],
  ['<a href=javascript:>', '<a href="javascript:alert(1)">click</a>', '<a href="javascript:alert(1)">click</a>'],
  ['<b> wrapped round text', 'a <b>bold</b> b', '<b>bold</b>'],
  ['<server01> is kept', 'host <server01> is down', '<server01>'],
  ['a link nested in link text', '[a [b](https://one.example)](https://two.example)'],
  ['link reference definition to javascript:', '[x][r]\n\n[r]: javascript:alert(1)'],
  ['www. with a script tag', 'www.x.com/<script>alert(1)</script>'],
  ['image inside a link', '[![a](https://t.example/p.gif)](https://l.example)'],
  ['image with data: src', '![a](data:image/gif;base64,R0lGODlhAQABAAAAACw=)'],
  ['image with javascript: src', '![a](javascript:alert(1))'],
  ['footnote', 'text[^1]\n\n[^1]: note [x](javascript:alert(1))'],
  ['task list', '- [x] done\n- [ ] todo'],
  ['code fence with a hostile info string', '```x" onclick="y\ncode\n```'],
  ['code fence with a class-looking info string', '```language-bash hljs-keyword\ncode\n```'],
  ['html comment', 'a <!-- <script>alert(1)</script> --> b'],
  ['right-to-left override in link text', '[‮gpj.exe](https://evil.example/x)'],
  ['table with alignment', '| a | b |\n|:-:|--:|\n| 1 | 2 |'],
  ['heading', '# <script>alert(1)</script>'],
  ['mailto', '[m](mailto:a@example.com?subject=x)'],
  ['raw html table', '<table onclick=alert(1)><tr><td>x</td></tr></table>', '<table onclick=alert(1)>'],
  ['style attribute', '<p style="position:fixed;inset:0">overlay</p>', '<p style="position:fixed;inset:0">'],
]

describe('TicketText: hostile input', () => {
  it.each(corpus)('markdown: %s', async (_name, input, literal) => {
    const body = await renderBody(input)
    assertSafe(body, input)
    if (literal) expect(body.textContent).toContain(literal)
  })

  // Same component, same pipeline: a plain post shown formatted goes through
  // the identical renderer, so the corpus must hold on that path too.
  it.each(corpus)('plain post, shown formatted: %s', async (_name, input, literal) => {
    const { container } = render(<TicketText text={input} format="plain" />)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /show formatted/i }))
    })
    const body = container.querySelector('[data-md-body]')!
    expect(body).not.toBeNull()
    assertSafe(body, input)
    if (literal) expect(body.textContent).toContain(literal)
  })

  it('a plain post is never rendered: no links, no elements, even for Markdown text', () => {
    const { container } = render(<TicketText text={'[x](https://a.example) **b** <i>x</i>'} format="plain" />)
    expect(container.querySelectorAll('a, strong, i, em')).toHaveLength(0)
    expect(container.textContent).toContain('[x](https://a.example) **b** <i>x</i>')
  })

  it('the link refused by the allow-list is shown as its text, not as a link', async () => {
    const body = await renderBody('[click me](javascript:alert(1))')
    expect(body.querySelector('a')).toBeNull()
    expect(body.textContent).toContain('click me')
  })

  it('an image is never loaded: it becomes the link "[image: alt]"', async () => {
    const body = await renderBody('![a cat](https://t.example/p.gif)')
    expect(body.querySelector('img')).toBeNull()
    const a = body.querySelector('a')!
    expect(a.getAttribute('href')).toBe('https://t.example/p.gif')
    expect(a.textContent).toBe('[image: a cat]')
  })

  it('an image with a hostile src is not a link at all', async () => {
    const body = await renderBody('![a](javascript:alert(1))')
    expect(body.querySelector('img, a')).toBeNull()
  })

  it('a code fence info string cannot set attributes', async () => {
    const body = await renderBody('```evil onclick=1\ncode\n```')
    for (const el of Array.from(body.querySelectorAll('*'))) {
      expect(el.getAttributeNames().some((n) => n.startsWith('on'))).toBe(false)
    }
    expect(body.querySelector('code')!.getAttributeNames().filter((n) => n !== 'class')).toEqual([])
  })

  it('links open in a new tab with a restrictive rel', async () => {
    const body = await renderBody('[x](https://a.example)')
    const a = body.querySelector('a')!
    expect(a.getAttribute('target')).toBe('_blank')
    expect(a.getAttribute('rel')).toBe('noopener noreferrer nofollow ugc')
  })

  it('64 KB of nesting renders in under a second without throwing', async () => {
    for (const unit of ['>', '*', '[', '- ', '>*']) {
      const input = long(unit, 65536 / unit.length)
      const t0 = performance.now()
      await renderAny(input)
      expect(performance.now() - t0, `unit ${JSON.stringify(unit)}`).toBeLessThan(1000)
    }
  })
})

describe('TicketText: text too complex to format', () => {
  it('is shown as plain text with a note, not parsed', async () => {
    const { container } = await renderNow(<TicketText text={'a*'.repeat(20000)} format="markdown" />)
    expect(container.querySelector('[data-md-body]')).toBeNull()
    expect(container.textContent).toContain('too complex to format')
    expect(container.textContent).toContain('a*a*a*')
  })
})
