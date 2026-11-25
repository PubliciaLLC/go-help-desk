import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join, sep } from 'node:path'
import { describe, expect, it } from 'vitest'

// #709: the colour guard. Every colour in the app is a theme token (bg-card,
// text-fg-muted, border-border; see index.css), because a Tailwind palette
// class (bg-white, text-gray-500, text-red-600) is fixed to the light look and
// is wrong in dark mode. This test fails on any palette utility, arbitrary
// colour value or colour literal in a .ts/.tsx file under src/, tests excepted.
//
// It is a test and not an ESLint rule because class names live in strings,
// template literals and cva() configs, and a regex over the text sees them all.
//
// A line that must keep a literal because it is data rather than styling says
// why, on the line itself:  // theme-ok: stored data, not styling
// The reason must be at least 10 characters. The number of such lines is capped
// (MAX_THEME_OK): raising the cap is a reviewed change.
//
// While the colour migration is in progress, src/test/theme-pending/<PKG>.txt
// lists the files its package has not migrated yet (paths relative to src/, one
// per line). Listed files are skipped. A package deletes its own list as its
// last commit; the guard then covers its files. A list that names a file that
// does not exist, or one that is already clean, fails: lists cannot rot.

const MAX_THEME_OK = 8

const COLOUR_FAMILY =
  'white|black|slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose'
const UTILITY =
  'bg|text|border(?:-[trblxyse])?|ring(?:-offset)?|divide|outline|placeholder|fill|stroke|from|via|to|decoration|caret|accent|shadow'

// Each rule: what it catches, and the regex (per line, comments included).
const RULES: { name: string; re: RegExp }[] = [
  {
    name: 'palette colour class',
    re: new RegExp(
      `(?<![\\w-])(?:[\\w-]+:)*(?:${UTILITY})-(?:${COLOUR_FAMILY})(?:-\\d{2,3})?(?:\\/\\d{1,3})?(?![\\w-])`,
    ),
  },
  {
    // bg-[#fff], text-[rgb(...)], outline-[color:hsl(var(--ring))], bg-[var(--x)]:
    // use the registered utility instead.
    name: 'arbitrary colour value',
    re: new RegExp(`(?<![\\w-])(?:${UTILITY})-\\[(?:color:)?(?:#|rgb|hsl|oklch|color-mix|var\\()`),
  },
  { name: 'hex colour literal', re: /['"`]#[0-9a-fA-F]{3,8}['"`]/ },
  { name: 'colour function in a string', re: /['"`][^'"`\n]*\b(?:rgba?|hsla?|oklch)\(/ },
]

const ESCAPE = /theme-ok:\s*(\S.{9,})/

function violation(line: string): string | null {
  if (ESCAPE.test(line)) return null
  for (const { name, re } of RULES) {
    const m = re.exec(line)
    if (m) return `${name} "${m[0]}"`
  }
  return null
}

// ── What is scanned ──────────────────────────────────────────────────────────

const SRC = 'src' // vitest runs from frontend/
const PENDING_DIR = join(SRC, 'test', 'theme-pending')

const rel = (p: string) => p.split(sep).join('/')

function scanned(): string[] {
  return (readdirSync(SRC, { recursive: true }) as string[])
    .map(rel)
    .filter((p) => /\.tsx?$/.test(p))
    .filter((p) => !/\.test\.tsx?$/.test(p))
    .filter((p) => !p.startsWith('test/'))
    .sort()
}

function pendingLists(): Map<string, string[]> {
  const lists = new Map<string, string[]>()
  if (!existsSync(PENDING_DIR)) return lists
  for (const f of readdirSync(PENDING_DIR).filter((n) => n.endsWith('.txt'))) {
    const lines = readFileSync(join(PENDING_DIR, f), 'utf8')
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l !== '' && !l.startsWith('#'))
    lists.set(f.replace(/\.txt$/, ''), lines)
  }
  return lists
}

function violationsIn(path: string): { line: number; what: string }[] {
  const out: { line: number; what: string }[] = []
  readFileSync(join(SRC, path), 'utf8')
    .split('\n')
    .forEach((text, i) => {
      const what = violation(text)
      if (what) out.push({ line: i + 1, what })
    })
  return out
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

describe('colour guard', () => {
  const files = scanned()
  const lists = pendingLists()
  const pending = new Set([...lists.values()].flat())

  it('scans the source', () => {
    expect(files.length).toBeGreaterThan(100)
    expect(files).toContain('components/ui/button.tsx')
    expect(files).not.toContain('test/theme-colours.test.ts')
  })

  it('finds no palette class, arbitrary colour or colour literal outside the pending lists', () => {
    const found = files
      .filter((f) => !pending.has(f))
      .flatMap((f) => violationsIn(f).map((v) => `${f}:${v.line}  ${v.what}`))
    expect(found, 'use a theme token (see src/index.css), or add `// theme-ok: <reason>` for data that is not styling').toEqual([])
  })

  it('has a pending list that names only files that exist, and only files that still need migrating', () => {
    const problems: string[] = []
    for (const [pkg, names] of lists) {
      for (const f of names) {
        if (!files.includes(f)) problems.push(`${pkg}: ${f} is not a scanned source file (gone, renamed, or a test)`)
        else if (violationsIn(f).length === 0) problems.push(`${pkg}: ${f} is already clean: remove it from the list`)
        const owners = [...lists].filter(([, n]) => n.includes(f)).map(([p]) => p)
        if (owners.length > 1 && pkg === owners[0]) problems.push(`${f} is in more than one list: ${owners.join(', ')}`)
      }
    }
    expect(problems).toEqual([])
  })

  it(`uses theme-ok: at most ${MAX_THEME_OK} times`, () => {
    const uses = files.flatMap((f) =>
      readFileSync(join(SRC, f), 'utf8')
        .split('\n')
        .map((text, i) => ({ f, text, n: i + 1 }))
        .filter((l) => l.text.includes('theme-ok:')),
    )
    // Printed so a reviewer sees each one.
    console.info(`theme-ok: ${uses.length} of ${MAX_THEME_OK}\n` + uses.map((u) => `  ${u.f}:${u.n}`).join('\n'))
    expect(uses.length).toBeLessThanOrEqual(MAX_THEME_OK)
  })

  // Becomes the whole guard when the last package deletes its list. Z removes
  // the directory.
  it('reports which lists are still open', () => {
    console.info(`pending lists: ${lists.size === 0 ? 'none' : [...lists].map(([p, n]) => `${p} (${n.length})`).join(', ')}`)
    expect(true).toBe(true)
  })
})
