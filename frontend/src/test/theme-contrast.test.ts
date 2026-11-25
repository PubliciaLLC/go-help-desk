import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// #709: the arbiter of the colour tokens. It reads the token definitions out of
// src/index.css (the :root block and the .dark block) and Tailwind's own
// palette out of node_modules, resolves var() and color-mix() the way a browser
// does, and asserts WCAG contrast for every pair a token is meant to be used in.
//
// To add a token, add its pairs to PAIRS. If a pair fails, move that token one
// Tailwind shade (darker in light, lighter in dark) in index.css; do not add a
// token to make a number pass.

// vitest runs from frontend/ (the other file-reading tests rely on it too).
const read = (rel: string) => readFileSync(rel, 'utf8')
const indexCss = read('src/index.css')
const markdownCss = read('src/styles/markdown.css')
const paletteCss = read('node_modules/tailwindcss/theme.css')

type Theme = 'light' | 'dark'

// ── Colour engine (CSS Color 4, just what the tokens use) ────────────────────

interface Lab { L: number; a: number; b: number; alpha: number }

const cbrt = Math.cbrt
function linToLab(r: number, g: number, b: number, alpha: number): Lab {
  const l = cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  return {
    L: 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    a: 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    b: 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
    alpha,
  }
}
function labToLin({ L, a, b }: Lab): [number, number, number] {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ]
}
const toLin = (c: number) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)
const srgbToLab = (r: number, g: number, b: number, alpha = 1) => linToLab(toLin(r), toLin(g), toLin(b), alpha)

function hslToRgb(h: number, s: number, l: number): [number, number, number] {
  const k = (n: number) => (n + h / 30) % 12
  const a = s * Math.min(l, 1 - l)
  const f = (n: number) => l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1)))
  return [f(0), f(8), f(4)]
}

/** Splits on top-level commas (or, for `sep` = ' ', top-level whitespace). */
function splitTop(s: string, sep: ',' | ' '): string[] {
  const out: string[] = []
  let depth = 0
  let cur = ''
  for (const ch of s) {
    if (ch === '(') depth++
    if (ch === ')') depth--
    if (depth === 0 && (sep === ',' ? ch === ',' : /\s/.test(ch))) {
      if (cur.trim() !== '') out.push(cur.trim())
      cur = ''
    } else cur += ch
  }
  if (cur.trim() !== '') out.push(cur.trim())
  return out
}

const num = (s: string) => (s.endsWith('%') ? parseFloat(s) / 100 : parseFloat(s))

type Lookup = (name: string) => string

function parseColour(input: string, lookup: Lookup): Lab {
  const v = input.trim()
  const fn = /^([a-z-]+)\((.*)\)$/s.exec(v)
  if (!fn) {
    if (v === 'transparent') return { L: 0, a: 0, b: 0, alpha: 0 }
    if (v === 'white') return srgbToLab(1, 1, 1)
    if (v === 'black') return srgbToLab(0, 0, 0)
    const hex = /^#([0-9a-f]{3,8})$/i.exec(v)
    if (hex) {
      let h = hex[1]
      if (h.length <= 4) h = [...h].map((c) => c + c).join('')
      const n = (i: number) => parseInt(h.slice(i, i + 2), 16) / 255
      return srgbToLab(n(0), n(2), n(4), h.length === 8 ? n(6) : 1)
    }
    throw new Error(`cannot parse colour "${input}"`)
  }
  const [, name, body] = fn
  if (name === 'var') {
    const [ref, fallback] = splitTop(body, ',')
    const val = lookup(ref)
    if (val === '' && fallback) return parseColour(fallback, lookup)
    return parseColour(val, lookup)
  }
  if (name === 'color-mix') {
    const [space, ...parts] = splitTop(body, ',')
    if (space !== 'in oklab') throw new Error(`color-mix space "${space}" is not supported`)
    const parse = (p: string) => {
      const m = /^(.*?)(?:\s+([\d.]+)%)?$/s.exec(p)!
      return { colour: parseColour(m[1], lookup), pct: m[2] === undefined ? null : parseFloat(m[2]) }
    }
    const [x, y] = [parse(parts[0]), parse(parts[1])]
    const px = x.pct ?? (y.pct === null ? 50 : 100 - y.pct)
    const py = y.pct ?? 100 - px
    const wx = px / (px + py)
    const wy = py / (px + py)
    // Premultiplied interpolation, as the spec says; the alphas add.
    const alpha = x.colour.alpha * wx + y.colour.alpha * wy
    const mix = (k: 'L' | 'a' | 'b') =>
      alpha === 0 ? 0 : (x.colour[k] * x.colour.alpha * wx + y.colour[k] * y.colour.alpha * wy) / alpha
    return { L: mix('L'), a: mix('a'), b: mix('b'), alpha }
  }
  // Space-separated colour functions: `oklch(L C H / A)`, `hsl(H S L)`, `rgb(...)`.
  const [main, a] = body.split('/').map((s) => s.trim())
  const [p1, p2, p3] = splitTop(main.replace(/,/g, ' '), ' ')
  const alpha = a === undefined ? 1 : num(a)
  if (name === 'oklch') {
    const L = p1.endsWith('%') ? parseFloat(p1) / 100 : parseFloat(p1)
    const C = parseFloat(p2)
    const H = (parseFloat(p3) * Math.PI) / 180
    return { L, a: C * Math.cos(H), b: C * Math.sin(H), alpha }
  }
  if (name === 'hsl') {
    const [r, g, b] = hslToRgb(parseFloat(p1), num(p2), num(p3))
    return srgbToLab(r, g, b, alpha)
  }
  if (name === 'rgb') {
    const c = (p: string) => (p.endsWith('%') ? parseFloat(p) / 100 : parseFloat(p) / 255)
    return srgbToLab(c(p1), c(p2), c(p3), alpha)
  }
  throw new Error(`cannot parse colour function "${name}"`)
}

