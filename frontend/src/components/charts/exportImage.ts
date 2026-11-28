// Image export (#131): the screen as the browser laid it out, repainted on a
// canvas. There is one layout, the page's CSS. readBoxes reads it back as
// boxes (fills, borders, text lines, one image per chart svg) and paintPng
// draws them. No image library, no Blob and no object URL: the CSP allows
// img-src 'self' data: and nothing else, and attachments are download-only
// (CLAUDE.md, #165), so every URL here is a data: URL.

export type Box =
  | { kind: 'fill'; x: number; y: number; w: number; h: number; color: string; radius: number }
  | { kind: 'border'; x: number; y: number; w: number; h: number; color: string; width: number; radius: number }
  | { kind: 'text'; x: number; y: number; h: number; text: string; font: string; color: string }
  | { kind: 'image'; x: number; y: number; w: number; h: number; src: string }

// Width in CSS px of text set in a CSS font shorthand: the canvas's
// measureText in the browser, a fixed-width fake in tests.
export type Measure = (text: string, font: string) => number

const SVG_PROPS = ['fill', 'fill-opacity', 'stroke', 'stroke-width', 'stroke-opacity', 'opacity', 'vector-effect']
const SVG_PREFIX = 'data:image/svg+xml;charset=utf-8,'
const SKIP = '.sr-only, [data-export="skip"]'

// A copy of the chart's svg that draws the same without the page's CSS: the
// computed paint properties (colours resolved, so currentColor and the tone
// classes are rgb()) are copied onto each element as attributes, and class and
// style are removed. The charts hold no text, so no font is needed.
export function inlineSvg(svg: SVGSVGElement, widthPx: number, heightPx: number): string {
  const clone = svg.cloneNode(true) as SVGSVGElement
  const from = [svg, ...Array.from(svg.querySelectorAll('*'))]
  const to = [clone, ...Array.from(clone.querySelectorAll('*'))]
  from.forEach((el, i) => {
    const cs = window.getComputedStyle(el)
    for (const prop of SVG_PROPS) {
      const v = cs.getPropertyValue(prop)
      if (v) to[i].setAttribute(prop, v)
    }
    to[i].removeAttribute('class')
    to[i].removeAttribute('style')
  })
  // No xmlns attribute is set by hand: the serializer writes the namespace
  // declaration itself, and an attribute as well makes it appear twice.
  clone.setAttribute('width', String(widthPx))
  clone.setAttribute('height', String(heightPx))
  return SVG_PREFIX + encodeURIComponent(new XMLSerializer().serializeToString(clone))
}

// The lines of a text node as the browser broke them, from one rect per
// character: characters whose top is within 1 px of the line's first are one
// line, x is the first character's left, trailing white space is dropped and a
// line of nothing but white space is dropped.
export function linesFromCharRects(
  text: string,
  rects: { left: number; top: number; height: number }[],
): { text: string; x: number; y: number; h: number }[] {
  const out: { text: string; x: number; y: number; h: number }[] = []
  let cur: { text: string; x: number; y: number; h: number } | null = null
  for (let i = 0; i < rects.length; i++) {
    const r = rects[i]
    if (cur && Math.abs(r.top - cur.y) <= 1) {
      cur.text += text[i]
    } else {
      if (cur) out.push(cur)
      cur = { text: text[i], x: r.left, y: r.top, h: r.height }
    }
  }
  if (cur) out.push(cur)
  return out
    .map((l) => ({ ...l, text: l.text.trimEnd() }))
    .filter((l) => l.text.trim() !== '')
}

// Only an alpha of zero is transparent. The alpha is the fourth comma argument
// of rgba()/hsla() or what follows "/" in the space-separated form; the last
// channel of an opaque colour, such as black as rgb(0, 0, 0), is not one.
const ZERO_ALPHA = /^(?:(?:rgba|hsla)\([^,)]*,[^,)]*,[^,)]*,|[^/)]*\/)\s*0(?:\.0+)?%?\s*\)$/

export function isTransparent(color: string): boolean {
  return color === '' || color === 'transparent' || ZERO_ALPHA.test(color)
}

