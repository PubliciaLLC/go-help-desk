import { describe, expect, it } from 'vitest'

// docs/DESIGN.md: "Every link to an attachment downloads it. No inline
// rendering of any attachment, images included: no lightbox, no thumbnail, no
// <img> pointing at the download route, no PDF viewer."
//
// What actually enforces that is the server. It refuses a request for an
// attachment whose Sec-Fetch-Dest says the browser is going to render the
// response — see handler_attachments.go and its test. That check cannot be
// written around from here, which is the point: an earlier version of this
// file was the only thing standing between an <img> and a rendered
// attachment, and two rounds of adversarial review walked past it five
// different ways.
//
// So these tests are not the control. They are here to keep the frontend
// honest about its own design, and to fail loudly at review time rather than
// at runtime: someone who adds an attachment preview should find out from a
// red test, not from a 403 in the browser.
//
// What text matching can and cannot do, said plainly so nobody trusts it
// further than it goes. It CAN list every JSX element in this frontend that
// renders remote content, and every place an attachment URL is spelled out.
// It CANNOT follow a value between files, and it cannot see a URL assembled
// from pieces or an element built by a function call. Anything it misses, the
// server still refuses.
//
// A note on <iframe>, since an earlier version of this comment got it wrong:
// an iframe load is a navigation, and browsers do honour
// Content-Disposition: attachment on a navigation — that is the old
// hidden-iframe download trick. <img> and CSS url() are the ones that render
// regardless, because a subresource fetch ignores the disposition entirely.

