import { test, expect } from '@playwright/test'
import { mockApi, REPLIES, TICKET_DETAIL } from './mock'

// Long plain-text posts are laid out only when they scroll near the screen
// (.lazy-layout, content-visibility: auto). Until a post is drawn the browser
// holds a height for it: TicketText's estimate from the post's own rows at the
// box's width. A wrong estimate shows as the page moving under the reader, worst
// in WebKit, which does not keep the reader's place when content above changes
// size (measured with an estimate of one row per stored line: jumps of 8,000 to
// 20,000 px and a page that grew by 40 to 140 percent as it was read; with one
// unit per UTF-16 character, Chinese text jumped 2,820 to 11,060 px). English and
// Chinese are both run, in Chromium and WebKit, at the widths the layout is
// tested at.

const words = 'the printer on the second floor stopped answering after the update and nobody could reach the spooler service again so we restarted it twice'.split(' ')
// The same posts in Chinese: no spaces, every character twice the width of a
// Latin one. Counting one unit per character made the estimate half the drawn
// height, and Safari lost the reader's place by 5 to 13 screens.
const sentences = '二楼的打印机在更新之后就没有响应了，没有人能够连接到打印服务。我们已经重启了两次，但是问题依然存在，请尽快处理。'.split('')
type Language = 'English' | 'Chinese'
function prose(language: Language, seed: number, chars: number) {
  let out = ''
  for (let n = seed; out.length < chars; n++) {
    let para = ''
    if (language === 'English') {
      for (let i = 0; i < 40 + (n % 70); i++) para += words[(n * 7 + i * 3) % words.length] + ' '
      out += para.trim() + '.\n\n'
    } else {
      for (let i = 0; i < 200 + (n % 300); i++) para += sentences[(n * 7 + i) % sentences.length]
      out += para + '\n\n'
    }
  }
  return out.slice(0, chars)
}
// Mixed sizes, all over the 5,000-character threshold but one in five short.
const repliesIn = (language: Language) =>
  Array.from({ length: 30 }, (_, i) => ({
    ...REPLIES[0],
    id: `r-${i}`,
    body: i % 5 === 4 ? prose(language, i, 200) : prose(language, i, 6000 + ((i * 7919) % 42000)),
    body_format: 'plain',
  }))

// The estimate cannot assume a font: the CI runner's sans-serif is wider than a
// Mac's, and a 10 percent error showed as a 2,000 px jump in WebKit there. A
// monospace font, a fifth wider than any sans-serif, is the same kind of change
// made larger.
const cases = [
  { language: 'English', font: 'the default font' },
  { language: 'Chinese', font: 'the default font' },
  { language: 'English', font: 'a monospace font' },
] as const

