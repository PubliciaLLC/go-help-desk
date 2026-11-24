import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_REPORTER, TICKET_DETAIL } from './mock'

// #695: staff and administrators add other staff as followers and remove any
// follower, from the Followers panel on the ticket page. The server is mocked
// (layout and behaviour of the panel are what is checked here); the rules for
// who may be added are the server's, and are tested against Postgres.

const T = TICKET_DETAIL.id
const ALEX = { user_id: 'u-alex', display_name: 'Alex Admin', created_at: '2026-10-01T09:00:00Z' }
const BEA = { user_id: 'u-bea', display_name: 'Bea Staff', created_at: '2026-10-02T09:00:00Z' }
const STAFF = [
  { id: 'u-staff', display_name: 'Sam Staff', assignable: true },
  { id: 'u-alex', display_name: 'Alex Admin', assignable: true },
  { id: 'u-bea', display_name: 'Bea Staff', assignable: true },
  { id: 'u-cal', display_name: 'Cal Colleague', assignable: true },
]

// A followers list the test controls, with the two writes the panel makes.
async function mockFollowers(page: Page, opts: { refuse?: { status: number; code: string; message: string } } = {}) {
  let followers = [ALEX]
  const writes: string[] = []
  await page.route('**/api/v1/staff', (route) => route.fulfill({ json: STAFF }))
  await page.route(`**/api/v1/tickets/${T}/followers`, (route) => route.fulfill({ json: followers }))
  await page.route(`**/api/v1/tickets/${T}/followers/*`, async (route) => {
    const req = route.request()
    const id = new URL(req.url()).pathname.split('/').pop()!
    writes.push(`${req.method()} ${id}`)
    if (opts.refuse && req.method() === 'PUT') {
      await route.fulfill({
        status: opts.refuse.status,
        json: { error: { code: opts.refuse.code, message: opts.refuse.message } },
      })
      return
    }
    if (req.method() === 'PUT') {
      const person = [BEA, ALEX].find((p) => p.user_id === id)
      if (person && !followers.some((f) => f.user_id === id)) followers = [...followers, person]
    } else {
      followers = followers.filter((f) => f.user_id !== id)
    }
    await route.fulfill({ status: 204, body: '' })
  })
  return writes
}

async function noHorizontalScroll(page: Page, width: number) {
  const m = await page.evaluate(() => {
    const main = document.querySelector('main')!
    return { sw: main.scrollWidth, cw: main.clientWidth, dw: document.documentElement.scrollWidth }
  })
  expect(m.sw, `main is wider than its own box at ${width}px`).toBeLessThanOrEqual(m.cw)
  expect(m.dw, `the document is wider than ${width}px`).toBeLessThanOrEqual(width)
}

for (const width of [320, 1280]) {
  test.describe(`followers panel at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 } })

    test('staff add a follower and remove one', async ({ page }) => {
      await mockApi(page)
      const writes = await mockFollowers(page)
      await page.goto(`/tickets/${T}`)

      const picker = page.getByRole('combobox', { name: 'Add follower' })
      await expect(picker).toBeVisible()
      await expect(page.getByRole('button', { name: 'Remove Alex Admin as a follower' })).toBeVisible()
      // Offered: people not already following, and not me.
      await expect(picker.locator('option')).toHaveText(['Select a person…', 'Bea Staff', 'Cal Colleague'])

      await picker.selectOption('u-bea')
      await page.getByRole('button', { name: 'Add', exact: true }).click()
      await expect(page.getByRole('button', { name: 'Remove Bea Staff as a follower' })).toBeVisible()
      await expect(picker.locator('option')).toHaveText(['Select a person…', 'Cal Colleague'])
      expect(writes).toEqual(['PUT u-bea'])

      await page.getByRole('button', { name: 'Remove Alex Admin as a follower' }).click()
      await expect(page.getByRole('button', { name: 'Remove Alex Admin as a follower' })).toHaveCount(0)
      expect(writes).toEqual(['PUT u-bea', 'DELETE u-alex'])

      await noHorizontalScroll(page, width)
    })

    test('a refusal is shown, and nothing changes', async ({ page }) => {
      await mockApi(page)
      await mockFollowers(page, {
        refuse: { status: 422, code: 'follower_cannot_view_ticket', message: 'that person cannot see this ticket' },
      })
      await page.goto(`/tickets/${T}`)

      await page.getByRole('combobox', { name: 'Add follower' }).selectOption('u-cal')
      await page.getByRole('button', { name: 'Add', exact: true }).click()

      await expect(page.getByRole('alert').filter({ hasText: 'cannot see this ticket' })).toHaveText(
        'Cal Colleague cannot see this ticket, so they cannot follow it.',
      )
      await expect(page.getByRole('button', { name: 'Remove Cal Colleague as a follower' })).toHaveCount(0)
      await noHorizontalScroll(page, width)
    })

    test('the controls are labelled, reachable by keyboard and at least 24px', async ({ page }) => {
      await mockApi(page)
      await mockFollowers(page)
      await page.goto(`/tickets/${T}`)

      const picker = page.getByRole('combobox', { name: 'Add follower' })
      const remove = page.getByRole('button', { name: 'Remove Alex Admin as a follower' })
      const add = page.getByRole('button', { name: 'Add', exact: true })
      for (const c of [picker, remove, add]) {
        await c.scrollIntoViewIfNeeded()
        const box = (await c.boundingBox())!
        expect(box.width, await c.innerHTML()).toBeGreaterThanOrEqual(24)
        expect(box.height, await c.innerHTML()).toBeGreaterThanOrEqual(24)
      }
      await remove.focus()
      await expect(remove).toBeFocused()
    })
  })
}

test('a requester never sees the panel', async ({ page }) => {
  await mockApi(page, { me: ME_REPORTER })
  await mockFollowers(page)
  const asked: string[] = []
  page.on('request', (r) => asked.push(new URL(r.url()).pathname))
  await page.goto(`/tickets/${T}`)

  await expect(page.getByText(TICKET_DETAIL.subject)).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Add follower' })).toHaveCount(0)
  await expect(page.getByText('Followers', { exact: true })).toHaveCount(0)
  expect(asked.filter((p) => p.includes('/followers'))).toEqual([])
})
