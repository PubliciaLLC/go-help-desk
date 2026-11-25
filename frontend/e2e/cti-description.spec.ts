import { test, expect, type Page } from '@playwright/test'
import { mockApi, mockAdminApi, mockCti, ME_REPORTER, CTI_DESCRIPTIONS } from './mock'

// The description of the chosen Category / Type / Item, beneath the chooser, at
// 320 CSS pixels (the narrowest viewport DESIGN.md's Small screens section covers).
// It is one text, the most specific one, that follows the selectors, is plain
// text, wraps, and takes no room while there is nothing to say.
test.use({ viewport: { width: 320, height: 640 } })

// The element the selectors point at. Its text is what a person reads.
const region = (page: Page) => page.locator('#cti-description')

async function fitsTheViewport(page: Page) {
  // The page's own scroller: <main> in the shell, the document on /submit.
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const el = document.querySelector('main') ?? document.documentElement
    return { scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }
  })
  expect(scrollWidth, 'the page scrolls sideways').toBeLessThanOrEqual(clientWidth)
}

async function insideTheViewport(page: Page, selector: string) {
  const box = await page.locator(selector).boundingBox()
  expect(box, selector).not.toBeNull()
  expect(box!.x).toBeGreaterThanOrEqual(0)
  expect(box!.x + box!.width, `${selector} runs past the right edge`).toBeLessThanOrEqual(320)
}

test.describe('new ticket page', () => {
  test('follows the selectors, falls back, and clears with the category (staff)', async ({ page }) => {
    await mockApi(page)
    await mockCti(page)
    await page.goto('/tickets/new')
    await expect(page.getByRole('heading', { level: 1, name: 'New Ticket' })).toBeVisible()

    // Before a category: nothing, and no box.
    await expect(region(page)).toHaveText('')
    await expect(region(page).locator('p')).toHaveCount(0)

    // Category -> its text, line break kept, the long token wrapped inside the box.
    await page.getByLabel('Category').selectOption('cat-hw')
    await expect(region(page)).toContainText('Laptops, docks and monitors.')
    await expect(region(page)).toContainText('For phones see Mobile.')
    await insideTheViewport(page, '#cti-description p')
    await fitsTheViewport(page)
    const lines = await region(page).locator('p').evaluate((p) => {
      const range = document.createRange()
      range.selectNodeContents(p)
      return new Set([...range.getClientRects()].map((r) => Math.round(r.top))).size
    })
    expect(lines, 'the newline in the text is a line break, and a long text wraps').toBeGreaterThan(2)

    // Type -> the type's.
    await page.getByLabel('Type').selectOption('ty-lap')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.laptop)

    // Item with its own -> the item's. Item with none -> the type's, still.
    await page.getByLabel('Item').selectOption('it-dock')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.dock)
    await page.getByLabel('Item').selectOption('it-bat')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.laptop)

    // Another category: the old text is gone and, having none, nothing replaces it.
    await page.getByLabel('Category').selectOption('cat-sw')
    await expect(region(page)).toHaveText('')
    await expect(region(page).locator('p')).toHaveCount(0)
    await fitsTheViewport(page)
  })

  test('an empty description leaves no gap: the form below does not move', async ({ page }) => {
    await mockApi(page)
    await mockCti(page)
    await page.goto('/tickets/new')
    await expect(page.getByLabel('Category')).toBeVisible()
    // The description editor above loads late and changes the page's height; measure
    // after it has, or the first reading is of a different layout.
    await expect(page.getByRole('toolbar', { name: 'Formatting' })).toBeVisible()
    const priorityTop = async () => (await page.getByLabel('Priority').boundingBox())!.y

    const before = await priorityTop()
    await page.getByLabel('Category').selectOption('cat-sw') // no description
    await expect(page.getByLabel('Type')).toBeEnabled()
    expect(await priorityTop()).toBe(before)

    await page.getByLabel('Category').selectOption('cat-hw') // a description: it takes room
    await expect(region(page).locator('p')).toBeVisible()
    expect(await priorityTop()).toBeGreaterThan(before)
  })

  test('markup is shown as text, not rendered', async ({ page }) => {
    await mockApi(page)
    await mockCti(page)
    await page.goto('/tickets/new')
    await page.getByLabel('Category').selectOption('cat-mk')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.markup)
    await expect(region(page).locator('b, i')).toHaveCount(0)
  })

  test('is wired to the selectors and announced politely, with no hover tooltip', async ({ page }) => {
    await mockApi(page)
    await mockCti(page)
    await page.goto('/tickets/new')
    await expect(region(page)).toHaveAttribute('aria-live', 'polite')
    for (const name of ['Category', 'Type', 'Item']) {
      await expect(page.getByLabel(name)).toHaveAttribute('aria-describedby', 'cti-description')
    }
    await page.getByLabel('Category').selectOption('cat-hw')
    await expect(region(page).locator('[title]')).toHaveCount(0)
  })

  test('a requester sees the category and type text; there is no item', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await mockCti(page)
    await page.goto('/tickets/new')
    await expect(page.getByLabel('Item')).toHaveCount(0)
    await page.getByLabel('Category').selectOption('cat-hw')
    await expect(region(page)).toContainText('Laptops, docks and monitors.')
    await page.getByLabel('Type').selectOption('ty-lap')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.laptop)
    await fitsTheViewport(page)
  })
})

test.describe('guest form (/submit)', () => {
  test('shows the category text, and nothing for a category with none', async ({ page }) => {
    await page.route('**/api/v1/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/site') {
        return route.fulfill({ json: { name: 'Go Help Desk', logo_url: '', version: '1.3.0', guest_submission_enabled: true } })
      }
      return route.fulfill({ json: [] })
    })
    await mockCti(page)
    await page.goto('/submit')
    await expect(page.getByRole('heading', { level: 1, name: 'Submit a request' })).toBeVisible()
    await expect(region(page)).toHaveText('')

    await page.getByLabel('Category').selectOption('cat-hw')
    await expect(region(page)).toContainText('Laptops, docks and monitors.')
    await expect(page.getByLabel('Category')).toHaveAttribute('aria-describedby', 'cti-description')
    await insideTheViewport(page, '#cti-description p')
    await fitsTheViewport(page)

    await page.getByLabel('Category').selectOption('cat-sw')
    await expect(region(page)).toHaveText('')
    await expect(region(page).locator('p')).toHaveCount(0)

    await page.getByLabel('Category').selectOption('cat-mk')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.markup)
    await expect(region(page).locator('b, i')).toHaveCount(0)
  })
})

test.describe('admin: the Description field', () => {
  test('fits at 320px on a category and on the add form', async ({ page }) => {
    await mockAdminApi(page, { short: true })
    await page.goto('/admin/categories')
    await expect(page.getByRole('heading', { level: 1, name: 'Categories' })).toBeVisible()

    // A category's own editor.
    await page.getByRole('button', { name: 'Expand' }).first().click()
    await expect(page.getByLabel(/^Description/)).toHaveCount(1)
    await page.getByLabel(/^Description/).fill('x'.repeat(520))
    await expect(page.getByText('520 / 500')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Save description' })).toBeDisabled()
    await fitsTheViewport(page)

    // And the new-category form.
    await page.getByRole('button', { name: 'New Category' }).click()
    await expect(page.getByLabel(/^Description/)).toHaveCount(2)
    await fitsTheViewport(page)
    for (const box of await page.getByLabel(/^Description/).all()) {
      const b = (await box.boundingBox())!
      expect(b.x + b.width, 'the Description box runs past the right edge').toBeLessThanOrEqual(320)
    }
  })
})