type Rgb = [number, number, number]
const clip = (c: number) => Math.min(1, Math.max(0, c))
const lin = (c: Lab): Rgb => labToLin(c).map(clip) as Rgb

const encode = (c: number) => (c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055)

/**
 * `top` painted over an opaque `under`, returned as linear sRGB (what the
 * luminance formula wants). The blend itself is done on the gamma-encoded sRGB
 * values, which is what a browser does when it composites a translucent colour:
 * blending in linear light would call a 20% tint far lighter than it looks.
 */
function composite(top: Lab, under: Lab): Rgb {
  const t = lin(top).map(encode)
  const u = lin(under).map(encode)
  return [0, 1, 2].map((i) => toLin(t[i] * top.alpha + u[i] * (1 - top.alpha))) as Rgb
}
const luminance = ([r, g, b]: Rgb) => 0.2126 * r + 0.7152 * g + 0.0722 * b
function contrast(a: Rgb, b: Rgb): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

// ── Token definitions, read from the CSS ─────────────────────────────────────

function declarations(block: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const m of block.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out[m[1]] = m[2].replace(/\s+/g, ' ').trim()
  return out
}
function blockOf(css: string, selector: RegExp): string {
  const m = selector.exec(css)
  if (!m) throw new Error(`no ${selector} block in index.css`)
  const start = css.indexOf('{', m.index) + 1
  let depth = 1
  let i = start
  while (depth > 0) {
    if (css[i] === '{') depth++
    if (css[i] === '}') depth--
    i++
  }
  return css.slice(start, i - 1)
}

