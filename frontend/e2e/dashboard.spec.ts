import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_ADMIN, ME_REPORTER, ME_STAFF, LONG_SUBJECT } from './mock'

// #675: the dashboard's queue strip, needs-attention list and 30-day trend.
// DESIGN.md, Dashboard, Layout: nothing scrolls sideways (the attention list
// holds a 300-character subject with no break point), and every strip cell is at
// least 44px tall on a touch screen. Run for each role at a phone width and at
// desktop width.

async function noHorizontalScroll(page: Page, width: number) {
  // main's own box: the shell around it is overflow-hidden, so the document can
  // never be wider than the viewport whatever overflows inside main.
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, `main is wider than its own box at ${width}px`).toBeLessThanOrEqual(clientWidth)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
}


// The shell is exactly one viewport tall (h-dvh) and scrolls inside <main>, so
// the document itself must never scroll. A screen-reader-only chart table that
// escaped <main>'s clipping made it scroll: a blank block under the footer.
async function documentDoesNotScroll(page: Page) {
  const { scrollHeight, innerHeight } = await page.evaluate(() => ({
    scrollHeight: document.documentElement.scrollHeight,
    innerHeight: window.innerHeight,
  }))
  expect(scrollHeight, 'the page scrolls as a whole: something escapes <main>').toBeLessThanOrEqual(innerHeight)
}

const roles = [
  { who: 'an administrator', me: ME_ADMIN, nav: 'Your queue', cells: 5, staff: true },
  { who: 'staff', me: ME_STAFF, nav: 'Your queue', cells: 5, staff: true },
  { who: 'a requester', me: ME_REPORTER, nav: 'Your tickets', cells: 1, staff: false },
] as const

async function open(page: Page, me: object, nav: string) {
  await mockApi(page, { me })
  await page.goto('/dashboard')
  await expect(page.getByRole('navigation', { name: nav })).toBeVisible()
}

for (const { who, me, nav, cells, staff } of roles) {
  test.describe(`the dashboard for ${who}`, () => {
    test.describe('at 375px on a touch screen', () => {
      test.use({ viewport: { width: 375, height: 800 }, hasTouch: true, isMobile: true })

      test('nothing scrolls sideways and every cell is at least 44px tall', async ({ page }) => {
        await open(page, me, nav)
        const links = page.getByRole('navigation', { name: nav }).getByRole('link')
        await expect(links).toHaveCount(cells)
        for (let i = 0; i < cells; i++) {
          const box = await links.nth(i).boundingBox()
          expect(box, `cell ${i + 1} is not laid out`).not.toBeNull()
          expect(box!.height, `cell ${i + 1} is shorter than 44px`).toBeGreaterThanOrEqual(44)
          expect(box!.x, `cell ${i + 1} starts off the left edge`).toBeGreaterThanOrEqual(0)
          expect(box!.x + box!.width, `cell ${i + 1} runs off the right edge`).toBeLessThanOrEqual(375)
        }
        if (staff) {
          // Two columns below sm: the first two cells share a row.
          const [a, b] = [await links.nth(0).boundingBox(), await links.nth(1).boundingBox()]
          expect(b!.y).toBe(a!.y)
          await expect(page.getByText(LONG_SUBJECT)).toBeVisible()
        }
        await noHorizontalScroll(page, 375)
      })
    })

    test.describe('at 1280px', () => {
      test.use({ viewport: { width: 1280, height: 900 } })

      test('nothing scrolls sideways', async ({ page }) => {
        await open(page, me, nav)
        if (staff) {
          // One row of five from lg.
          const links = page.getByRole('navigation', { name: nav }).getByRole('link')
          const ys = await Promise.all(Array.from({ length: cells }, async (_, i) => (await links.nth(i).boundingBox())!.y))
          expect(new Set(ys).size, 'the five cells are not in one row').toBe(1)
          await expect(page.getByRole('heading', { name: 'Needs attention' })).toBeVisible()
          await expect(page.getByRole('heading', { name: 'Tickets in the last 30 days' })).toBeVisible()
          await expect(page.getByText(LONG_SUBJECT)).toBeVisible()
        } else {
          await expect(page.getByRole('heading', { name: 'Needs attention' })).toHaveCount(0)
        }
        await noHorizontalScroll(page, 1280)
      })
    })
  })
}

test.describe('the dashboard does not scroll as a whole', () => {
  for (const [w, h] of [[1280, 900], [1920, 1000], [375, 800]] as const) {
    test(`at ${w}x${h}, with the 30-day trend`, async ({ page }) => {
      await page.setViewportSize({ width: w, height: h })
      await open(page, ME_STAFF, 'Your queue')
      await documentDoesNotScroll(page)
    })
  }
})