function noLayout(r: DOMRect): boolean {
  return r.width === 0 && r.height === 0
}

// What to paint for the subtree under root, in document order, in CSS px
// relative to root's top left. See the design for what is skipped and why.
export function readBoxes(root: HTMLElement, measure: Measure): Box[] {
  const origin = root.getBoundingClientRect()
  const boxes: Box[] = []
  const range = document.createRange()

  const text = (node: Text, parent: Element, cs: CSSStyleDeclaration) => {
    const data = node.data
    if (data.trim() === '') return
    const font = ['font-style', 'font-weight', 'font-size', 'font-family'].map((p) => cs.getPropertyValue(p)).join(' ')
    const color = cs.getPropertyValue('color')

    if (cs.getPropertyValue('text-overflow') === 'ellipsis' && parent.scrollWidth > parent.clientWidth) {
      range.setStart(node, 0)
      range.setEnd(node, data.length)
      const r = range.getBoundingClientRect()
      const full = data.replace(/\s+/g, ' ').trim()
      let cut = full.length
      while (cut > 0 && measure(full.slice(0, cut).trimEnd() + '…', font) > parent.clientWidth) cut--
      boxes.push({
        kind: 'text',
        x: r.left - origin.left,
        y: r.top - origin.top,
        h: r.height,
        text: full.slice(0, cut).trimEnd() + '…',
        font,
        color,
      })
      return
    }

    // One rect per character. A character the browser did not lay out
    // (collapsed white space) has an empty rect and is not painted.
    let kept = ''
    const rects: { left: number; top: number; height: number }[] = []
    for (let i = 0; i < data.length; i++) {
      range.setStart(node, i)
      range.setEnd(node, i + 1)
      const r = range.getBoundingClientRect()
      if (noLayout(r)) continue
      kept += data[i]
      rects.push({ left: r.left - origin.left, top: r.top - origin.top, height: r.height })
    }
    for (const l of linesFromCharRects(kept, rects)) {
      boxes.push({ kind: 'text', x: l.x, y: l.y, h: l.h, text: l.text, font, color })
    }
  }

  const visit = (el: Element) => {
    if (el.matches(SKIP)) return
    const cs = window.getComputedStyle(el)
    if (cs.getPropertyValue('visibility') === 'hidden' || cs.getPropertyValue('display') === 'none') return
    const r = el.getBoundingClientRect()
    const x = r.left - origin.left
    const y = r.top - origin.top

    if (el.localName === 'svg') {
      boxes.push({ kind: 'image', x, y, w: r.width, h: r.height, src: inlineSvg(el as SVGSVGElement, r.width, r.height) })
      return
    }

    const radius = parseFloat(cs.getPropertyValue('border-top-left-radius')) || 0
    if (r.width > 0 && r.height > 0) {
      const bg = cs.getPropertyValue('background-color')
      if (!isTransparent(bg)) boxes.push({ kind: 'fill', x, y, w: r.width, h: r.height, color: bg, radius })
      const width = parseFloat(cs.getPropertyValue('border-top-width')) || 0
      const bc = cs.getPropertyValue('border-top-color')
      if (width > 0 && !isTransparent(bc)) {
        boxes.push({ kind: 'border', x, y, w: r.width, h: r.height, color: bc, width, radius })
      }
    }

    el.childNodes.forEach((child) => {
      if (child.nodeType === Node.ELEMENT_NODE) visit(child as Element)
      else if (child.nodeType === Node.TEXT_NODE) text(child as Text, el, cs)
    })
  }

  visit(root)
  return boxes
}

const CONTEXT_FONT = '12px ui-sans-serif, system-ui, sans-serif'
const CONTEXT_COLOR = '#6b7280' // theme-ok: the export is always light, never themed
const CONTEXT_RULE = '#e5e7eb' // theme-ok: the export is always light, never themed
const PAD = 12
const LINE = 16

