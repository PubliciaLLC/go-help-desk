import { test, expect, type Page } from '@playwright/test'
import { mockApi, REPLIES, TICKET_DETAIL } from './mock'

// #443 security review: Markdown text is written by requesters, guests
// included, and opened by staff. Parsing it is not linear (a table header's
// width multiplies every short row, unclosed brackets rescan the rest of the
// text, hard breaks and nested lists add up), and a reply has no length cap.
// Counting markers in the text cannot catch all of that, so the formatting runs
// in a Web Worker with a time budget, and over-large text is never formatted.
// Each of these inputs must leave the page usable within a few seconds, with
// the post shown formatted or as plain text with a note, and its Plain text
// button reachable.

const MB = 1_000_000
const fill = (u: string, n: number) => u.repeat(Math.ceil(n / u.length)).slice(0, n)
const ladder = (ch: string, n: number, sep: string) => Array.from({ length: n }, (_, k) => ch.repeat(k + 1) + sep).join('')

// [name, body, must the post fall back to plain text?]
const inputs: [string, string, boolean][] = [
  ['baseline 1 MB words', fill('lorem ipsum ', MB), true],
  ['1 MB soft newlines', fill('a\n', MB), true],
  ['1 MB hard breaks', fill('a  \n', MB), true],
  ['1 MB backslash hard breaks', fill('a\\\n', MB), true],
  ['2999 "[" then 300 KB', '[a '.repeat(2999) + fill('b ', 300_000), true],
  ['2-column table at 1 MB', '|a|b|\n|-|-|\n' + fill('|x|y|\n', MB), true],
  ['1000-column table, 1 MB of short rows', '|' + 'a|'.repeat(1000) + '\n|' + '-|'.repeat(1000) + '\n' + fill('|x\n', MB), true],
  ['1 MB indented list ladder', Array.from({ length: 1000 }, (_, i) => ' '.repeat(i * 2) + '- x').join('\n'), true],
  ['1 MB code fence json', '```json\n' + fill('[{"a":', MB) + '\n```', true],
  ['50K: 300-column table header, short rows', ('|' + 'a|'.repeat(300) + '\n|' + '-|'.repeat(300) + '\n' + fill('|x\n', 50_000)).slice(0, 50_000), false],
  ['50K: 1000-column table header, short rows', ('|' + 'a|'.repeat(1000) + '\n|' + '-|'.repeat(1000) + '\n' + fill('|x\n', 50_000)).slice(0, 50_000), false],
  ['50K: 2999 "[" then text', ('[a '.repeat(2999) + fill('b ', 50_000)).slice(0, 50_000), false],
  ['50K: indented list ladder, depth 220', Array.from({ length: 220 }, (_, i) => ' '.repeat(i * 2) + '- x').join('\n'), false],
  ['50K: indented ordered ladder, depth 150', Array.from({ length: 150 }, (_, i) => ' '.repeat(i * 3) + '1. x').join('\n'), false],
  ['50K: backtick ladder', ladder('`', 310, ' x '), false],
  ['50K: hard breaks', fill('a  \n', 50_000), false],
  ['200K: hard breaks', fill('a  \n', 200_000), true],
  ['1990 unclosed "<!--" then text', '<!--a '.repeat(1990) + fill('a ', 40_000), false],
  ['1 MB links', fill('[a](http://x.example) ', MB), true],
  ['1 MB emphasis pairs', fill('*a* ', MB), true],
]

async function responsive(page: Page) {
  let worst = 0
  for (let i = 0; i < 6; i++) {
    const t = Date.now()
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => r(1))))
    worst = Math.max(worst, Date.now() - t)
    await page.waitForTimeout(150)
  }
  return worst
}

for (const [where, mk] of [
  ['a reply', (body: string) => ({ replies: [{ ...REPLIES[0], body, body_format: 'markdown' }] })],
  ['a description', (body: string) => ({ ticket: { ...TICKET_DETAIL, description: body, description_format: 'markdown' } })],
] as const) {
  test.describe(`hostile Markdown in ${where}`, () => {
    for (const [name, body, mustFallBack] of inputs) {
      test(name, async ({ page }) => {
        test.setTimeout(30_000)
        let crashed = false
        page.on('crash', () => (crashed = true))
        await mockApi(page, mk(body))
        await page.goto(`/tickets/${TICKET_DETAIL.id}`)
        const worst = await responsive(page)
        expect(worst, 'the page stopped answering while the text was formatted').toBeLessThan(1500)
        const toggle = page.getByRole('button', { name: 'Plain text' }).first()
        await expect(toggle).toBeVisible({ timeout: 8000 })
        expect(crashed).toBe(false)
        if (mustFallBack) {
          await expect(page.getByText(/Shown as plain text: too (long|complex) to format/).first()).toBeVisible()
        }
        // The stored text can always be reached.
        await toggle.click()
        await expect(toggle).toHaveAttribute('aria-pressed', 'true')
      })
    }
  })
}

