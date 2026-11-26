import { test, expect, type Page } from '@playwright/test'
import { mockApi, mockCti, ME_ADMIN, ME_REPORTER, CTI_DESCRIPTIONS } from './mock'

// #711: a requester's Item selector (Show Items to registered users) and the guest
// form's Type and Item selectors (Show Types to guests and Show Items to guests), at 320
// CSS pixels, plus the two switches on their settings pages. Every call is
// mocked; nothing here needs the Go server.
test.use({ viewport: { width: 320, height: 640 } })

const region = (page: Page) => page.locator('#cti-description')

async function fitsTheViewport(page: Page) {
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

test.describe('new ticket page, as a requester', () => {
  test('has no Item selector until the setting is on', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await mockCti(page)
    await page.goto('/tickets/new')
    await expect(page.getByLabel('Type')).toBeVisible()
    await expect(page.getByLabel('Item')).toHaveCount(0)
  })

  test('offers the Item under Type once /me says so, and the cascade works', async ({ page }) => {
    await mockApi(page, { me: { ...ME_REPORTER, can_choose_item: true } })
    await mockCti(page)
    let sent: Record<string, unknown> | null = null
    await page.route('**/api/v1/tickets', async (route) => {
      if (route.request().method() !== 'POST') return route.fallback()
      sent = route.request().postDataJSON()
      return route.fulfill({ status: 201, json: { id: 'tk-new', tracking_number: 'GHD-2026-000099' } })
    })
    await page.goto('/tickets/new')
    await expect(page.getByLabel('Item')).toBeDisabled()

    await page.getByLabel('Category').selectOption('cat-hw')
    await page.getByLabel('Type').selectOption('ty-lap')
    await expect(page.getByLabel('Item')).toBeEnabled()
    await page.getByLabel('Item').selectOption('it-dock')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.dock)
    await page.getByLabel('Item').selectOption('it-bat') // no text of its own
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.laptop)

    // A new category clears everything beneath it.
    await page.getByLabel('Category').selectOption('cat-sw')
    await expect(page.getByLabel('Type')).toHaveValue('')
    await expect(page.getByLabel('Item')).toHaveValue('')
    await fitsTheViewport(page)

    await page.getByLabel('Category').selectOption('cat-hw')
    await page.getByLabel('Type').selectOption('ty-lap')
    await page.getByLabel('Item').selectOption('it-dock')
    await page.getByLabel('Subject').fill('Dock dead')
    await page.getByRole('button', { name: /^(Submit|Create)/ }).click()
    await expect.poll(() => sent).not.toBeNull()
    expect(sent).toMatchObject({ category_id: 'cat-hw', type_id: 'ty-lap', item_id: 'it-dock' })
  })
})

test.describe('guest form (/submit)', () => {
  async function mockGuest(page: Page, chooseTypeItem: boolean | undefined) {
    await page.route('**/api/v1/**', async (route) => {
      const path = new URL(route.request().url()).pathname
      if (path === '/api/v1/site') {
        return route.fulfill({ json: {
          name: 'Go Help Desk', logo_url: '', version: '1.3.0', guest_submission_enabled: true,
          guests_show_types: chooseTypeItem, guests_show_items: chooseTypeItem,
        } })
      }
      return route.fulfill({ json: [] })
    })
    await mockCti(page)
  }

  test('is category only while the setting is off', async ({ page }) => {
    await mockGuest(page, false)
    await page.goto('/submit')
    await page.getByLabel('Category').selectOption('cat-hw')
    await expect(region(page)).toContainText('Laptops, docks and monitors.')
    await expect(page.getByLabel('Type')).toHaveCount(0)
    await expect(page.getByLabel('Item')).toHaveCount(0)
  })

  test('shows Category, Type and Item while it is on, with the most specific description', async ({ page }) => {
    await mockGuest(page, true)
    let sent: Record<string, unknown> | null = null
    await page.route('**/api/v1/guest/tickets', async (route) => {
      sent = route.request().postDataJSON()
      return route.fulfill({ status: 201, json: { tracking_number: 'GHD-2026-000042' } })
    })
    await page.goto('/submit')
    await expect(page.getByLabel('Type')).toBeDisabled()
    await expect(page.getByLabel('Item')).toBeDisabled()

    await page.getByLabel('Category').selectOption('cat-hw')
    await page.getByLabel('Type').selectOption('ty-lap')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.laptop)
    await page.getByLabel('Item').selectOption('it-dock')
    await expect(region(page)).toHaveText(CTI_DESCRIPTIONS.dock)
    await insideTheViewport(page, '#cti-description p')
    await insideTheViewport(page, '#item')
    await fitsTheViewport(page)

    await page.getByLabel('Name *').fill('Visitor')
    await page.getByLabel('Email *').fill('v@example.com')
    await page.getByLabel('Subject *').fill('Dock dead')
    await page.getByRole('button', { name: 'Submit request' }).click()
    await expect(page.getByText('GHD-2026-000042')).toBeVisible()
    expect(sent).toMatchObject({ category_id: 'cat-hw', type_id: 'ty-lap', item_id: 'it-dock' })
  })
})

test.describe('admin settings', () => {
  async function adminPage(page: Page, settings: object) {
    await mockApi(page, { me: ME_ADMIN })
    await page.route('**/api/v1/admin/settings', (route) =>
      route.request().method() === 'GET' ? route.fulfill({ json: settings }) : route.fallback())
  }

  test('Show Items to registered users sits under General -> Tickets, off by default', async ({ page }) => {
    await adminPage(page, { post_upgrade_130_seen: true })
    await page.goto('/admin/settings/general')
    const toggle = page.getByRole('switch', { name: 'Show Items to registered users' })
    await expect(toggle).toBeVisible()
    await expect(toggle).toHaveAttribute('aria-checked', 'false')
    await fitsTheViewport(page)
  })

  test('Show Types to guests and Show Items to guests are under Guest Access and wait for guest submission', async ({ page }) => {
    await adminPage(page, { post_upgrade_130_seen: true, guest_submission_enabled: false })
    await page.goto('/admin/settings/guest-access')
    for (const name of ['Show Types to guests', 'Show Items to guests']) {
      const toggle = page.getByRole('switch', { name })
      await expect(toggle).toBeVisible()
      await expect(toggle).toBeDisabled()
    }
    await fitsTheViewport(page)
  })
})
