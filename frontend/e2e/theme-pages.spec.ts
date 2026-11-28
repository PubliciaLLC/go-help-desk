import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_ADMIN, ME_STAFF, STATUSES, TICKET_DETAIL } from './mock'

// #709, the places the switch and the theme reach beyond the login page: the
// drawer, a touch screen with the desktop sidebar, a status colour an
// administrator chose, Your account's failed save, and a look at every main page
// for a light card in dark. (theme.spec.ts has the persistence and FOUC cases,
// axe-theme.spec.ts the contrast.)

const dark = (page: Page) => page.evaluate(() => document.documentElement.classList.contains('dark'))

async function signedIn(page: Page, theme: 'light' | 'dark' | 'device', me: object = ME_STAFF, statuses?: object[]) {
  const patches: unknown[] = []
  await mockApi(page, { me: { ...me, theme } })
  if (statuses) await page.route('**/api/v1/statuses', (route) => route.fulfill({ json: statuses }))
  await page.route('**/api/v1/me/appearance', async (route) => {
    const body = route.request().postDataJSON() as { theme: string }
    patches.push(body)
    await route.fulfill({ json: { ...me, theme: body.theme } })
  })
  return patches
}

test.describe('the drawer on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test('has its own working switch, independent of the hidden sidebar one', async ({ page }) => {
    await signedIn(page, 'light')
    await page.goto('/dashboard')
    await page.getByRole('button', { name: /menu|navigation/i }).click()
    const drawer = page.getByRole('dialog')
    const group = drawer.getByRole('group', { name: 'Appearance' })
    await expect(group).toBeVisible()

    // Two switches are in the page (the desktop sidebar is hidden), in two radio groups.
    const names = await page.locator('input[type="radio"]').evaluateAll((els) => new Set(els.map((e) => (e as HTMLInputElement).name)).size)
    expect(names).toBe(2)

    // It fits the drawer and Sign out sits beside it on one line.
    const sw = (await group.boundingBox())!
    const box = (await drawer.boundingBox())!
    expect(sw.x + sw.width).toBeLessThanOrEqual(box.x + box.width)
    const out = (await drawer.getByRole('button', { name: 'Sign out' }).boundingBox())!
    expect(out.x + out.width).toBeLessThanOrEqual(sw.x + 0.5)

    await group.getByTitle('Dark', { exact: true }).click()
    expect(await dark(page)).toBe(true)
    // The drawer's own switch shows it checked; the other group follows the store.
    await expect(group.getByRole('radio', { name: 'Dark' })).toBeChecked()
    await expect(drawer).toBeVisible()
    // The drawer itself is dark, not a light panel over a dark page.
    const bg = await drawer.evaluate((el) => getComputedStyle(el).backgroundColor)
    const [r, g, b] = bg.match(/\d+/g)!.map(Number)
    expect(r + g + b, `the drawer is ${bg}`).toBeLessThan(200)
  })
})

test.describe('a touch screen with the desktop sidebar', () => {
  test.use({ viewport: { width: 1024, height: 768 }, hasTouch: true, isMobile: true })

  test('keeps "Sign out" on one line beside or above the switch, inside the sidebar', async ({ page }) => {
    await signedIn(page, 'light')
    await page.goto('/dashboard')
    expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches), 'this device is not coarse: the case is not exercised').toBe(true)
    const aside = page.locator('aside').first()
    const signOut = aside.getByRole('button', { name: 'Sign out' })
    const group = aside.getByRole('group', { name: 'Appearance' })
    await expect(group).toBeVisible()

    const a = (await aside.boundingBox())!
    const s = (await signOut.boundingBox())!
    const g = (await group.boundingBox())!
    expect(g.x + g.width, 'the switch runs past the sidebar').toBeLessThanOrEqual(a.x + a.width + 0.5)
    // One line: the label does not overflow its button, and the button is as tall as set.
    expect(await signOut.evaluate((el) => el.scrollWidth <= el.clientWidth + 1 && el.scrollHeight <= el.clientHeight + 1)).toBe(true)
    expect(s.height).toBeLessThan(40)
    // Side by side, or the switch under it; never over it.
    const beside = s.x + s.width <= g.x + 0.5
    const below = g.y >= s.y + s.height - 0.5
    expect(beside || below, 'Sign out and the switch overlap').toBe(true)
    // Targets are at least 44 px tall.
    expect(g.height).toBeGreaterThanOrEqual(44)
  })
})