test('a normal post is still formatted, and soon', async ({ page }) => {
  await mockApi(page, { replies: [{ ...REPLIES[0], body: '**bold** and a | b', body_format: 'markdown' }] })
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  await expect(page.locator('[data-md-body] strong').first()).toHaveText('bold', { timeout: 3000 })
})

// Memory: every formatted post holds a tree and its elements. 220 replies of
// 50 KB each took 3.76 GB of heap when all were formatted. Posts now format only
// near the screen and the page has a budget, so the heap stays bounded however
// many there are, and whatever is scrolled past.
const lines = (seed: number) => Array.from({ length: 12_400 }, (_, i) => `${String.fromCharCode(97 + ((i + seed) % 26))}  `).join('\n')
// Live memory, not garbage: usedJSHeapSize counts objects nobody references any
// more until the collector next runs, which on a slow machine can be after the
// reading (one run read 1998 MB against a 900 MB bound, and passed with a forced
// collection). So collect first, then read. The bound itself is unchanged.
const heapMB = async (page: Page) => {
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('HeapProfiler.collectGarbage')
  await cdp.detach()
  return page.evaluate(() => ((performance as unknown as { memory?: { usedJSHeapSize: number } }).memory?.usedJSHeapSize ?? 0) / 1e6)
}

test('220 hostile replies keep the heap bounded, on open and after jumping through them', async ({ page }) => {
  test.setTimeout(180_000)
  let crashed = false
  page.on('crash', () => (crashed = true))
  const replies = Array.from({ length: 220 }, (_, i) => ({ ...REPLIES[0], id: `r-${i}`, body: lines(i), body_format: 'markdown' }))
  await mockApi(page, { replies })
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  await expect(page.locator('[data-md-body]').first()).toBeVisible({ timeout: 15_000 })
  await page.waitForTimeout(3000)

  expect(await page.locator('[data-md-body]').count(), 'posts far below the screen were formatted').toBeLessThan(25)
  expect(await heapMB(page)).toBeLessThan(500)

  // Each reply is hundreds of thousands of pixels tall as plain text, so jump
  // from one to the next (then back to the top) instead of scrolling a screen
  // at a time: every post that comes near tries to format.
  const height = await page.evaluate(() => document.querySelector('main')!.scrollHeight)
  for (let i = 1; i <= 60 && !crashed; i++) {
    await page.evaluate((y) => document.querySelector('main')!.scrollTo(0, y), Math.floor((height / 60) * i))
    await page.waitForTimeout(400)
  }
  await page.evaluate(() => document.querySelector('main')!.scrollTo(0, 0))
  await page.waitForTimeout(1500)
  expect(crashed).toBe(false)
  // Past the budget the rest stay as written, with the note.
  expect(await page.locator('[data-md-body]').count()).toBeLessThan(40)
  await expect(page.getByText(/Shown as plain text: this page already shows a lot of formatted text/).first()).toBeAttached()
  expect(await heapMB(page)).toBeLessThan(900)
})

// A very long plain text is drawn as blocks the browser can skip while off
// screen (lib/plainChunks.ts). That must not change what is there: selecting the
// post and copying gives back exactly the stored text, and finding text deep in
// it still works.
test('a very long plain post keeps every character, selectable as one text', async ({ page }) => {
  const text = Array.from({ length: 30_000 }, (_, i) => `line ${i}`).join('\n')
  await mockApi(page, { replies: [{ ...REPLIES[0], body: text, body_format: 'plain' }] })
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  await expect(page.locator('[data-plain-chunk]').first()).toBeAttached()
  const copied = await page.evaluate(() => {
    const chunks = document.querySelectorAll('[data-plain-chunk]')
    const range = document.createRange()
    range.setStartBefore(chunks[0])
    range.setEndAfter(chunks[chunks.length - 1])
    const sel = window.getSelection()!
    sel.removeAllRanges()
    sel.addRange(range)
    return sel.toString()
  })
  expect(copied).toBe(text)
})