// Source files as text. import.meta.glob is Vite's, so this needs no Node
// types — which is not a stylistic preference: importing node:fs here broke
// `tsc -b`, and therefore the production build, while `vitest` stayed green.
const modules = import.meta.glob('/src/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>

const files = Object.entries(modules)
  .filter(([path]) => !/\.test\.tsx?$/.test(path))
  .map(([path, source]) => ({ path: path.replace(/^\//, ''), source }))

// The 1-based line a character offset falls on, for a message someone can
// click.
function lineAt(source: string, offset: number): number {
  return source.slice(0, offset).split('\n').length
}

// Where the URL builder is called, by character position. Import lines are
// skipped — naming it is not calling it — and an alias counts, because
// `import { attachmentDownloadUrl as dl }` is a rename, not a loophole.
function callSites(source: string): number[] {
  const names = ['attachmentDownloadUrl']
  const alias = source.match(/attachmentDownloadUrl\s+as\s+(\w+)/)
  if (alias) names.push(alias[1])

  const sites: number[] = []
  for (const name of names) {
    const re = new RegExp(`\\b${name}\\s*\\(`, 'g')
    for (const m of source.matchAll(re)) {
      const lineStart = source.lastIndexOf('\n', m.index) + 1
      const lineEnd = source.indexOf('\n', m.index)
      const line = source.slice(lineStart, lineEnd === -1 ? undefined : lineEnd)
      if (/^\s*(import|export)\b/.test(line)) continue
      sites.push(m.index)
    }
  }
  return sites
}

// A download attribute, not the word "download" anywhere in the element. A
// className of "download-link" was passing this.
function hasDownloadAttribute(element: string): boolean {
  return /[\s{]download(\s|=|\/|>|$)/.test(element)
}

describe('attachments are never rendered inline', () => {
  // Every element in the frontend that renders remote content, with the reason
  // it is allowed to exist. This is deliberately a list and not a rule: the
  // point is that adding one is a decision someone makes on purpose, in a
  // review, rather than something that slips in.
  //
  // Layout.tsx and SettingsPage.tsx are the instance logo: an admin-uploaded
  // image served from its own route under a policy that blocks scripts (see
  // logoCSP in security_headers.go). LoginPage.tsx is the TOTP enrolment QR
  // code, which is a data: URL the server generates during MFA setup — not an
  // upload at all.
  const allowedRenderers = new Set([
    'src/components/Layout.tsx:69',
    'src/pages/LoginPage.tsx:235',
    'src/pages/admin/SettingsPage.tsx:594',
  ])

  it('renders nothing that is not on the list', () => {
    // JSX allows whitespace and a newline between "<" and the tag name, and
    // a component can be built without JSX at all, so normalise first and
    // look for the other spellings too. createElement('img', …) renders just
    // as well as <img>, and so does an innerHTML string or a CSS url().
    const renderer =
      /<\s*(img|image|object|embed|iframe|frame|video|audio|source|track)\b|createElement\s*\(\s*['"`](img|image|object|embed|iframe|frame|video|audio|source|track)['"`]|dangerouslySetInnerHTML|\burl\s*\(/i

    const found = files.flatMap(({ path, source }) =>
      [...source.matchAll(new RegExp(renderer.source, 'gi'))].map(
        (m) => `${path}:${lineAt(source, m.index)}`,
      ),
    )

    const unexpected = found.filter((site) => !allowedRenderers.has(site))
    expect(
      unexpected,
      `A new element that renders remote content. If it renders an attachment ` +
        `it contradicts docs/DESIGN.md and is stored XSS against staff sessions. ` +
        `If it does not, add it to allowedRenderers with the reason.`,
    ).toEqual([])

    // The other direction: a line that moves or disappears leaves the list
    // stale, and a stale list quietly stops guarding anything.
    const gone = [...allowedRenderers].filter((site) => !found.includes(site))
    expect(gone, 'allowedRenderers is out of date — these no longer exist').toEqual([])
  })

  // Anything that addresses an attachment has to go through one function, or
  // the checks below only cover the paths that happen to use it. Matched on
  // the URL text in any form — a template literal, a concatenation, a plain
  // string — because the way the URL is spelled is not the point.
  it('has exactly one way to build an attachment URL', () => {
    // Matched on the path text in any spelling — template literal,
    // concatenation, a plain string, with or without the trailing slash —
    // because how the URL is written is not the point. The trailing slash
    // used to be required, which let ['…/attachments', id].join('/') past.
    const builders = files
      .filter(({ source }) => /['"`][^'"`]*\/attachments\b/.test(source))
      .map(({ path }) => path)
    expect(builders).toEqual(['src/api/tickets.ts'])
  })

  // And the one place that uses it has to ask the browser to save. The
  // server says so too; this is here so losing one does not lose both.
  it('uses the attachment URL only on a link that downloads', () => {
    const callers = files.filter(
      ({ path, source }) =>
        path !== 'src/api/tickets.ts' && source.includes('attachmentDownloadUrl'),
    )
    expect(callers.length, 'nothing links to an attachment any more').toBeGreaterThan(0)

    for (const { path, source } of callers) {
      // Every call site by character position, not by line. Searching from
      // the start of the line found the last "<" before the line's leading
      // whitespace, which is a different element: it rejected a correct
      // single-line <a download> and accepted a multi-line <a target="_blank">
      // whose className happened to contain the word "download".
      for (const call of callSites(source)) {
        const open = source.lastIndexOf('<', call)
        const element = source.slice(open, source.indexOf('>', open) + 1)
        const line = lineAt(source, call)
        expect(
          /^<\s*a\b/.test(element) && hasDownloadAttribute(element),
          `${path}:${line} uses an attachment URL outside an <a download>:\n${element}`,
        ).toBe(true)
      }
    }
  })

  // A blob or object URL is the other way to hand a browser something to
  // render, and it carries none of the server's headers — so it would defeat
  // the Go-side test completely. Checked across the whole frontend: there is
  // no legitimate use here today, so any appearance is worth a look.
  it('never turns a response into a blob or object URL', () => {
    const offenders = files
      .filter(({ source }) => /createObjectURL|new Blob\(/.test(source))
      .map(({ path }) => path)
    expect(
      offenders,
      'builds a blob, which renders outside the headers the server sends',
    ).toEqual([])
  })
})
