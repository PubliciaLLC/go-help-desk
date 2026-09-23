import { describe, expect, it } from 'vitest'

// docs/DESIGN.md: "Every link to an attachment downloads it. No inline
// rendering of any attachment, images included: no lightbox, no thumbnail, no
// <img> pointing at the download route, no PDF viewer."
//
// The server sends headers that stop a browser rendering an attachment, and
// those have a Go test. But a browser ignores Content-Disposition for a
// subresource: point an <img> or an <iframe> at the download route from our
// own pages and it renders anyway, on our origin, where the staff sessions
// live. So the first line of defence is that this application never asks for
// an attachment to be rendered, and that is what these tests hold.
//
// They read source text rather than rendering components, because the claim is
// about the whole frontend and not one page.
//
// What text-matching can and cannot do, stated plainly so nobody trusts this
// further than it goes. It CAN tell you every element in this frontend that
// renders remote content, and every place an attachment URL is built. It
// CANNOT follow a value across files — if a component builds an attachment URL
// and passes it as a prop to an <img> somewhere else, no regular expression
// here is going to see it. The first test closes that gap by listing every
// renderer in the frontend rather than only the ones in files that mention
// attachments: a new <img> anywhere fails, and a person has to look at it and
// say why it is allowed.

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

function lines(source: string): { n: number; text: string }[] {
  return source.split('\n').map((text, i) => ({ n: i + 1, text }))
}

describe('attachments are never rendered inline', () => {
  // Every element in the frontend that renders remote content, with the reason
  // it is allowed to exist. This is deliberately a list and not a rule: the
  // point is that adding one is a decision someone makes on purpose, in a
  // review, rather than something that slips in.
  //
  // All three are the instance logo, which is an admin-uploaded image served
  // from its own route under a CSP that blocks scripts — see handler_logo.go.
  const allowedRenderers = new Set([
    'src/components/Layout.tsx:69',
    'src/pages/LoginPage.tsx:235',
    'src/pages/admin/SettingsPage.tsx:594',
  ])

  it('renders nothing that is not on the list', () => {
    const renderer = /<(img|image|object|embed|iframe|video|audio|source|track)\b/i

    const found = files.flatMap(({ path, source }) =>
      lines(source)
        .filter((l) => renderer.test(l.text))
        .map((l) => `${path}:${l.n}`),
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
    const builders = files
      .filter(({ source }) => /['"`][^'"`]*\/attachments\//.test(source))
      .map(({ path }) => path)
    expect(builders).toEqual(['src/api/tickets.ts'])
  })

  // And the one place that uses it has to ask the browser to save. The header
  // says so too; this is here so losing one does not lose both.
  it('uses the attachment URL only on a link that downloads', () => {
    const callers = files.filter(
      ({ path, source }) =>
        path !== 'src/api/tickets.ts' && source.includes('attachmentDownloadUrl'),
    )
    expect(callers.length, 'nothing links to an attachment any more').toBeGreaterThan(0)

    for (const { path, source } of callers) {
      for (const l of lines(source)) {
        if (!l.text.includes('attachmentDownloadUrl(')) continue
        // The href and the download attribute are written across several
        // lines, so look at the element around the call rather than the line.
        const start = source.lastIndexOf('<', source.indexOf(l.text))
        const element = source.slice(start, source.indexOf('>', start) + 1)
        expect(
          /^<a\b/.test(element) && /\bdownload\b/.test(element),
          `${path}:${l.n} uses an attachment URL outside an <a download>:\n${element}`,
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
