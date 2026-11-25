import { readFileSync } from 'fs'
import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_ADMIN, ME_STAFF, KPI_RESPONSE, LONG_CATEGORY } from './mock'

// #131: the Reports page. DESIGN.md, KPI reports, Layout: nothing scrolls
// sideways at 375 px or 1280 px, with a 120-character name in a chart label, a
// select and the filter line; every button, select and date input is at least
// 44 px on a touch screen. And the exports work in a real browser: a PNG per
// card and one of the board whose width is twice the element's on screen, and
// a CSV with its header row. (Under the production CSP: e2e-csp/.)

const CATEGORIES = [
  { id: 'cat-long', name: LONG_CATEGORY, sort_order: 1, active: true },
  { id: 'cat-1', name: 'Hardware', sort_order: 2, active: true },
]

const roles = [
  { who: 'an administrator', me: ME_ADMIN },
  { who: 'staff', me: ME_STAFF },
] as const

async function noHorizontalScroll(page: Page, width: number) {
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, `main is wider than its own box at ${width}px`).toBeLessThanOrEqual(clientWidth)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
}

// The long category chosen, so the filter line holds its name too.
const FILTERED = { ...KPI_RESPONSE, filters: { ...KPI_RESPONSE.filters, category_id: 'cat-long' } }

async function open(page: Page, me: object) {
  await mockApi(page, { me, kpi: FILTERED, categories: CATEGORIES })
  await page.goto('/reports?category_id=cat-long')
  await expect(page.getByRole('region', { name: 'Workload' }).getByRole('button', { name: 'Download Workload as an image' })).toBeVisible()
}

for (const { who, me } of roles) {
  test.describe(`Reports for ${who}`, () => {
    test.describe('at 375px on a touch screen', () => {
      test.use({ viewport: { width: 375, height: 800 }, hasTouch: true, isMobile: true })

      test('nothing scrolls sideways and every control is at least 44px', async ({ page }) => {
        await open(page, me)
        await expect(page.getByText(`${LONG_CATEGORY} · All priorities · All groups`)).toBeVisible()
        await noHorizontalScroll(page, 375)

        const controls = page.locator('main button:visible, main select:visible, main input:visible')
        const n = await controls.count()
        expect(n).toBeGreaterThan(10)
        for (let i = 0; i < n; i++) {
          const box = await controls.nth(i).boundingBox()
          const what = await controls.nth(i).evaluate((e) => e.getAttribute('aria-label') ?? e.textContent ?? e.tagName)
          expect(box!.height, `${what} is shorter than 44px`).toBeGreaterThanOrEqual(44)
          if (await controls.nth(i).evaluate((e) => e.tagName === 'BUTTON' && e.hasAttribute('aria-label'))) {
            expect(box!.width, `${what} is narrower than 44px`).toBeGreaterThanOrEqual(44)
          }
          expect(box!.x + box!.width, `${what} runs off the right edge`).toBeLessThanOrEqual(375)
        }

        // One column: the cards are stacked, not side by side.
        const first = await page.getByRole('region', { name: 'Time to resolve' }).boundingBox()
        const second = await page.getByRole('region', { name: 'First response' }).boundingBox()
        expect(second!.y).toBeGreaterThan(first!.y)
        // Custom range: the dates stack and fit.
        await page.getByRole('button', { name: 'Custom' }).click()
        await expect(page.getByLabel('From')).toBeVisible()
        await noHorizontalScroll(page, 375)
      })
    })

    test.describe('at 1280px', () => {
      test.use({ viewport: { width: 1280, height: 900 } })

      test('nothing scrolls sideways, in three columns, with Volume and Workload full width', async ({ page }) => {
        await open(page, me)
        await noHorizontalScroll(page, 1280)
        const box = async (name: string) => (await page.getByRole('region', { name }).boundingBox())!
        const [a, b, c, volume] = [await box('Time to resolve'), await box('First response'), await box('SLA attainment'), await box('Volume')]
        expect(new Set([a.y, b.y, c.y]).size, 'the first three cards are not in one row').toBe(1)
        expect(b.x).toBeGreaterThan(a.x)
        expect(c.x).toBeGreaterThan(b.x)
        expect(volume.width).toBeGreaterThan(a.width * 2.5)
      })
    })
  })
}

// DESIGN.md, KPI reports, The page: the address reads range=7 and priority=high.
// The router quotes a string that reads as JSON, so this looks at the real
// address in a real browser, not at the record the page hands the router.
test.describe('the Reports address', () => {
  test.use({ viewport: { width: 1280, height: 900 } })

  test('a preset and one priority are written as range=7 and priority=high', async ({ page }) => {
    await mockApi(page, { me: ME_ADMIN, categories: CATEGORIES })
    await page.goto('/reports')
    await expect(page.getByText(/compared with/)).toBeVisible()

    await page.getByRole('button', { name: '7 days' }).click()
    await expect(page.getByRole('button', { name: '7 days' })).toHaveAttribute('aria-pressed', 'true')
    expect(new URL(page.url()).search).toBe('?range=7')

    await page.getByLabel('Priority', { exact: true }).selectOption({ label: 'High' })
    await expect.poll(() => new URL(page.url()).search).toBe('?range=7&priority=high')

    // And the address, typed or shared, shows the same board.
    await page.reload()
    await expect(page.getByRole('button', { name: '7 days' })).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByLabel('Priority', { exact: true })).toHaveValue('high')
  })
})