for (const { language, font } of cases)
for (const [width, height] of [[320, 640], [390, 844], [768, 1024], [1440, 900]] as const) {
  test(`long plain posts in ${language} in ${font} do not move the page under the reader: ${width}px`, async ({ page }) => {
    test.setTimeout(120_000)
    await page.setViewportSize({ width, height })
    if (font === 'a monospace font') {
      await page.addInitScript(() => {
        document.addEventListener('DOMContentLoaded', () => {
          const style = document.createElement('style')
          style.textContent = "p.whitespace-pre-wrap { font-family: Menlo, 'Courier New', monospace !important }"
          document.head.append(style)
        })
      })
    }
    const replies = repliesIn(language)
    await mockApi(page, { replies: replies as never })
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    await page.waitForFunction((n) => document.querySelectorAll('p.whitespace-pre-wrap').length >= n, replies.length)
    // Every lazy post has its estimate. It is set by a ResizeObserver, a frame or
    // more after the post is in the page, and on a slow runner the first frames are
    // long: a page measured before then is one row per post (4,821 px), which is
    // not what a reader scrolls.
    await page.waitForFunction(() => {
      const lazy = Array.from(document.querySelectorAll<HTMLElement>('p.lazy-layout'))
      return lazy.length > 0 && lazy.every((p) => p.style.getPropertyValue('--text-rows') !== '')
    })

    const r = await page.evaluate(async () => {
      const frame = () => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(() => res(0))))
      const settle = async (ms = 80) => {
        await frame()
        await new Promise((res) => setTimeout(res, ms))
      }
      const main = document.querySelector('main')!
      const top = () => main.getBoundingClientRect().top
      const posts = () => Array.from(document.querySelectorAll('p.whitespace-pre-wrap')) as HTMLElement[]
      const lazy = posts().filter((p) => p.classList.contains('lazy-layout')).length
      // The rule itself, not only the class: a post that is not drawn is skipped by the browser.
      const skipping = posts().filter((p) => p.classList.contains('lazy-layout') && getComputedStyle(p).contentVisibility === 'auto').length
      const heights = { min: main.scrollHeight, max: main.scrollHeight }
      let worst = 0
      // How far the post under the middle of the screen moved, beyond the scroll asked for.
      const sweep = async (steps: number, dy: number) => {
        for (let s = 0; s < steps; s++) {
          const mid = top() + main.clientHeight / 2
          const anchor = posts().find((p) => {
            const b = p.getBoundingClientRect()
            return b.top <= mid && b.bottom >= mid
          })
          const before = anchor?.getBoundingClientRect().top ?? 0
          main.scrollBy(0, dy)
          await settle()
          if (anchor) worst = Math.max(worst, Math.abs(anchor.getBoundingClientRect().top - before + dy))
          heights.min = Math.min(heights.min, main.scrollHeight)
          heights.max = Math.max(heights.max, main.scrollHeight)
        }
      }
      const step = Math.floor(main.clientHeight * 0.9)
      await sweep(20, step) // from the top, downwards
      main.scrollTo(0, Math.floor(main.scrollHeight * 0.7)) // a deep jump, then on
      await settle(300)
      await sweep(20, step)
      main.scrollTo(0, main.scrollHeight) // the bottom, then back up through posts never drawn
      await settle(300)
      await sweep(20, -step)

      // Jumping to a reply by link: the target should land at the top and stay.
      const landed: number[] = []
      for (const fraction of [0.3, 0.7, 0.95]) {
        const target = posts()[Math.floor(posts().length * fraction)]
        target.id = `reply-${fraction}`
        main.scrollTo(0, 0)
        await settle(200)
        location.hash = `#${target.id}`
        await settle(700)
        landed.push(Math.round(Math.abs(target.getBoundingClientRect().top - top())))
      }

      // Select All still holds every post: nothing is dropped from a copy.
      const range = document.createRange()
      range.selectNodeContents(main)
      const selection = getSelection()!
      selection.removeAllRanges()
      selection.addRange(range)
      const selected = selection.toString()
      const missing = posts().filter((p) => !selected.includes(p.textContent!.slice(-60).trim())).length
      return { lazy, skipping, worst, heights, landed, missing, viewport: main.clientHeight }
    })

    console.log(`${language}, ${font}, ${width}px: worst jump ${r.worst}px, page ${r.heights.min} to ${r.heights.max}, landed ${r.landed.join('/')}`)
    expect(r.lazy, 'long posts should be lazy').toBe(24)
    expect(r.skipping, 'every lazy post should compute content-visibility: auto').toBe(24)
    expect(r.missing, 'posts missing from Select All').toBe(0)
    // The page keeps its size as posts are drawn (it grew 40 to 140% with a one-row-per-line estimate).
    expect(r.heights.max / r.heights.min).toBeLessThan(1.05)
    // No visible jump larger than three screens. The estimate is within about 7 percent
    // of a post that can be ten thousand pixels tall, so a screen or so is what it
    // costs in WebKit; the mistakes this exists to catch were 5 to 20 screens.
    expect(r.worst).toBeLessThan(r.viewport * 3)
    // A link to a reply lands on it, and stays there.
    for (const off of r.landed) expect(off).toBeLessThan(5)
  })
}
