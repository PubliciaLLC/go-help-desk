import { afterEach, describe, expect, it, vi } from 'vitest'
import { filterLine } from '@/lib/kpi'
import { contextBox, inlineSvg, linesFromCharRects, paintPng, readBoxes } from './exportImage'
import type { Box } from './exportImage'

type TextBox = Extract<Box, { kind: 'text' }>

// A fixed-width fake: every character is 6 px wide, whatever the font.
const measure = (text: string) => text.length * 6

function stubStyles(styles: Map<Element, Record<string, string>>, fallback: Record<string, string> = {}) {
  vi.spyOn(window, 'getComputedStyle').mockImplementation((el: Element) => {
    const s = { ...fallback, ...(styles.get(el) ?? {}) }
    return new Proxy(
      {},
      {
        get: (_t, prop: string) => {
          if (prop === 'getPropertyValue') return (name: string) => s[name] ?? ''
          // camelCase read, e.g. backgroundColor.
          const kebab = prop.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`)
          return s[kebab] ?? ''
        },
      },
    ) as unknown as CSSStyleDeclaration
  })
}

describe('inlineSvg', () => {
  function svgWith() {
    document.body.innerHTML =
      '<svg viewBox="0 0 100 84" preserveAspectRatio="none" class="block text-chart-blue" style="height:84px">' +
      '<g class="text-chart-blue"><rect class="bar" style="fill:red" x="0" y="5" width="50" height="18" fill="currentColor"/></g></svg>'
    return document.querySelector('svg')! as SVGSVGElement
  }

  it('resolves colours from the computed style and strips class and style', () => {
    const svg = svgWith()
    const rect = svg.querySelector('rect')!
    stubStyles(
      new Map([
        [
          rect,
          {
            fill: 'rgb(37, 99, 235)',
            'fill-opacity': '0.15',
            stroke: 'none',
            'stroke-width': '1px',
            'stroke-opacity': '1',
            opacity: '1',
            'vector-effect': 'non-scaling-stroke',
          },
        ],
      ]),
    )
    const url = inlineSvg(svg, 320, 84)
    const prefix = 'data:image/svg+xml;charset=utf-8,'
    expect(url.startsWith(prefix)).toBe(true)
    const xml = decodeURIComponent(url.slice(prefix.length))
    const out = new DOMParser().parseFromString(xml, 'image/svg+xml')
    const root = out.documentElement
    expect(xml.match(/xmlns="http:\/\/www\.w3\.org\/2000\/svg"/g)).toHaveLength(1)
    expect(root.namespaceURI).toBe('http://www.w3.org/2000/svg')
    expect(root.getAttribute('width')).toBe('320')
    expect(root.getAttribute('height')).toBe('84')
    expect(root.getAttribute('viewBox')).toBe('0 0 100 84')
    expect(root.getAttribute('preserveAspectRatio')).toBe('none')
    expect(out.querySelectorAll('[class]')).toHaveLength(0)
    expect(out.querySelectorAll('[style]')).toHaveLength(0)
    const r = out.querySelector('rect')!
    expect(r.getAttribute('fill')).toBe('rgb(37, 99, 235)')
    expect(r.getAttribute('fill-opacity')).toBe('0.15')
    expect(r.getAttribute('stroke')).toBe('none')
    expect(r.getAttribute('stroke-width')).toBe('1px')
    expect(r.getAttribute('stroke-opacity')).toBe('1')
    expect(r.getAttribute('opacity')).toBe('1')
    expect(r.getAttribute('vector-effect')).toBe('non-scaling-stroke')
  })

  it('leaves the live element alone', () => {
    const svg = svgWith()
    stubStyles(new Map(), { fill: 'rgb(0, 0, 0)' })
    inlineSvg(svg, 10, 10)
    expect(svg.getAttribute('class')).toBe('block text-chart-blue')
    expect(svg.querySelector('rect')!.getAttribute('fill')).toBe('currentColor')
  })
})

describe('linesFromCharRects', () => {
  const r = (left: number, top: number, height = 16) => ({ left, top, height })
  const chars = (n: number, top: number, from = 0) => Array.from({ length: n }, (_, i) => r(from + i * 6, top))

  it.each([
    {
      name: 'one line',
      text: 'Hello',
      rects: chars(5, 10, 8),
      want: [{ text: 'Hello', x: 8, y: 10, h: 16 }],
    },
    {
      name: 'a wrap into two lines',
      text: 'ab cd',
      rects: [...chars(3, 10), ...chars(2, 26)],
      want: [
        { text: 'ab', x: 0, y: 10, h: 16 },
        { text: 'cd', x: 0, y: 26, h: 16 },
      ],
    },
    {
      name: 'a wrap into three lines',
      text: 'a b c',
      rects: [r(0, 0), r(6, 0), r(0, 16), r(6, 16), r(0, 32)],
      want: [
        { text: 'a', x: 0, y: 0, h: 16 },
        { text: 'b', x: 0, y: 16, h: 16 },
        { text: 'c', x: 0, y: 32, h: 16 },
      ],
    },
    {
      name: 'trailing spaces dropped',
      text: 'ab   ',
      rects: chars(5, 0),
      want: [{ text: 'ab', x: 0, y: 0, h: 16 }],
    },
    {
      name: '0.5 px jitter in top stays one line',
      text: 'abc',
      rects: [r(0, 10), r(6, 10.5), r(12, 9.5)],
      want: [{ text: 'abc', x: 0, y: 10, h: 16 }],
    },
    {
      name: 'an all-space line is dropped',
      text: 'a  b',
      rects: [r(0, 0), r(6, 16), r(12, 16), r(0, 32)],
      want: [
        { text: 'a', x: 0, y: 0, h: 16 },
        { text: 'b', x: 0, y: 32, h: 16 },
      ],
    },
    { name: 'no text', text: '', rects: [], want: [] },
  ])('$name', ({ text, rects, want }) => {
    expect(linesFromCharRects(text, rects)).toEqual(want)
  })
})

describe('contextBox', () => {
  const texts = (boxes: Box[]) => boxes.filter((b): b is TextBox => b.kind === 'text').map((b) => b.text)

  it('puts a 12 px muted line under a top border, 12 px padded', () => {
    const { boxes, h } = contextBox(['Created 24 Nov 2026, 10:15'], 400, measure)
    expect(boxes[0]).toMatchObject({ kind: 'fill', x: 0, y: 0, w: 400, h: 1 })
    const [t] = boxes.filter((b): b is TextBox => b.kind === 'text')
    expect(t.x).toBe(12)
    expect(t.font).toContain('12px')
    // 1 border + 12 + one 16px line + 12.
    expect(h).toBe(1 + 12 + 16 + 12)
    expect(t.y).toBe(1 + 12)
    expect(t.h).toBe(16)
  })

  it('wraps at spaces to the width inside the padding', () => {
    // 6 px a character, 100 - 24 = 76 px = 12 characters a line.
    const { boxes, h } = contextBox(['aaaa bbbb cccc dddd'], 100, measure)
    expect(texts(boxes)).toEqual(['aaaa bbbb', 'cccc dddd'])
    expect(h).toBe(1 + 12 + 32 + 12)
  })

  it('never splits a word, and keeps the break between inputs', () => {
    const { boxes } = contextBox(['aaaaaaaaaaaaaaaaaaaa b', 'second'], 100, measure)
    expect(texts(boxes)).toEqual(['aaaaaaaaaaaaaaaaaaaa', 'b', 'second'])
  })

  it('carries the no-filter wording whole', () => {
    const none = filterLine({ category_id: null, priority: [], assignee_group_id: null }, {})
    expect(none).toBe('All categories · All priorities · All groups')
    const { boxes } = contextBox([none], 600, measure)
    expect(texts(boxes)).toEqual([none])
  })

  it('offsets everything by top when asked', () => {
    const { boxes } = contextBox(['x'], 100, measure, 200)
    expect(boxes[0]).toMatchObject({ y: 200 })
    expect((boxes[1] as TextBox).y).toBe(213)
  })
})

describe('readBoxes', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  type Rect = { left: number; top: number; width: number; height: number }
  const rect = ({ left, top, width, height }: Rect): DOMRect =>
    ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON: () => ({}) }) as DOMRect

  const BASE: Record<string, string> = {
    'background-color': 'rgba(0, 0, 0, 0)',
    'border-top-width': '0px',
    'border-top-color': 'rgb(0, 0, 0)',
    visibility: 'visible',
    display: 'block',
    'font-style': 'normal',
    'font-weight': '400',
    'font-size': '16px',
    'font-family': 'Inter',
    color: 'rgb(17, 24, 39)',
    'text-overflow': 'clip',
    'border-top-left-radius': '0px',
  }

  function setup(html: string, rects: Record<string, Rect>, styles: Record<string, Record<string, string>>) {
    document.body.innerHTML = html
    const byId = (id: string) => document.getElementById(id)!
    const styleMap = new Map<Element, Record<string, string>>()
    for (const [id, s] of Object.entries(styles)) styleMap.set(byId(id), s)
    stubStyles(styleMap, BASE)
    for (const [id, r] of Object.entries(rects)) {
      byId(id).getBoundingClientRect = () => rect(r)
    }
    return byId('root')
  }

  // Stubs the per-character Range: the layout gives one rect per character of
  // the text node, and a range over the whole node spans them.
  function stubRange(layout: (text: string) => Rect[]) {
    vi.spyOn(document, 'createRange').mockImplementation(() => {
      let node: Text | null = null
      let a = 0
      let b = 0
      return {
        setStart: (n: Text, o: number) => {
          node = n
          a = o
        },
        setEnd: (_n: Text, o: number) => {
          b = o
        },
        getBoundingClientRect: () => {
          const all = layout(node!.data)
          if (b - a === node!.data.length) {
            const f = all[0]
            const l = all[all.length - 1]
            return rect({ left: f.left, top: f.top, width: l.left + l.width - f.left, height: f.height })
          }
          return rect(all[a])
        },
      } as unknown as Range
    })
  }

  const line = (left: number, top: number) => (text: string) =>
    Array.from(text, (_c, i) => ({ left: left + i * 6, top, width: 6, height: 16 }))

  it('paints background and border of an element, relative to the root', () => {
    const root = setup(
      '<div id="root"><section id="card">x</section></div>',
      { root: { left: 100, top: 50, width: 300, height: 200 }, card: { left: 110, top: 60, width: 280, height: 100 } },
      {
        card: {
          'background-color': 'rgb(255, 255, 255)',
          'border-top-width': '1px',
          'border-top-color': 'rgb(229, 231, 235)',
          'border-top-left-radius': '8px',
        },
      },
    )
    stubRange(line(116, 66))
    const boxes = readBoxes(root, measure)
    expect(boxes).toContainEqual({ kind: 'fill', x: 10, y: 10, w: 280, h: 100, color: 'rgb(255, 255, 255)', radius: 8 })
    expect(boxes).toContainEqual({
      kind: 'border',
      x: 10,
      y: 10,
      w: 280,
      h: 100,
      color: 'rgb(229, 231, 235)',
      width: 1,
      radius: 8,
    })
    // Fill comes before border, and both before the text on top of them.
    expect(boxes.findIndex((b) => b.kind === 'fill')).toBeLessThan(boxes.findIndex((b) => b.kind === 'border'))
    expect(boxes.findIndex((b) => b.kind === 'border')).toBeLessThan(boxes.findIndex((b) => b.kind === 'text'))
  })

  it('paints nothing for a transparent background or a transparent border colour', () => {
    const root = setup(
      '<div id="root"><p id="p"></p></div>',
      { root: { left: 0, top: 0, width: 100, height: 100 }, p: { left: 0, top: 0, width: 100, height: 20 } },
      { p: { 'border-top-width': '2px', 'border-top-color': 'rgba(0, 0, 0, 0)' } },
    )
    expect(readBoxes(root, measure)).toEqual([])
  })

  it.each([
    ['rgb(0, 0, 0)', true],
    ['rgb(255, 255, 0)', true],
    ['rgb(255, 165, 0)', true],
    ['rgba(0, 0, 0, 1)', true],
    ['rgba(0, 0, 0, 0.5)', true],
    ['rgb(0 0 0 / 0.5)', true],
    ['color(srgb 0 0 0 / 0.2)', true],
    ['rgba(0, 0, 0, 0)', false],
    ['rgba(0, 0, 0, 0.0)', false],
    ['rgb(0 0 0 / 0)', false],
    ['rgb(0 0 0 / 0%)', false],
    ['color(srgb 0 0 0 / 0)', false],
    ['transparent', false],
    ['', false],
  ])('paints background and border colour %j: %s', (color, painted) => {
    const root = setup(
      '<div id="root"><p id="p"></p></div>',
      { root: { left: 0, top: 0, width: 100, height: 100 }, p: { left: 0, top: 0, width: 100, height: 20 } },
      { p: { 'background-color': color, 'border-top-width': '2px', 'border-top-color': color } },
    )
    const kinds = readBoxes(root, measure).map((b) => b.kind)
    expect(kinds).toEqual(painted ? ['fill', 'border'] : [])
  })

  it('skips .sr-only, [data-export=skip] and visibility:hidden subtrees', () => {
    const root = setup(
      '<div id="root"><table class="sr-only" id="t"><tbody><tr><td>hidden table</td></tr></tbody></table>' +
        '<button id="b" data-export="skip">Download</button><div id="h">ghost</div><span id="k">kept</span></div>',
      {
        root: { left: 0, top: 0, width: 100, height: 100 },
        t: { left: 0, top: 0, width: 10, height: 10 },
        b: { left: 0, top: 0, width: 50, height: 20 },
        h: { left: 0, top: 20, width: 50, height: 20 },
        k: { left: 0, top: 40, width: 50, height: 20 },
      },
      { h: { visibility: 'hidden' }, b: { 'background-color': 'rgb(1, 2, 3)' } },
    )
    stubRange(line(0, 40))
    const boxes = readBoxes(root, measure)
    expect(boxes).toEqual([
      { kind: 'text', x: 0, y: 40, h: 16, text: 'kept', font: 'normal 400 16px Inter', color: 'rgb(17, 24, 39)' },
    ])
  })

  it('makes an image box of an svg and does not walk into it', () => {
    const root = setup(
      '<div id="root"><svg id="s" viewBox="0 0 100 28"><rect id="r" width="10" height="18"/><title>never</title></svg></div>',
      { root: { left: 10, top: 10, width: 200, height: 100 }, s: { left: 30, top: 20, width: 160, height: 28 } },
      { r: { 'background-color': 'rgb(9, 9, 9)' } },
    )
    stubRange(line(0, 0))
    const boxes = readBoxes(root, measure)
    expect(boxes).toHaveLength(1)
    expect(boxes[0]).toMatchObject({ kind: 'image', x: 20, y: 10, w: 160, h: 28 })
    expect((boxes[0] as Extract<Box, { kind: 'image' }>).src.startsWith('data:image/svg+xml;charset=utf-8,')).toBe(true)
  })

  it('emits one text box per browser line, wrapped where the screen wraps', () => {
    const root = setup(
      '<div id="root"><p id="p">ab cd</p></div>',
      { root: { left: 0, top: 0, width: 100, height: 100 }, p: { left: 0, top: 0, width: 20, height: 32 } },
      { p: { 'font-weight': '600', color: 'rgb(0, 0, 255)' } },
    )
    stubRange(() => [
      { left: 0, top: 0, width: 6, height: 16 },
      { left: 6, top: 0, width: 6, height: 16 },
      { left: 12, top: 0, width: 6, height: 16 },
      { left: 0, top: 16, width: 6, height: 16 },
      { left: 6, top: 16, width: 6, height: 16 },
    ])
    expect(readBoxes(root, measure)).toEqual([
      { kind: 'text', x: 0, y: 0, h: 16, text: 'ab', font: 'normal 600 16px Inter', color: 'rgb(0, 0, 255)' },
      { kind: 'text', x: 0, y: 16, h: 16, text: 'cd', font: 'normal 600 16px Inter', color: 'rgb(0, 0, 255)' },
    ])
  })

  it('drops characters the browser did not lay out (collapsed white space)', () => {
    const root = setup(
      '<div id="root"><p id="p">a  b</p></div>',
      { root: { left: 0, top: 0, width: 100, height: 100 }, p: { left: 0, top: 0, width: 50, height: 16 } },
      {},
    )
    stubRange(() => [
      { left: 0, top: 0, width: 6, height: 16 },
      { left: 6, top: 0, width: 3, height: 16 },
      { left: 0, top: 0, width: 0, height: 0 },
      { left: 9, top: 0, width: 6, height: 16 },
    ])
    const [t] = readBoxes(root, measure) as TextBox[]
    expect(t.text).toBe('a b')
  })

  it('cuts an overflowing text-overflow:ellipsis label with … to the parent width', () => {
    const root = setup(
      '<div id="root"><div id="l">Very long category name</div></div>',
      { root: { left: 0, top: 0, width: 200, height: 100 }, l: { left: 0, top: 0, width: 60, height: 28 } },
      { l: { 'text-overflow': 'ellipsis' } },
    )
    const l = document.getElementById('l')!
    Object.defineProperty(l, 'scrollWidth', { value: 130 })
    Object.defineProperty(l, 'clientWidth', { value: 60 })
    stubRange(line(0, 6))
    // 60 px at 6 px a character is 10 characters, one of them the ellipsis.
    expect(readBoxes(root, measure)).toEqual([
      { kind: 'text', x: 0, y: 6, h: 16, text: 'Very long…', font: 'normal 400 16px Inter', color: 'rgb(17, 24, 39)' },
    ])
  })

  it('does not cut an ellipsis label that fits', () => {
    const root = setup(
      '<div id="root"><div id="l">Short</div></div>',
      { root: { left: 0, top: 0, width: 200, height: 100 }, l: { left: 0, top: 0, width: 60, height: 28 } },
      { l: { 'text-overflow': 'ellipsis' } },
    )
    const l = document.getElementById('l')!
    Object.defineProperty(l, 'scrollWidth', { value: 30 })
    Object.defineProperty(l, 'clientWidth', { value: 60 })
    stubRange(line(0, 6))
    const boxes = readBoxes(root, measure) as TextBox[]
    expect(boxes.map((b) => b.text)).toEqual(['Short'])
  })
})

describe('paintPng', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('rejects when an image cannot be decoded, and makes no PNG', async () => {
    class BadImage {
      src = ''
      decode() {
        return Promise.reject(new Error('decode failed'))
      }
    }
    vi.stubGlobal('Image', BadImage)
    Object.defineProperty(document, 'fonts', { value: { ready: Promise.resolve() }, configurable: true })
    const toDataURL = vi.fn()
    vi.spyOn(HTMLCanvasElement.prototype, 'toDataURL').mockImplementation(toDataURL)
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as unknown as CanvasRenderingContext2D)
    const boxes: Box[] = [
      { kind: 'image', x: 0, y: 0, w: 10, h: 10, src: 'data:image/svg+xml;charset=utf-8,%3Csvg%2F%3E' },
    ]
    await expect(paintPng(boxes, 100, 100, '#fff')).rejects.toThrow('decode failed')
    expect(toDataURL).not.toHaveBeenCalled()
  })
})