test.describe('Reports exports', () => {
  test.use({ viewport: { width: 1280, height: 900 } })
  test.skip(({ browserName }) => browserName !== 'chromium', 'the downloads are checked in Chromium')

  // The PNG's signature, then the width in its IHDR chunk.
  function readPng(path: string): { width: number; height: number } {
    const b = readFileSync(path)
    expect([...b.subarray(0, 8)]).toEqual([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
    return { width: b.readUInt32BE(16), height: b.readUInt32BE(20) }
  }

  test('Download image on a card yields a PNG twice as wide as the card on screen', async ({ page }) => {
    await mockApi(page, { me: ME_ADMIN, categories: CATEGORIES })
    await page.goto('/reports')
    const card = page.getByRole('region', { name: 'Time to resolve' })
    await expect(card.getByText('79 tickets resolved')).toBeVisible()
    const shown = (await card.boundingBox())!

    const [download] = await Promise.all([
      page.waitForEvent('download'),
      card.getByRole('button', { name: 'Download Time to resolve as an image' }).click(),
    ])
    expect(download.suggestedFilename()).toBe('kpi-time_to_resolve-2026-10-26-to-2026-11-24.png')
    const png = readPng((await download.path())!)
    expect(Math.abs(png.width - 2 * shown.width)).toBeLessThanOrEqual(1)
    // The card, and the strip under it.
    expect(png.height).toBeGreaterThan(2 * shown.height)
  })

  // #709: an image is the same whichever theme the person is in (it is for
  // printing). Exported from the dark theme it must be the image the light theme
  // exports: the paper (top-left pixel) is white, and the two agree on all but a
  // sliver of pixels. Not all: the line of small text under the chart is
  // anti-aliased a hair differently from one run to the next, in light against
  // light as well. A dark-themed export would differ on nearly every pixel.
  test('Download image while dark is the same image as in light, white paper', async ({ browser }) => {
    async function exportCard(theme: 'light' | 'dark') {
      const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, colorScheme: 'light' })
      await context.addInitScript((t) => window.localStorage.setItem('ghd.theme', t), theme)
      const page = await context.newPage()
      await mockApi(page, { me: { ...ME_ADMIN, theme }, categories: CATEGORIES })
      await page.goto('/reports')
      const card = page.getByRole('region', { name: 'Time to resolve' })
      await expect(card.getByText('79 tickets resolved')).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.classList.contains('dark'))).toBe(theme === 'dark')
      const [download] = await Promise.all([
        page.waitForEvent('download'),
        card.getByRole('button', { name: 'Download Time to resolve as an image' }).click(),
      ])
      const bytes = readFileSync((await download.path())!)
      // Still the theme it was: the export put it back.
      expect(await page.evaluate(() => document.documentElement.classList.contains('dark'))).toBe(theme === 'dark')
      await context.close()
      return bytes.toString('base64')
    }

    const light = await exportCard('light')
    const dark = await exportCard('dark')
    // Decode both in a page with a canvas and compare the pixels.
    const page = await browser.newPage()
    await page.goto('about:blank')
    const result = await page.evaluate(async ([a, b]) => {
      async function pixels(b64: string) {
        const img = new Image()
        img.src = 'data:image/png;base64,' + b64
        await img.decode()
        const c = document.createElement('canvas')
        c.width = img.width
        c.height = img.height
        const ctx = c.getContext('2d')!
        ctx.drawImage(img, 0, 0)
        return { width: img.width, height: img.height, data: ctx.getImageData(0, 0, img.width, img.height).data }
      }
      const [x, y] = [await pixels(a), await pixels(b)]
      let different = 0
      for (let i = 0; i < x.data.length; i += 4) {
        if (x.data[i] !== y.data[i] || x.data[i + 1] !== y.data[i + 1] || x.data[i + 2] !== y.data[i + 2]) different++
      }
      return {
        sameSize: x.width === y.width && x.height === y.height,
        lightTopLeft: Array.from(x.data.slice(0, 4)),
        darkTopLeft: Array.from(y.data.slice(0, 4)),
        share: different / (x.data.length / 4),
      }
    }, [light, dark])
    await page.close()
    expect(result.lightTopLeft).toEqual([255, 255, 255, 255])
    expect(result.darkTopLeft).toEqual([255, 255, 255, 255])
    expect(result.sameSize).toBe(true)
    expect(result.share, 'the dark export differs from the light one').toBeLessThan(0.02)
  })

  test('Download image on the board yields a PNG twice as wide as the board on screen', async ({ page }) => {
    await mockApi(page, { me: ME_STAFF, categories: CATEGORIES })
    await page.goto('/reports')
    await expect(page.getByRole('region', { name: 'Workload' }).getByTitle('Service Desk')).toBeVisible()
    const shown = (await page.locator('#reports-board').boundingBox())!

    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Download image', exact: true }).click(),
    ])
    expect(download.suggestedFilename()).toBe('kpis-2026-10-26-to-2026-11-24.png')
    const png = readPng((await download.path())!)
    expect(Math.abs(png.width - 2 * shown.width)).toBeLessThanOrEqual(1)
    expect(png.height).toBeGreaterThan(2 * shown.height)
  })

  test('Download CSV yields the file with its header row and the change column', async ({ page }) => {
    await mockApi(page, { me: ME_ADMIN, categories: CATEGORIES })
    await page.goto('/reports')
    await expect(page.getByText(/compared with/)).toBeVisible()
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Download CSV' }).click(),
    ])
    expect(download.suggestedFilename()).toBe('kpis-2026-10-26-to-2026-11-24.csv')
    const text = readFileSync((await download.path())!, 'utf8')
    expect(text.startsWith('﻿measure,key,label,current,previous,change,unit\r\n')).toBe(true)
    expect(text).toContain('\r\ncreated,,Created,84,72,0.1667,count\r\n')
    expect(text).toContain('\r\nworkload_person,u-long,' + LONG_CATEGORY + ',7,,,count\r\n')
  })
})
