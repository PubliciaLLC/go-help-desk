/* eslint-disable no-restricted-syntax -- the crafted trees below contain the banned name, as data */
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { renderHast } from './markdownComponents'
import type { Root } from 'hast'

// #443 security review: the page does not trust what the worker sends. These are
// trees a compromised or confused worker (or a bug) could produce, as they would
// arrive after structured clone. Each must either be refused (renderHast throws,
// and the post falls back to its source) or render nothing dangerous.

// Trees as they would arrive after structured clone (JSON.parse gives own __proto__ keys too).
const el = (tagName: string, properties: unknown, children: unknown[] = [{ type: 'text', value: 'x' }]) =>
  ({ type: 'root', children: [{ type: 'element', tagName, properties, children }] }) as unknown as Root
const crafted: [string, Root][] = [
  ['img', el('img', { src: 'https://t.example/x' })],
  ['script', el('script', {})],
  ['dangerouslySetInnerHTML on p', el('p', { dangerouslySetInnerHTML: { __html: '<img src=x onerror=alert(1)>' } })],
  ['style on p', el('p', { style: 'background:url(https://t.example)' })],
  ['onClick on a', el('a', { href: 'https://ok.example', onClick: 'alert(1)' })],
  ['own __proto__ (JSON)', JSON.parse('{"type":"root","children":[{"type":"element","tagName":"p","properties":{"__proto__":{"dangerouslySetInnerHTML":{"__html":"<img src=x>"}}},"children":[]}]}')],
  ['constructor key', el('p', { constructor: { prototype: { polluted: 1 } } })],
  ['children key', el('p', { children: { type: 'x' } })],
  ['href array', el('a', { href: ['javascript:alert(1)'] })],
  ['href javascript', el('a', { href: 'javascript:alert(1)' })],
  ['href object toString', el('a', { href: { toString: () => 'javascript:alert(1)' } })],
  ['className hostile on span', el('span', { className: ['hljs-x', 'evil', 'onclick=1'] })],
  ['className string on code', el('code', { className: 'language-bash evil' })],
  ['start string', el('ol', { start: '1" onclick="x' })],
  ['raw node', { type: 'root', children: [{ type: 'raw', value: '<img src=x onerror=alert(1)>' }] } as unknown as Root],
  ['comment node', { type: 'root', children: [{ type: 'comment', value: 'x' }] } as unknown as Root],
  ['doctype', { type: 'root', children: [{ type: 'doctype' }] } as unknown as Root],
  ['mdxJsx', { type: 'root', children: [{ type: 'mdxJsxFlowElement', name: 'img', attributes: [{ type: 'mdxJsxAttribute', name: 'src', value: 'x' }], children: [] }] } as unknown as Root],
  ['text value object', { type: 'root', children: [{ type: 'text', value: { $$typeof: 'react.element', type: 'img', props: { src: 'x' } } }] } as unknown as Root],
  ['nested root in element', el('p', {}, [{ type: 'root', children: [{ type: 'element', tagName: 'img', properties: {}, children: [] }] }])],
  ['svg tagName case', el('SVG', {})],
  ['tagName with space', el('p onclick', {})],
  ['element with no properties', { type: 'root', children: [{ type: 'element', tagName: 'p', children: [] }] } as unknown as Root],
  ['element with no children', { type: 'root', children: [{ type: 'element', tagName: 'p', properties: {} }] } as unknown as Root],
  ['properties null', el('p', null)],
  ['properties array', el('p', ['style'])],
]

describe('renderHast boundary', () => {
  it.each(crafted)('%s', (_n, tree) => {
    let root: Element | null = null
    try {
      const { container } = render(<div data-x>{renderHast(structuredClone(tree))}</div>)
      root = container.querySelector('[data-x]')
    } catch {
      return // refused: the caller shows the source instead
    }
    expect(root!.querySelectorAll('img,script,svg,[style],[src]').length).toBe(0)
    for (const el of Array.from(root!.querySelectorAll('*'))) {
      expect(el.getAttributeNames().some((n) => n.startsWith('on')), el.outerHTML).toBe(false)
    }
    const hrefs = Array.from(root!.querySelectorAll('a')).map((a) => a.getAttribute('href') ?? '')
    expect(hrefs.every((h) => /^(https?|mailto):/.test(h))).toBe(true)
    expect(({} as Record<string, unknown>).polluted).toBeUndefined()
    expect(root!.innerHTML).not.toMatch(/evil|onclick|\$\$typeof/)
  })
})