test.describe('a status colour an administrator chose', () => {
  test.use({ viewport: { width: 1280, height: 900 } })
  const NAVY = '#1e3a8a'
  const navy = STATUSES.map((s) => (s.id === 'st-new' ? { ...s, color: NAVY } : s))

  const luminance = ([r, g, b]: number[]) =>
    [r, g, b].map((v) => v / 255).map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)).reduce((acc, c, i) => acc + c * [0.2126, 0.7152, 0.0722][i], 0)
  const ratio = (a: number[], b: number[]) => {
    const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
    return (hi + 0.05) / (lo + 0.05)
  }
  const rgb = (css: string) => css.match(/[\d.]+/g)!.slice(0, 3).map(Number)

  async function chip(page: Page) {
    const el = page.locator('tbody tr span[style*="color"]', { hasText: 'New' }).first()
    await expect(el).toBeVisible()
    return {
      color: rgb(await el.evaluate((e) => getComputedStyle(e).color)),
      card: rgb(await page.locator('tbody tr').first().evaluate((e) => getComputedStyle(e.closest('table')!.parentElement!).backgroundColor === 'rgba(0, 0, 0, 0)' ? getComputedStyle(document.body).backgroundColor : getComputedStyle(e.closest('table')!.parentElement!).backgroundColor)),
    }
  }

  test('is shown as chosen in light', async ({ page }) => {
    await signedIn(page, 'light', ME_STAFF, navy)
    await page.goto('/tickets')
    const { color } = await chip(page)
    expect(color).toEqual([30, 58, 138])
  })

  test('is lightened in dark until it reads on the card, and its dot keeps the colour inside a ring', async ({ page }) => {
    await signedIn(page, 'dark', ME_STAFF, navy)
    await page.goto('/tickets')
    const { color } = await chip(page)
    expect(color).not.toEqual([30, 58, 138])
    // The card in dark is gray-900 (#101828).
    expect(ratio(color, [16, 24, 40])).toBeGreaterThanOrEqual(4.5)

    const dot = page.locator('tbody tr span[aria-hidden="true"][style*="background-color"]').first()
    expect(rgb(await dot.evaluate((e) => getComputedStyle(e).backgroundColor))).toEqual([30, 58, 138])
    expect(await dot.evaluate((e) => getComputedStyle(e).boxShadow)).not.toBe('none')
  })
})

test.describe('Your account, Appearance', () => {
  test.use({ viewport: { width: 1280, height: 900 } })

  test('puts the previous theme back and says so when the save fails', async ({ page }) => {
    await signedIn(page, 'light')
    await page.route('**/api/v1/me/appearance', (route) => route.fulfill({ status: 500, json: { error: { code: 'internal', message: 'boom' } } }))
    await page.goto('/account')
    const main = page.getByRole('main')
    await main.getByText('Dark', { exact: true }).click()
    await expect(main.getByText("Couldn't save your appearance. Try again.")).toBeVisible()
    expect(await dark(page)).toBe(false)
    expect(await page.evaluate(() => window.localStorage.getItem('ghd.theme'))).toBe('light')
    await expect(main.getByRole('radio', { name: 'Light' })).toBeChecked()
  })

  test('says "Saved." and keeps the choice after a good save', async ({ page }) => {
    const patches = await signedIn(page, 'light')
    await page.goto('/account')
    await page.getByRole('main').getByText('Match my device', { exact: true }).click()
    await expect(page.getByText('Saved.')).toBeVisible()
    expect(patches).toEqual([{ theme: 'device' }])
  })
})

// A cheap look at every main page: in dark, no card, panel or table sits white on the dark page.
test.describe('every main page in dark has no light card', () => {
  test.use({ viewport: { width: 1280, height: 900 } })
  const pages: { url: string; me: object; ready: string | RegExp }[] = [
    { url: '/dashboard', me: ME_STAFF, ready: 'Dashboard' },
    { url: '/tickets', me: ME_STAFF, ready: 'Assigned to Me' },
    { url: `/tickets/${TICKET_DETAIL.id}`, me: ME_STAFF, ready: TICKET_DETAIL.subject },
    { url: '/reports', me: ME_ADMIN, ready: 'Reports' },
    { url: '/account', me: ME_STAFF, ready: 'Appearance' },
  ]
  for (const p of pages) {
    test(p.url, async ({ page }) => {
      await signedIn(page, 'dark', p.me)
      await page.goto(p.url)
      await expect(page.getByRole('heading', { name: p.ready }).first()).toBeVisible()
      await page.waitForLoadState('networkidle')
      expect(await dark(page)).toBe(true)
      const light = await page.evaluate(() => {
        const out: string[] = []
        for (const el of document.querySelectorAll<HTMLElement>('main *, aside, aside *, header, header *')) {
          const r = el.getBoundingClientRect()
          if (r.width < 24 || r.height < 12) continue
          const m = getComputedStyle(el).backgroundColor.match(/[\d.]+/g)
          if (!m) continue
          const [rr, g, b, a = '1'] = m
          // An opaque, near-white fill larger than a chip.
          if (Number(a) > 0.9 && Number(rr) > 235 && Number(g) > 235 && Number(b) > 235 && r.width * r.height > 2500) {
            out.push(`${el.tagName.toLowerCase()}.${String(el.className).slice(0, 60)}`)
          }
        }
        return out
      })
      expect(light, 'a near-white box on a dark page').toEqual([])
    })
  }
})