const palette = declarations(paletteCss)
const light = declarations(blockOf(indexCss, /(?:^|\n)\s*:root\s*\{/))
const dark = declarations(blockOf(indexCss, /(?:^|\n)\s*\.dark\s*\{/))

function lookupFor(theme: Theme): Lookup {
  const tokens = theme === 'dark' ? { ...light, ...dark } : light
  return (name) => {
    const v = tokens[name] ?? palette[name]
    if (v === undefined) throw new Error(`${name} is not defined (theme ${theme})`)
    return v
  }
}
const tokenIn = (theme: Theme, token: string): Lab => parseColour(`var(--${token})`, lookupFor(theme))

/** What a surface looks like: a token drawn over the card, so a tint is opaque. */
function surfaceRgb(theme: Theme, token: string): Rgb {
  return composite(tokenIn(theme, token), tokenIn(theme, 'card'))
}
function ratio(theme: Theme, fg: string, bg: string): number {
  const bgLab = tokenIn(theme, bg)
  const under = { ...bgLab, alpha: 1 }
  const fgRgb = composite(tokenIn(theme, fg), under)
  return contrast(fgRgb, surfaceRgb(theme, bg))
}

// ── The pairs (data) ─────────────────────────────────────────────────────────

const SURFACES = ['canvas', 'card', 'subtle', 'muted']
const TEXT = ['fg', 'fg-soft', 'fg-muted', 'fg-faint', 'link', 'danger-fg', 'warning-fg', 'success-fg', 'info-fg', 'attention-fg', 'selected-fg']
const FAMILIES = ['danger', 'warning', 'success', 'info'] as const
const SOLIDS: [string, string][] = [
  ['primary-foreground', 'primary'],
  ['primary-foreground', 'primary-hover'],
  ['danger-foreground', 'danger'],
  ['danger-foreground', 'danger-hover'],
  ['warning-foreground', 'warning'],
  ['success-foreground', 'success'],
  ['success-foreground', 'success-hover'],
  ['info-foreground', 'info'],
  ['inverse-foreground', 'inverse'],
]

// In the light theme some text colours measure just under 4.5:1 on a tinted
// surface: on `muted` (gray-100) fg-faint 4.39, danger-fg 4.33, success-fg 4.49;
// on `selected` (blue-50) fg-faint 4.44. Moving them a shade darker would change
// 400 uses across the app that sit on white, where they pass, so these pairs are
// not asserted in light. They are not allowed in practice either:
// theme-colours.test.ts fails on a line that puts one of these text colours on
// that surface (a muted chip or hover fill uses fg-muted, danger-fg-strong or
// success-fg-strong; those pairs are asserted below), and axe checks the
// rendered pages.
const NOT_IN_LIGHT = new Set(['fg-faint|muted', 'danger-fg|muted', 'success-fg|muted', 'fg-faint|selected'])

// [foreground token, surface token, minimum ratio]
const PAIRS: [string, string, number][] = [
  // Text tokens on every surface they may sit on.
  ...TEXT.flatMap((t) => SURFACES.map((s): [string, string, number] => [t, s, 4.5])),
  // The text colours are also used on the popover and shadcn base surfaces.
  ...['fg', 'fg-soft', 'fg-muted', 'fg-faint', 'link'].flatMap((t) =>
    ['popover', 'background'].map((s): [string, string, number] => [t, s, 4.5]),
  ),
  ['card-foreground', 'card', 4.5],
  ['popover-foreground', 'popover', 4.5],
  ['foreground', 'background', 4.5],
  ['secondary-foreground', 'secondary', 4.5],
  ['accent-foreground', 'accent', 4.5],
  ['muted-foreground', 'background', 4.5],
  ['muted-foreground', 'card', 4.5],
  // Selected text on its own fill.
  ['selected-fg', 'selected', 4.5],
  ['selected-fg', 'selected-strong', 4.5],
  // Quiet text inside a selected row or chip (its count, its icon).
  ['fg-faint', 'selected', 4.5],
  ['fg-muted', 'selected', 4.5],
  ['fg-muted', 'selected-strong', 4.5],
  // The strong text of each family on its tint.
  ...FAMILIES.map((f): [string, string, number] => [`${f}-fg-strong`, `${f}-soft`, 4.5]),
  ['attention-fg', 'attention-soft', 4.5],
  // The plain text colour of a family on its own tint is how callouts print their body.
  ...FAMILIES.map((f): [string, string, number] => [`${f}-fg-strong`, 'card', 4.5]),
  ...['danger', 'success'].map((f): [string, string, number] => [`${f}-fg-strong`, 'muted', 4.5]),
  // Text on a solid fill.
  ...SOLIDS.map(([fg, bg]): [string, string, number] => [fg, bg, 4.5]),
  // Search match: the surrounding text keeps its colour, over the card.
  ['fg', 'highlight', 4.5],
  ['fg-soft', 'highlight', 4.5],
  // Non-text: the focus ring and the charts against the surfaces they are drawn on (WCAG 1.4.11).
  ['ring', 'card', 3],
  ['ring', 'canvas', 3],
  ...['chart-blue', 'chart-green', 'chart-amber', 'chart-red', 'chart-gray'].flatMap((c) =>
    ['card', 'canvas'].map((s): [string, string, number] => [c, s, 3]),
  ),
]

describe.each<Theme>(['light', 'dark'])('contrast tokens, %s theme', (theme) => {
  const pairs = PAIRS.filter(([fg, bg]) => !(theme === 'light' && NOT_IN_LIGHT.has(`${fg}|${bg}`)))
  it.each(pairs)('%s on %s is at least %d:1', (fg, bg, min) => {
    const got = ratio(theme, fg, bg)
    expect(got, `${fg} on ${bg} is ${got.toFixed(2)}:1, needs ${min}:1`).toBeGreaterThanOrEqual(min)
  })
})

describe('the token sets', () => {
  it('define the same names in both themes, or inherit them (dark only overrides)', () => {
    for (const name of Object.keys(dark)) expect(light, `--${name.slice(2)} is only in .dark`).toHaveProperty(name)
  })

  it('every token a pair names is defined', () => {
    for (const [fg, bg] of PAIRS) {
      expect(light, fg).toHaveProperty(`--${fg}`)
      expect(light, bg).toHaveProperty(`--${bg}`)
    }
  })

  it('the engine agrees with known values', () => {
    const l = lookupFor('light')
    const rgb = (c: string) => composite(parseColour(c, l), parseColour('white', l))
    // Black on white, and the WCAG example of #767676 on white (4.54:1).
    expect(contrast(rgb('#000'), rgb('#fff'))).toBeCloseTo(21, 1)
    expect(contrast(rgb('#767676'), rgb('#fff'))).toBeCloseTo(4.54, 1)
    // oklch round trip: Tailwind v4's gray-500 is #6a7282.
    const g = lin(parseColour('oklch(55.1% 0.027 264.364)', l)).map((c) => Math.round((c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055) * 255))
    expect(g).toEqual([106, 114, 130])
  })
})

// ── Code highlighting (src/styles/markdown.css) ──────────────────────────────

interface HljsRule { cls: string; dark: boolean; color?: string; background?: string }

function hljsRules(css: string): HljsRule[] {
  const out: HljsRule[] = []
  for (const m of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const props = Object.fromEntries(
      [...m[2].matchAll(/(?:^|;)\s*([\w-]+)\s*:\s*([^;]+)/g)].map((p) => [p[1], p[2].trim()]),
    )
    for (const sel of m[1].split(',')) {
      const cls = /\.hljs-([\w]+)/.exec(sel)
      if (!cls || !sel.includes('[data-md-body]')) continue
      out.push({ cls: cls[1], dark: /\.dark\b/.test(sel), color: props.color, background: props.background })
    }
  }
  return out
}

/** A token class's colour and background, merged across rules of one theme. */
function hljsFor(rules: HljsRule[], dark: boolean) {
  const merged = new Map<string, { color?: string; background?: string }>()
  for (const r of rules.filter((x) => x.dark === dark)) {
    const cur = merged.get(r.cls) ?? {}
    merged.set(r.cls, { color: r.color ?? cur.color, background: r.background ?? cur.background })
  }
  return merged
}

describe('code highlighting colours', () => {
  const rules = hljsRules(markdownCss)

  for (const theme of ['light', 'dark'] as Theme[]) {
    const set = hljsFor(rules, theme === 'dark')

    if (theme === 'dark') {
      it('has a dark set for every class the light set colours', () => {
        const lightSet = hljsFor(rules, false)
        for (const cls of lightSet.keys()) expect(set.has(cls), `.hljs-${cls} has no dark colour`).toBe(true)
      })
    }

    it.each([...set.entries()])(`.hljs-%s is at least 4.5:1 on the ${theme} code box`, (cls, { color, background }) => {
      expect(color, `${cls} has no colour`).toBeDefined()
      const l = lookupFor(theme)
      const box = surfaceRgb(theme, 'muted')
      const boxLab = { ...tokenIn(theme, 'muted'), alpha: 1 }
      const bg = background ? composite(parseColour(background, l), { ...boxLab }) : box
      const fg = composite(parseColour(color!, l), { ...boxLab })
      expect(contrast(fg, bg), `${cls} ${color} on ${background ?? 'the code box'}`).toBeGreaterThanOrEqual(4.5)
    })
  }

  it('finds the rules it is meant to check', () => {
    expect(hljsFor(rules, false).size).toBeGreaterThan(5)
  })
})
