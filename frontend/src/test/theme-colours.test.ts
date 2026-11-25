import { readdirSync, readFileSync } from 'node:fs'
import { join, sep } from 'node:path'
import { describe, expect, it } from 'vitest'

// #709: the colour guard. Every colour in the app is a theme token (bg-card,
// text-fg-muted, border-border; see index.css), because a Tailwind palette
// class (bg-white, text-gray-500, text-red-600) is fixed to the light look and
// is wrong in dark mode. This test fails on any palette utility, arbitrary
// colour value or colour literal (hex, rgb(), a named colour in a style or an
// attribute, an arbitrary property, the bg-(--x) shorthand) in a .ts/.tsx file
// under src/, tests excepted, and on a palette utility or literal colour in a
// stylesheet outside its token blocks. It also fails on a class string that puts
// a text colour on a tint it measures under 4.5:1 on in the light theme (see
// NOT_IN_LIGHT in theme-contrast.test.ts).
//
// It is a test and not an ESLint rule because class names live in strings,
// template literals and cva() configs, and a regex over the text sees them all.
//
// A line that must keep a literal because it is data rather than styling says
// why, on the line itself:  // theme-ok: stored data, not styling
// The reason must be at least 10 characters. The number of such lines is capped
// (MAX_THEME_OK): raising the cap is a reviewed change. A block that must stay
// literal (the email starter HTML) is bracketed by `theme-ok-begin: <reason>` and
// `theme-ok-end` comment lines, and counts as one use.

const MAX_THEME_OK = 8

const COLOUR_FAMILY =
  'white|black|slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose'
const UTILITY =
  'bg|text|text-shadow|drop-shadow|inset-shadow|inset-ring|border(?:-[trblxyse])?|ring(?:-offset)?|divide|outline|placeholder|fill|stroke|from|via|to|decoration|caret|accent|shadow'

// CSS named colours that are a colour and not a keyword (transparent, currentColor,
// inherit and none are fine: they follow the theme).
const NAMED =
  'white|black|red|green|blue|yellow|orange|purple|pink|brown|gray|grey|silver|gold|navy|teal|aqua|cyan|magenta|lime|maroon|olive|fuchsia|indigo|violet|crimson|coral|salmon|tomato|khaki|ivory|beige|lavender|plum|orchid|tan|azure|snow|linen|wheat|turquoise|skyblue|slategray|slategrey|darkgray|darkgrey|lightgray|lightgrey|dimgray|dimgrey|whitesmoke|gainsboro|darkred|darkblue|darkgreen|lightblue|lightgreen|rebeccapurple'
const COLOUR_FN = 'rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix'
const HEX = '#[0-9a-fA-F]{3,8}\\b'

