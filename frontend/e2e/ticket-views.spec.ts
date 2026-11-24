import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_ADMIN, ME_REPORTER, ME_STAFF } from './mock'

// #676: the Tickets views are in the navigation drawer below md, the same
// SidebarContent as the desktop sidebar. At 320 CSS pixels, the narrowest phone
// the app supports, the drawer lists the views each role may use, nothing
// scrolls sideways, and the My Group's Tickets toolbar wraps.
test.use({ viewport: { width: 320, height: 640 } })

async function noHorizontalScroll(page: Page, where: string) {
  // Measured on main's own box, as in mobile-layout.spec.ts: the shell's row is
  // overflow-hidden, so the document itself can never be wider than the viewport.
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, `${where} is wider than its own box at 320px`).toBeLessThanOrEqual(clientWidth)
}

test.describe('the Tickets views in the drawer at 320px', () => {
  const cases = [
    ['an administrator', ME_ADMIN, ['Assigned to Me', "My Group's Tickets", 'Available', 'Search']],
    // The setting is off in the mock, so Available is not listed for staff.
    ['staff', ME_STAFF, ['Assigned to Me', "My Group's Tickets", 'Search']],
  ] as const

  for (const [who, me, views] of cases) {
    test(`${who} sees the views under Tickets, and nothing scrolls sideways`, async ({ page }) => {
      await mockApi(page, { me })
      await page.goto('/dashboard')
      await page.getByRole('button', { name: 'Open navigation menu' }).click()

      const drawer = page.getByRole('dialog')
      await expect(drawer.getByRole('group', { name: 'Tickets' }).getByRole('link')).toHaveText([...views])

      const { scrollWidth, clientWidth } = await drawer.evaluate((el) => ({
        scrollWidth: el.scrollWidth,
        clientWidth: el.clientWidth,
      }))
      expect(scrollWidth, 'the drawer is wider than its own box at 320px').toBeLessThanOrEqual(clientWidth)
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320)
    })
  }

  test('a requester keeps the single Tickets link', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await page.goto('/dashboard')
    await page.getByRole('button', { name: 'Open navigation menu' }).click()

    const drawer = page.getByRole('dialog')
    await expect(drawer.getByRole('group', { name: 'Tickets' })).toHaveCount(0)
    await expect(drawer.getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()
  })
})

test("My Group's Tickets toolbar wraps at 320px without horizontal scroll", async ({ page }) => {
  await mockApi(page, { me: ME_STAFF })
  await page.goto('/tickets?view=groups')

  const toolbar = page.getByRole('group', { name: 'Who holds the ticket' })
  await expect(toolbar).toBeVisible()
  const buttons = toolbar.getByRole('button')
  await expect(buttons).toHaveCount(4)
  for (let i = 0; i < 4; i++) {
    const box = await buttons.nth(i).boundingBox()
    expect(box, `choice ${i + 1} is not laid out`).not.toBeNull()
    expect(box!.x, `choice ${i + 1} starts off the left edge`).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width, `choice ${i + 1} runs off the right edge`).toBeLessThanOrEqual(320)
  }
  await noHorizontalScroll(page, 'the ticket list')
})
