#!/usr/bin/env node
// Measures that no page scrolls sideways (#425, #477).
//
// Serves the site from the repository root, loads every page in Chromium and
// WebKit at each width, and fails when the document is wider than the viewport
// or when an element sticks out past the right edge without an
// overflow-x:auto/scroll/hidden box around it (a wide table or code block
// should scroll inside its own box, never the page).
//
//   NODE_PATH=/path/to/node_modules node scripts/layout-check.cjs
//
// `playwright` must be resolvable (the app repository has it in
// frontend/node_modules) with browsers installed:
//   npx playwright install chromium webkit
const http = require('http')
const fs = require('fs')
const path = require('path')
const { chromium, webkit } = require('playwright')

const ROOT = path.resolve(__dirname, '..')
const WIDTHS = [320, 375, 768, 900, 1024, 1280]
const TYPES = { '.html': 'text/html', '.css': 'text/css', '.js': 'text/javascript', '.svg': 'image/svg+xml', '.png': 'image/png', '.yaml': 'text/yaml', '.json': 'application/json' }

function pages() {
  const out = ['index.html', 'docs.html', '404.html']
  for (const f of fs.readdirSync(path.join(ROOT, 'docs'))) if (f.endsWith('.html')) out.push('docs/' + f)
  return out
}

const server = http.createServer((req, res) => {
  const p = path.join(ROOT, decodeURIComponent(req.url.split('?')[0]))
  if (!p.startsWith(ROOT) || !fs.existsSync(p) || fs.statSync(p).isDirectory()) { res.writeHead(404); return res.end() }
  res.writeHead(200, { 'content-type': TYPES[path.extname(p)] || 'application/octet-stream' })
  fs.createReadStream(p).pipe(res)
})

// Elements whose right edge passes the viewport and that no scroll box contains.
const probe = () => {
  const vw = window.innerWidth
  const out = []
  for (const e of document.body.querySelectorAll('*')) {
    const r = e.getBoundingClientRect()
    if (r.width === 0 || r.right <= vw + 1) continue
    const cs = getComputedStyle(e)
    if (cs.position === 'fixed' || cs.display === 'none' || cs.visibility === 'hidden') continue
    // An ancestor with overflow-x hidden or clip counts as a box, like auto and
    // scroll. That means this check accepts content that sticks out inside an
    // overflow-x:hidden/clip parent: it is cut off there, not scrolled to, so a
    // real clipping bug in such a parent will not be reported.
    let boxed = false
    for (let a = e.parentElement; a && a !== document.documentElement; a = a.parentElement) {
      const ox = getComputedStyle(a).overflowX
      if (ox === 'auto' || ox === 'scroll' || ox === 'hidden' || ox === 'clip') { boxed = true; break }
    }
    if (!boxed) out.push(`${e.tagName.toLowerCase()}${e.className ? '.' + String(e.className).split(' ')[0] : ''} right=${Math.round(r.right)}`)
  }
  // On a phone the docs text must start near the top, not after the whole sidebar (#477).
  const main = document.querySelector('main.docs-content')
  const top = main ? Math.round(main.getBoundingClientRect().top + window.scrollY) : 0
  return { sw: document.documentElement.scrollWidth, vw, stick: out.slice(0, 4), top: vw <= 768 ? top : 0 }
}

;(async () => {
  await new Promise((r) => server.listen(0, '127.0.0.1', r))
  const base = `http://127.0.0.1:${server.address().port}/`
  const list = pages()
  let bad = 0
  for (const [name, type] of [['chromium', chromium], ['webkit', webkit]]) {
    const browser = await type.launch()
    for (const w of WIDTHS) {
      const ctx = await browser.newContext({ viewport: { width: w, height: 900 } })
      const page = await ctx.newPage()
      for (const p of list) {
        await page.goto(base + p, { waitUntil: 'load' })
        const m = await page.evaluate(probe)
        if (m.sw > m.vw || m.stick.length || m.top > 500) {
          bad++
          console.log(`FAIL ${name} ${w}px ${p}: scrollWidth ${m.sw}/${m.vw}${m.stick.length ? '; ' + m.stick.join(', ') : ''}${m.top > 500 ? `; docs text starts ${m.top}px down the page` : ''}`)
        }
      }
      await ctx.close()
    }
    await browser.close()
  }
  server.close()
  console.log(bad ? `${bad} failing page/width/browser combinations` : `ok: ${list.length} pages x ${WIDTHS.length} widths x 2 browsers`)
  process.exit(bad ? 1 : 0)
})()