// Each rule: what it catches, and the regex (per line, comments included).
const RULES: { name: string; re: RegExp }[] = [
  {
    name: 'palette colour class',
    re: new RegExp(
      `(?<![\\w-])(?:[\\w-]+:)*(?:${UTILITY})-(?:${COLOUR_FAMILY})(?:-\\d{2,3})?(?:\\/\\d{1,3})?(?![\\w-])`,
    ),
  },
  {
    // bg-[#fff], text-[rgb(...)], text-[red], outline-[color:hsl(var(--ring))],
    // bg-[var(--x)], and the v4 shorthand bg-(--x): use the registered utility.
    name: 'arbitrary colour value',
    re: new RegExp(
      `(?<![\\w-])(?:${UTILITY})-(?:\\[(?:color:)?(?:#|(?:${COLOUR_FN})\\(|var\\(|(?:${NAMED})\\])|\\((?:color:)?--)`,
    ),
  },
  {
    // shadow-[0_0_0_1px_#000], ring-[3px_rgb(0_0_0)]: a colour inside a longer value.
    name: 'colour inside an arbitrary value',
    re: new RegExp(`(?<![\\w-])(?:${UTILITY})-\\[[^\\]]*(?:${HEX}|(?<![a-zA-Z])(?:${COLOUR_FN})\\()`),
  },
  {
    // [background-color:#fff], [color:red], [box-shadow:0_0_0_1px_rgb(0_0_0)].
    // A var() of a token is fine; a literal colour is not.
    name: 'arbitrary property with a colour',
    re: new RegExp(
      `\\[(?:[\\w-]*color|background|fill|stroke|border[\\w-]*|outline[\\w-]*|box-shadow|text-shadow):[^\\]]*(?:${HEX}|(?<![a-zA-Z])(?:${COLOUR_FN})\\(|\\b(?:${NAMED})\\b)`,
    ),
  },
  { name: 'hex colour literal', re: /['"`]#[0-9a-fA-F]{3,8}['"`]/ },
  // A hex of six or eight digits anywhere in a string ('1px solid #e5e7eb'). Three
  // digits is left alone here: it is as likely a ticket or issue number.
  { name: 'hex colour inside a string', re: /['"`][^'"`\n]*#(?:[0-9a-fA-F]{6}|[0-9a-fA-F]{8})\b/ },
  { name: 'colour function in a string', re: /['"`][^'"`\n]*\b(?:rgba?|hsla?|oklch|oklab|lab|lch|hwb)\(/ },
  {
    // fill="white", stroke={'black'}, color: 'red', background: "navy", fillStyle = 'black'.
    name: 'named colour in an attribute or style',
    re: new RegExp(
      `\\b(?:fill|stroke|stopColor|floodColor|lightingColor|color|backgroundColor|background|borderColor|border(?:Top|Right|Bottom|Left)?Color|outlineColor|caretColor|accentColor|boxShadow|textShadow)\\s*[:=]\\s*\\{?\\s*['"\`][^'"\`]*\\b(?:${NAMED})\\b`,
    ),
  },
  { name: 'canvas colour assigned from a literal', re: /\b(?:fillStyle|strokeStyle|shadowColor)\s*=\s*['"`]/ },
]

const ESCAPE = /theme-ok:\s*(\S.{9,})/
// A block of lines that must stay literal (the starter HTML of an email template:
// an email is never themed). Counts as one use of theme-ok.
const BLOCK_BEGIN = /theme-ok-begin:\s*(\S.{9,})/
const BLOCK_END = /theme-ok-end\b/

// Text that measures under 4.5:1 on a tinted surface in the light theme (the
// pairs NOT_IN_LIGHT in theme-contrast.test.ts). A line that puts one on the
// other fails; use fg-muted or the -strong level of the family instead.
const TINTS: Record<string, string[]> = {
  muted: ['fg-faint', 'danger-fg', 'success-fg'],
  'muted-strong': ['fg-faint', 'danger-fg', 'success-fg'],
  selected: ['fg-faint'],
  'selected-strong': ['fg-faint'],
}
const SURFACE_RE = /(?<![\w:-])(?:(?:sm|md|lg|xl|2xl|dark):)*bg-(muted-strong|muted|selected-strong|selected)(?![\w-])/g

/**
 * The string literals of a source text, each a unit of class names: a ternary's
 * two branches are two units, so `a ? 'bg-selected text-selected-fg' : 'text-fg-faint'`
 * is not one element. A template literal is one unit with its `${}` and nested
 * strings taken out.
 */
function classUnits(text: string): { text: string; at: number }[] {
  const out: { text: string; at: number }[] = []
  for (const m of text.matchAll(/'([^'\n]*)'|"([^"\n]*)"|`([^`]*)`/g)) {
    let body = m[1] ?? m[2] ?? m[3]
    if (m[3] !== undefined) {
      body = body.replace(/\$\{[^}]*\}/g, ' ').replace(/'[^'\n]*'|"[^"\n]*"/g, ' ')
      // The nested strings are units of their own.
      for (const n of m[3].matchAll(/'([^'\n]*)'|"([^"\n]*)"/g)) out.push({ text: n[1] ?? n[2], at: m.index! + n.index! })
    }
    out.push({ text: body, at: m.index! })
  }
  return out
}

function lowContrastIn(unit: string): string | null {
  for (const m of unit.matchAll(SURFACE_RE)) {
    for (const text of TINTS[m[1]]) {
      const re = new RegExp(`(?<![\\w:-])(?:(?:sm|md|lg|xl|2xl|dark):)*text-${text}(?![\\w-])`)
      if (re.test(unit)) return `text-${text} on bg-${m[1]}: below 4.5:1 in the light theme, use fg-muted or the -strong level`
    }
  }
  return null
}

/** Offsets and reasons of every class string in `text` that puts a failing text colour on a tint. */
function contrastViolations(text: string): { at: number; what: string }[] {
  return classUnits(text).flatMap((u) => {
    const what = lowContrastIn(u.text)
    return what ? [{ at: u.at, what }] : []
  })
}

function lineViolation(line: string): string | null {
  if (ESCAPE.test(line)) return null
  for (const { name, re } of RULES) {
    const m = re.exec(line)
    if (m) return `${name} "${m[0].slice(0, 60)}"`
  }
  return null
}

function violation(line: string): string | null {
  if (ESCAPE.test(line)) return null
  return lineViolation(line) ?? contrastViolations(line)[0]?.what ?? null
}

// ── What is scanned ──────────────────────────────────────────────────────────

const SRC = 'src' // vitest runs from frontend/
const rel = (p: string) => p.split(sep).join('/')

function scanned(): string[] {
  return (readdirSync(SRC, { recursive: true }) as string[])
    .map(rel)
    .filter((p) => /\.(?:tsx?|css)$/.test(p))
    .filter((p) => !/\.test\.tsx?$/.test(p))
    .filter((p) => !p.startsWith('test/'))
    .sort()
}

// ── CSS ──────────────────────────────────────────────────────────────────────
// The token blocks (:root, .dark, @theme inline) are where colours are defined,
// so they are exempt. Everywhere else in a stylesheet a colour must be a token:
// no palette utility (@apply bg-gray-100), no raw palette variable, no literal
// colour in a declaration. The one exception is a highlight.js rule (.hljs-*),
// whose colours theme-contrast.test.ts checks against the code box.

const CSS_RULES: { name: string; re: RegExp }[] = [
  { name: 'palette colour class', re: RULES[0].re },
  { name: 'raw palette variable', re: new RegExp(`var\\(--color-(?:${COLOUR_FAMILY})(?:-\\d{2,3})?\\)`) },
  {
    name: 'literal colour in a declaration',
    re: new RegExp(
      `(?:color|background|border[\\w-]*|fill|stroke|outline[\\w-]*|box-shadow|text-shadow)\\s*:[^;]*(?:${HEX}|(?<![a-zA-Z])(?:${COLOUR_FN})\\(|\\b(?:${NAMED})\\b)`,
    ),
  },
]

function cssViolation(line: string): string | null {
  if (line.includes('.hljs-')) return null
  for (const { name, re } of CSS_RULES) {
    const m = re.exec(line)
    if (m) return `${name} "${m[0].slice(0, 60)}"`
  }
  return null
}

/** Line numbers (1-based) inside a :root, .dark or @theme inline block. */
function tokenBlockLines(source: string): Set<number> {
  // Comments out of the way (their text would be taken for part of a selector).
  const text = source.replace(/\/\*[^]*?\*\//g, (c) => c.replace(/[^\n]/g, ' '))
  const inside = new Set<number>()
  let line = 1
  let depth = 0
  let blockDepth = -1
  let head = ''
  for (const ch of text) {
    if (ch === '\n') {
      line++
      if (blockDepth >= 0) inside.add(line)
    }
    if (ch === '{') {
      if (blockDepth < 0 && /^(?:@layer\s+base\s*\{\s*)?(?::root|\.dark|:root\.dark|@theme\s+inline)$/.test(head.trim())) {
        blockDepth = depth
        inside.add(line)
      }
      depth++
      head = ''
    } else if (ch === '}') {
      depth--
      if (depth === blockDepth) blockDepth = -1
      head = ''
    } else if (ch === ';') head = ''
    else head += ch
  }
  return inside
}

function violationsIn(path: string): { line: number; what: string }[] {
  const out: { line: number; what: string }[] = []
  const text = readFileSync(join(SRC, path), 'utf8')
  const lines = text.split('\n')
  if (path.endsWith('.css')) {
    const skip = tokenBlockLines(text)
    lines.forEach((l, i) => {
      if (skip.has(i + 1)) return
      const what = cssViolation(l)
      if (what) out.push({ line: i + 1, what })
    })
    return out
  }
  let inBlock = false
  lines.forEach((l, i) => {
    if (BLOCK_BEGIN.test(l)) inBlock = true
    const skipLine = inBlock
    if (inBlock && BLOCK_END.test(l)) inBlock = false
    if (skipLine) return
    const what = lineViolation(l)
    if (what) out.push({ line: i + 1, what })
  })
  for (const c of contrastViolations(text)) {
    const line = text.slice(0, c.at).split('\n').length
    if (!ESCAPE.test(lines[line - 1])) out.push({ line, what: c.what })
  }
  return out.sort((x, y) => x.line - y.line)
}

// ── Tests ────────────────────────────────────────────────────────────────────

describe('colour guard probes', () => {
  const flagged = [
    'hover:bg-gray-100',
    'md:text-red-600',
    'bg-white/80',
    'border-l-gray-200',
    'text-[#333]',
    '"#fff"',
    '`rgb(0 0 0)`',
    'focus-visible:ring-blue-500',
    'outline-[color:hsl(var(--ring))]',
    'bg-[var(--x)]',
    'className="text-gray-500"',
    "const c = '#6b7280'",
    'data-[state=open]:bg-white',
    'placeholder-gray-400',
    'group-hover:text-red-600',
    'ring-offset-white',
    'divide-gray-100',
    'bg-black/40',
    'text-black',
    // The bypasses found in review.
    "style={{ color: 'white' }}",
    '<path fill="white" />',
    "ctx.fillStyle = 'black'",
    "border: '1px solid #e5e7eb',",
    'className="[background-color:#fff]"',
    'className="shadow-[0_0_0_1px_#000]"',
    'className="text-[red]"',
    'className="bg-[lab(50%_10_10)]"',
    'className="bg-[color(display-p3_1_0_0)]"',
    'className="bg-(--x)"',
    'className="text-(color:--x)"',
    'className="inset-ring-red-500"',
    'className="inset-shadow-gray-300"',
    'className="text-shadow-black"',
    'className="drop-shadow-blue-500"',
    'className="ring-[3px_rgb(0_0_0)]"',
    // A text colour on a surface it fails on in light.
    'className="bg-muted text-fg-faint"',
    'className="text-fg-faint rounded bg-muted px-2"',
    'className="bg-muted-strong text-danger-fg"',
    'className="bg-muted text-success-fg"',
    'className="bg-selected text-fg-faint"',
    'className="md:bg-muted md:text-fg-faint"',
  ]
  const clean = [
    'bg-card',
    'text-fg-muted',
    'border-border',
    'ring-ring',
    'bg-danger/10',
    'context-gray-ish',
    'background-color',
    '// see #443 for the editor',
    "const a = '#'",
    'to-do',
    'bg-[length:10px]',
    'w-[var(--radix-popover-trigger-width)]',
    'hover:bg-accent hover:text-accent-foreground',
    'fill="currentColor"',
    'fill="none"',
    "style={{ color: 'inherit', background: 'transparent' }}",
    'className="shadow-[0_0_0_1px]"',
    'className="[mask-image:linear-gradient(black,transparent)]"',
    'className="bg-muted text-fg-muted"',
    "`rounded px-2 ${on ? 'bg-selected-strong text-selected-fg' : 'text-fg-faint hover:text-fg-soft'}`",
    'className="bg-muted text-danger-fg-strong"',
    'className="hover:bg-muted text-fg-faint"',
    'className="bg-card text-fg-faint"',
    'className="bg-subtle text-fg-faint"',
    'className="bg-muted-foreground"',
    'className="text-(length:--x)"',
    'className="[color:var(--fg)]"',
    'className="w-[calc(100%-2rem)]"',
  ]

  it.each(flagged)('flags %s', (line) => {
    expect(violation(line)).not.toBeNull()
  })

  it.each(clean)('does not flag %s', (line) => {
    expect(violation(line)).toBeNull()
  })

  it('honours theme-ok: with a reason of at least 10 characters, and only then', () => {
    expect(violation("const c = '#6b7280' // theme-ok: stored data, not styling")).toBeNull()
    expect(violation("const c = '#6b7280' // theme-ok: short")).not.toBeNull()
    expect(violation("const c = '#6b7280' // theme-ok:")).not.toBeNull()
  })
})

describe('colour guard, stylesheets', () => {
  it.each([
    '  @apply bg-gray-100 text-red-600;',
    '  background: var(--color-gray-100);',
    '  color: #333;',
    '  border-color: rgb(0 0 0);',
    '  box-shadow: 0 0 0 1px red;',
  ])('flags %s', (line) => {
    expect(cssViolation(line)).not.toBeNull()
  })

  it.each([
    '  @apply bg-card text-fg;',
    '  background: var(--muted);',
    '  color: transparent;',
    '  border: 1px solid var(--border);',
    '[data-md-body] code .hljs-quote { color: #4b5563; }',
    '  /* see #443 */',
  ])('does not flag %s', (line) => {
    expect(cssViolation(line)).toBeNull()
  })

  it('skips the token blocks and nothing else', () => {
    const css = [
      '/* tokens; a comment with a { brace */',
      '@layer base {',
      '  :root {',
      '    --a: var(--color-gray-50);',
      '  }',
      '  .dark {',
      '    --a: var(--color-gray-900);',
      '  }',
      '}',
      '.x { @apply bg-gray-100; }',
    ].join('\n')
    const skip = tokenBlockLines(css)
    expect([...skip].sort((a, b) => a - b)).toEqual([3, 4, 5, 6, 7, 8])
    expect(skip.has(10)).toBe(false)
  })
})

describe('colour guard', () => {
  const files = scanned()

  it('scans the source', () => {
    expect(files.length).toBeGreaterThan(100)
    expect(files).toContain('components/ui/button.tsx')
    expect(files).not.toContain('test/theme-colours.test.ts')
  })

  it('finds no palette class, arbitrary colour or colour literal', () => {
    const found = files
      .flatMap((f) => violationsIn(f).map((v) => `${f}:${v.line}  ${v.what}`))
    expect(found, 'use a theme token (see src/index.css), or add `// theme-ok: <reason>` for data that is not styling').toEqual([])
  })

  it(`uses theme-ok: at most ${MAX_THEME_OK} times`, () => {
    const uses = files.flatMap((f) =>
      readFileSync(join(SRC, f), 'utf8')
        .split('\n')
        .map((text, i) => ({ f, text, n: i + 1 }))
        .filter((l) => l.text.includes('theme-ok:') || l.text.includes('theme-ok-begin:')),
    )
    // Printed so a reviewer sees each one.
    console.info(`theme-ok: ${uses.length} of ${MAX_THEME_OK}\n` + uses.map((u) => `  ${u.f}:${u.n}`).join('\n'))
    expect(uses.length).toBeLessThanOrEqual(MAX_THEME_OK)
  })
})
