import { test, expect, type Page } from '@playwright/test'
import { mockAdminApi, LONG_NAME } from './mock'

// #446: archive and restore buttons on the admin Categories tree. Layout is
// the point (the buttons add width to every row at phone sizes), plus one
// round trip so a button that renders but does nothing fails here.

type Node = { id: string; name: string; sort_order: number; active: boolean; [k: string]: unknown }

// A stateful tree: PATCH flips `active` on the matching row, so a re-fetch
// after the mutation shows the new state like the real API would.
async function mockTree(page: Page, activeFlags: { cat: boolean; type: boolean; item: boolean }) {
  await mockAdminApi(page)
  const cat: Node = { id: 'cat-1', name: LONG_NAME, sort_order: 1, active: activeFlags.cat }
  const type: Node = { id: 'ty-1', category_id: 'cat-1', name: LONG_NAME, sort_order: 1, active: activeFlags.type }
  const item: Node = { id: 'it-1', type_id: 'ty-1', name: LONG_NAME, sort_order: 1, active: activeFlags.item }
  const patches: string[] = []
  const serve = (node: Node) => async (route: import('@playwright/test').Route) => {
    const req = route.request()
    if (req.method() === 'PATCH') {
      const body = req.postDataJSON() as { active?: boolean }
      if (typeof body.active === 'boolean') node.active = body.active
      patches.push(`${new URL(req.url()).pathname} active=${body.active}`)
      return route.fulfill({ json: node })
    }
    if (req.method() === 'GET') return route.fallback()
    return route.fulfill({ status: 204 })
  }
  // Each handler answers GET from the node itself (the mock's fixed fixture
  // would never reflect a PATCH).
  const get = (node: Node, list: boolean) => async (route: import('@playwright/test').Route) =>
    route.request().method() === 'GET' ? route.fulfill({ json: list ? [node] : node }) : serve(node)(route)
  await page.route('**/api/v1/admin/categories', get(cat, true))
  await page.route('**/api/v1/admin/categories/cat-1', serve(cat))
  await page.route('**/api/v1/admin/categories/cat-1/types', get(type, true))
  await page.route('**/api/v1/admin/categories/cat-1/types/ty-1', serve(type))
  await page.route('**/api/v1/admin/categories/cat-1/types/ty-1/items', get(item, true))
  await page.route('**/api/v1/admin/categories/cat-1/types/ty-1/items/it-1', serve(item))
  return patches
}

// Opens category, then type, waiting for each level's row to render so the
// next Expand button exists before it is clicked.
async function expandAll(page: Page, rows: number) {
  for (let level = 1; level < rows; level++) {
    await page.getByRole('button', { name: 'Expand' }).first().click()
    await expect(page.getByTitle(/^(Archive|Restore) /)).toHaveCount(level + 1)
  }
}

for (const width of [320, 375]) {
  test.describe(`touch ${width}px`, () => {
  test.use({ hasTouch: true })
  test(`archived rows with long names fit: ${width}px`, async ({ page }) => {
    await mockTree(page, { cat: false, type: false, item: false })
    await page.setViewportSize({ width, height: 844 })
    await page.goto('/admin/categories')
    await expect(page.getByRole('heading', { level: 1, name: 'Categories' })).toBeVisible()
    await expandAll(page, 3)
    await expect(page.getByText('Archived')).toHaveCount(3)
    await expect(page.getByRole('button', { name: /^Restore/ })).toHaveCount(3)
    // Every row action (Restore, Delete, Rename) is on screen, not clipped past
    // the right edge, and at least 44px square to tap.
    const actions = page.getByRole('button', { name: /^(Restore|Delete|Rename)/ })
    await expect(actions).toHaveCount(9)
    for (const b of await actions.all()) {
      const box = await b.boundingBox()
      const label = await b.getAttribute('title')
      expect(box, `${label} has a box`).not.toBeNull()
      expect(box!.x + box!.width, `${label} inside viewport`).toBeLessThanOrEqual(width)
      expect(box!.height, `${label} height`).toBeGreaterThanOrEqual(44)
      expect(box!.width, `${label} width`).toBeGreaterThanOrEqual(44)
    }
    const doc = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(doc).toBeLessThanOrEqual(width)
  })
  })
}

test('archive asks first, then restore brings the row back', async ({ page }) => {
  const patches = await mockTree(page, { cat: true, type: true, item: true })
  await page.goto('/admin/categories')
  await expect(page.getByRole('heading', { level: 1, name: 'Categories' })).toBeVisible()
  await page.getByRole('button', { name: 'Archive category' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('Tickets already filed keep it')
  expect(patches).toEqual([])
  await dialog.getByRole('button', { name: 'Archive category' }).click()
  await expect(page.getByText('Archived')).toHaveCount(1)
  expect(patches).toEqual(['/api/v1/admin/categories/cat-1 active=false'])
  await page.getByRole('button', { name: 'Restore category' }).click()
  await expect(page.getByText('Archived')).toHaveCount(0)
  expect(patches).toEqual([
    '/api/v1/admin/categories/cat-1 active=false',
    '/api/v1/admin/categories/cat-1 active=true',
  ])
})
