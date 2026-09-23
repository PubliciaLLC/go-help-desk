import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

// docs/DESIGN.md: "Every link to an attachment downloads it. No inline
// rendering of any attachment, images included: no lightbox, no thumbnail, no
// <img> pointing at the download route, no PDF viewer."
//
// That was true and pinned by nothing. The server-side header has a Go test,
// but the header is the second line of defence — the first is that this
// application never asks a browser to render an attachment in the first place.
// Someone adding an image preview would break the stated design, ship stored
// XSS against staff sessions, and no test would notice.
//
// This reads the source rather than rendering a component, because the claim is
// about the whole frontend rather than one page: a preview added to a new
// component would slip past a test that only rendered the current one.

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) {
      out.push(...sourceFiles(path))
    } else if (/\.(ts|tsx)$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(path)
    }
  }
  return out
}

const files = sourceFiles('src')

describe('attachments are never rendered inline', () => {
  // attachmentDownloadUrl is the only way to build an attachment URL. Anywhere
  // it appears, it must be feeding a download — not an element that renders.
  it('only ever reaches an <a download>, never a rendering element', () => {
    const renderers = /<(img|image|object|embed|iframe|video|audio|source|track)\b/i

    for (const file of files) {
      const src = readFileSync(file, 'utf8')
      if (!src.includes('attachmentDownloadUrl')) continue

      // Every element that renders remote content, in a file that also knows
      // how to build an attachment URL, is worth a human looking at.
      const lines = src.split('\n')
      lines.forEach((line, i) => {
        expect(
          renderers.test(line),
          `${file}:${i + 1} renders content in a file that builds attachment URLs. ` +
            `If this is an attachment preview, it contradicts docs/DESIGN.md; ` +
            `if it is not, move it to a file that does not import attachmentDownloadUrl.\n  ${line.trim()}`,
        ).toBe(false)
      })

      // And wherever the URL is used, the link must carry the download
      // attribute, so the browser saves rather than navigating even if a
      // response header is ever lost. Skipped for the file that defines the
      // function — defining it is not using it.
      if (!/export function attachmentDownloadUrl/.test(src)) {
        expect(
          /download[={]/.test(src),
          `${file} uses an attachment URL without a download attribute anywhere`,
        ).toBe(true)
      }
    }
  })

  // A blob or object URL is the other way to hand a browser something to
  // render, and it bypasses both the Content-Type and the Content-Disposition
  // the server sends — so it would defeat the Go-side test entirely.
  it('never turns an attachment into a blob or object URL', () => {
    for (const file of files) {
      const src = readFileSync(file, 'utf8')
      if (!src.includes('attachmentDownloadUrl')) continue
      expect(
        /createObjectURL|URL\.createObjectURL|new Blob\(/.test(src),
        `${file} builds a blob from an attachment, which renders outside the server's headers`,
      ).toBe(false)
    }
  })

  // The guard above only works while attachmentDownloadUrl is the single way
  // to address an attachment. A second URL builder would route around it.
  it('has exactly one way to build an attachment URL', () => {
    const builders = files.filter((f) =>
      /export (async )?function attachmentDownloadUrl|attachments\/\$\{/.test(readFileSync(f, 'utf8')),
    )
    expect(builders).toEqual(['src/api/tickets.ts'])
  })
})