// The strip under every image that says which period and filters it shows:
// 12 px muted text, one paragraph per entry of lines, wrapped at spaces to the
// width, with 12 px padding and a one-pixel rule on top. top is where the strip
// starts (the height of what it goes under).
export function contextBox(
  lines: string[],
  widthPx: number,
  measure: Measure,
  top = 0,
): { boxes: Box[]; h: number } {
  const room = widthPx - 2 * PAD
  const wrapped: string[] = []
  for (const para of lines) {
    let cur = ''
    for (const word of para.split(' ')) {
      const next = cur === '' ? word : `${cur} ${word}`
      if (cur !== '' && measure(next, CONTEXT_FONT) > room) {
        wrapped.push(cur)
        cur = word
      } else {
        cur = next
      }
    }
    wrapped.push(cur)
  }
  const boxes: Box[] = [{ kind: 'fill', x: 0, y: top, w: widthPx, h: 1, color: CONTEXT_RULE, radius: 0 }]
  wrapped.forEach((t, i) => {
    boxes.push({ kind: 'text', x: PAD, y: top + 1 + PAD + i * LINE, h: LINE, text: t, font: CONTEXT_FONT, color: CONTEXT_COLOR })
  })
  return { boxes, h: 1 + PAD + wrapped.length * LINE + PAD }
}

const MAX_SIDE = 16384
// The longest data: URL Chromium will download.
const MAX_URL = 2 * 1024 * 1024

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number) {
  ctx.beginPath()
  if (r > 0 && typeof ctx.roundRect === 'function') ctx.roundRect(x, y, w, h, r)
  else ctx.rect(x, y, w, h)
}

function render(
  boxes: Box[],
  images: Map<string, HTMLImageElement>,
  w: number,
  h: number,
  background: string,
  scale: number,
): string {
  const canvas = document.createElement('canvas')
  canvas.width = Math.ceil(w * scale)
  canvas.height = Math.ceil(h * scale)
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('canvas is not available')
  ctx.scale(scale, scale)
  ctx.fillStyle = background
  ctx.fillRect(0, 0, w, h)
  for (const b of boxes) {
    if (b.kind === 'fill') {
      ctx.fillStyle = b.color
      roundRect(ctx, b.x, b.y, b.w, b.h, b.radius)
      ctx.fill()
    } else if (b.kind === 'border') {
      // Stroked inside the box, as CSS draws a border.
      ctx.strokeStyle = b.color
      ctx.lineWidth = b.width
      roundRect(ctx, b.x + b.width / 2, b.y + b.width / 2, b.w - b.width, b.h - b.width, Math.max(0, b.radius - b.width / 2))
      ctx.stroke()
    } else if (b.kind === 'text') {
      ctx.font = b.font
      ctx.fillStyle = b.color
      ctx.textBaseline = 'middle'
      ctx.fillText(b.text, b.x, b.y + b.h / 2)
    } else {
      const img = images.get(b.src)
      if (img) ctx.drawImage(img, b.x, b.y, b.w, b.h)
    }
  }
  const url = canvas.toDataURL('image/png')
  if (!url.startsWith('data:image/png')) throw new Error('the image is too large to make')
  return url
}

// Paints the boxes on a canvas and returns a PNG data: URL. At 2x unless that
// would pass 16384 px a side; if the URL is over 2 MiB it is painted again at
// 1x, and rejected if still over. Rejects on any failure; nothing is retried.
export async function paintPng(boxes: Box[], widthPx: number, heightPx: number, background: string): Promise<string> {
  await document.fonts.ready
  const images = new Map<string, HTMLImageElement>()
  await Promise.all(
    boxes.map(async (b) => {
      if (b.kind !== 'image' || images.has(b.src)) return
      const img = new Image()
      img.src = b.src
      await img.decode()
      images.set(b.src, img)
    }),
  )
  const scale = widthPx * 2 > MAX_SIDE || heightPx * 2 > MAX_SIDE ? 1 : 2
  let url = render(boxes, images, widthPx, heightPx, background, scale)
  if (url.length > MAX_URL && scale === 2) url = render(boxes, images, widthPx, heightPx, background, 1)
  if (url.length > MAX_URL) throw new Error('the image is too large to download')
  return url
}
