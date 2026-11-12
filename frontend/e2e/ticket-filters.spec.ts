import { test, expect, type Page } from '@playwright/test'
import { mkdirSync } from 'fs'
import { mockApi, ME_ADMIN, ME_REPORTER, ME_STAFF, STATUSES, TICKETS } from './mock'

// The ticket list's filters, sort and reply matches in a real browser: the
// layout (vitest has no CSS engine), the full-screen dialog below md, and the
// request that goes out. Every backend call is mocked.
//
// SEARCH_SHOTS=<dir> also writes the screenshots the pull request shows.

const shots = process.env.SEARCH_SHOTS
async function shot(page: Page, name: string, fit = true) {
  if (!shots) return
  mkdirSync(shots, { recursive: true })
  // The page scrolls inside <main>, and a click leaves it wherever it scrolled to.
  await page.evaluate(() => document.querySelector('main')?.scrollTo(0, 0))
  // The shell is exactly one screen tall, so a full-page shot is a taller screen.
  const size = page.viewportSize()!
  const need = await page.evaluate(() => {
    const main = document.querySelector('main')!
    return window.innerHeight - main.clientHeight + main.scrollHeight
  })
  if (fit && need > size.height) await page.setViewportSize({ width: size.width, height: need })
  await page.screenshot({ path: `${shots}/${name}.png` })
  await page.setViewportSize(size)
}

// The filters in the address are ids, and the page keeps only UUID-shaped ones,
// so these fixtures are too.
const uid = (n: string) => `00000000-0000-4000-8000-0000000000${n}`
const ST_NEW = uid('a1')
const ST_CLOSED = uid('a2')
const ST_PROG = uid('a3')
const ST_PEND = uid('a4')
const ST_RESOLVED = uid('a5')
const CAT_HW = uid('c1')
const CAT_SW = uid('c2')
const TY_PRINTERS = uid('d1')

// What GET /me sends staff when SLA tracking is on; the page shows its SLA
// column and sort only then.
const STAFF = { ...ME_STAFF, sla_enabled: true }
const ADMIN = { ...ME_ADMIN, sla_enabled: true }

const DANA = { id: uid('b1'), display_name: 'Dana Whitfield', email: 'dana.whitfield@acme.example' }
const START = '\u0002'
const STOP = '\u0003'

const SLA = {
  policy_id: 'p',
  policy_name: 'Standard',
  response: { color: 'amber', target_min: 60, elapsed_min: 50, remaining_min: 10, met_at: null },
  resolution: { color: 'green', target_min: 480, elapsed_min: 50, remaining_min: 430, met_at: null },
}

const LIST = [
  { ...TICKETS[0], status_id: ST_NEW, category_id: CAT_HW, subject: 'Printer on the 3rd floor jams on tray 2', priority: 'high', sla: SLA, updated_at: '2026-10-28T10:00:00Z' },
  { ...TICKETS[1], status_id: ST_NEW, category_id: CAT_SW, subject: 'Toner warning stays on after replacement', priority: 'high', sla: SLA, updated_at: '2026-10-27T10:00:00Z' },
  { ...TICKETS[2], status_id: ST_NEW, category_id: CAT_SW, subject: 'Label printer prints blank lines between labels', priority: 'medium', sla: null, updated_at: '2026-10-26T10:00:00Z' },
]

const MATCHES = [
  {
    ...LIST[0],
    reply_match: {
      reply_id: 'r-1',
      internal: false,
      created_at: '2026-11-03T14:02:00Z',
      snippet: `…paper is creasing at the ${START}fuser${STOP}, so I have ordered a new ${START}fuser${STOP} kit and will fit it Thursday…`,
    },
  },
  {
    ...LIST[1],
    reply_match: {
      reply_id: 'r-2',
      internal: true,
      created_at: '2026-11-04T09:30:00Z',
      snippet: `…vendor says the counter resets with the ${START}fuser${STOP}, not the toner; check the service menu before…`,
    },
  },
]

type Seen = URLSearchParams[]

// Registered after mockApi, so it wins for these paths. What answers depends on
// the request: text with search_replies gets the reply matches, so the test
// sees what the page does with them.
async function mockSearch(page: Page, opts: { headers?: Record<string, string> } = {}): Promise<Seen> {
  const seen: Seen = []
  await page.route(/\/api\/v1\/tickets(\?|$)/, async route => {
    const url = new URL(route.request().url())
    if (url.pathname !== '/api/v1/tickets') return route.fallback()
    seen.push(url.searchParams)
    const withReplies = url.searchParams.has('q') && url.searchParams.get('search_replies') === 'true'
    await route.fulfill({ json: withReplies ? MATCHES : LIST, headers: opts.headers })
  })
  await page.route('**/api/v1/users/lookup**', async route => {
    const url = new URL(route.request().url())
    await route.fulfill({ json: url.searchParams.get('id') ? DANA : [DANA] })
  })
  await page.route('**/api/v1/categories', route =>
    route.fulfill({ json: [{ id: CAT_HW, name: 'Hardware', active: true }, { id: CAT_SW, name: 'Software', active: true }] }),
  )
  await page.route(`**/api/v1/categories/${CAT_HW}/types`, route =>
    route.fulfill({ json: [{ id: TY_PRINTERS, category_id: CAT_HW, name: 'Printers', active: true }] }),
  )
  await page.route('**/api/v1/statuses', route =>
    route.fulfill({
      json: [
        { ...STATUSES[0], id: ST_NEW },
        { id: ST_PROG, name: 'In Progress', kind: 'custom', sort_order: 2, color: '#f59e0b', active: true, ticket_count: 1 },
        { id: ST_PEND, name: 'Pending', kind: 'custom', sort_order: 3, color: '#8b5cf6', active: true, ticket_count: 1 },
        { ...STATUSES[1], id: ST_RESOLVED },
        { ...STATUSES[2], id: ST_CLOSED },
      ],
    }),
  )
  return seen
}

async function noHorizontalScroll(page: Page) {
  // main, not the document: the shell clips everything else (see mobile-layout.spec.ts).
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')!
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, 'main is wider than its own box').toBeLessThanOrEqual(clientWidth)
}

test.describe('desktop, 1440x900', () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test('panel, chips, sort by a column, and the address holds all of it', async ({ page }) => {
    await mockApi(page, { me: ADMIN })
    const seen = await mockSearch(page)
    await page.goto(`/tickets?status=${ST_NEW}&reporter=${DANA.id}`)

    // The two old links still work, and the chip names the account by id.
    const chips = page.getByRole('group', { name: 'Active filters' })
    await expect(chips.getByText('Dana Whitfield')).toBeVisible()
    await expect(chips).toContainText('Status: New')

    await page.getByRole('button', { name: /^Filters/ }).click()
    const panel = page.getByRole('region', { name: 'Filters' })
    await panel.getByLabel('In Progress').check()
    await panel.getByLabel('Pending').check()
    await panel.getByLabel('High').check()
    await panel.getByLabel('Category').selectOption(CAT_HW)
    await panel.getByLabel('Type').selectOption(TY_PRINTERS)
    await panel.getByLabel('Created from').fill('2026-10-01')
    await panel.getByLabel('Created to').fill('2026-10-31')
    await expect(chips.getByText('Hardware › Printers')).toBeVisible()
    await expect(chips).toContainText('Priority: High')

    await page.getByRole('button', { name: /^Subject/ }).click()
    await page.getByRole('button', { name: /^Subject/ }).click()
    await expect(page.getByRole('columnheader', { name: /Subject/ })).toHaveAttribute('aria-sort', 'descending')

    await expect.poll(() => seen.at(-1)?.get('sort')).toBe('subject')
    const last = seen.at(-1)!
    expect(last.get('order')).toBe('desc')
    expect(last.getAll('status_id').sort()).toEqual([ST_NEW, ST_PEND, ST_PROG].sort())
    expect(last.getAll('priority')).toEqual(['high'])
    expect(last.get('reporter_id')).toBe(DANA.id)
    expect(last.get('category_id')).toBe(CAT_HW)
    expect(last.get('type_id')).toBe(TY_PRINTERS)
    expect(last.has('created_after')).toBe(true)
    expect(last.has('scope')).toBe(false) // an account names a set of tickets: no queue

    const q = new URL(page.url()).searchParams
    expect(q.get('sort')).toBe('subject')
    expect(q.get('order')).toBe('desc')

    await shot(page, 'desktop-panel-open-sorted-chips')

    // Reload: the same search comes back from the address alone.
    await page.reload()
    await expect(page.getByRole('group', { name: 'Active filters' }).getByText('Dana Whitfield')).toBeVisible()
    await expect(page.getByRole('columnheader', { name: /Subject/ })).toHaveAttribute('aria-sort', 'descending')

    // Clear all leaves a plain address.
    await page.getByRole('button', { name: 'Clear all' }).first().click()
    await expect(page.getByRole('group', { name: 'Active filters' })).toHaveCount(0)
  })

  // Every choice in the Sort by menu has a header to click on a wide screen.
  test('an Updated column: shown, dated, and it sorts both ways', async ({ page }) => {
    await mockApi(page, { me: STAFF })
    const seen = await mockSearch(page)
    await page.goto('/tickets')

    const header = page.getByRole('columnheader', { name: /Updated/ })
    await expect(header).toBeVisible()
    await expect(page.getByRole('columnheader', { name: /Created/ })).toBeVisible()
    // Dates in the same short form as Created: LIST[0] was updated on 28 Oct.
    await expect(page.getByRole('cell', { name: /Oct 28|28 Oct/ }).first()).toBeVisible()

    await header.getByRole('button').click()
    await expect(header).toHaveAttribute('aria-sort', 'descending')
    await expect.poll(() => seen.at(-1)?.get('sort')).toBe('updated')
    expect(seen.at(-1)!.get('order')).toBe('desc')

    await header.getByRole('button').click()
    await expect(header).toHaveAttribute('aria-sort', 'ascending')
    await expect.poll(() => seen.at(-1)?.get('order')).toBe('asc')
    await noHorizontalScroll(page)
  })

  test('a requester has no Updated column', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await mockSearch(page)
    await page.goto('/tickets')
    await expect(page.getByRole('columnheader', { name: /Created/ })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: /Updated/ })).toHaveCount(0)
  })

  test('a reply match: highlighted words as text, the note marked, no author', async ({ page }) => {
    await mockApi(page, { me: STAFF })
    const seen = await mockSearch(page)
    await page.goto('/tickets')
    await page.getByPlaceholder(/Search by tracking number/).fill('fuser')

    await expect(page.getByText('Sorted by best match.')).toBeVisible()
    await expect.poll(() => seen.at(-1)?.get('search_replies')).toBe('true')
    expect(seen.at(-1)!.get('sort')).toBeNull()

    // The table and the cards both render the match; only one is on screen.
    const marks = page.locator('mark:visible')
    await expect(marks).toHaveCount(3)
    await expect(marks.first()).toHaveText('fuser')
    await expect(page.getByText('Internal note', { exact: false }).first()).toBeVisible()
    await shot(page, 'desktop-reply-match')
  })

  test('replies that could not be searched say so', async ({ page }) => {
    await mockApi(page, { me: STAFF })
    await mockSearch(page, { headers: { 'x-replies-searched': 'false' } })
    await page.goto('/tickets?q=fuser')
    await expect(page.getByText(/Replies weren.t fully searched/)).toBeVisible()
  })

  test('a reply index still being built says so', async ({ page }) => {
    await mockApi(page, { me: STAFF })
    await mockSearch(page, { headers: { 'x-reply-index': 'building' } })
    await page.goto('/tickets?q=fuser')
    await expect(page.getByText(/Reply search is still being prepared/)).toBeVisible()
  })

  test('a requester sees no staff filters and no Updated column', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await mockSearch(page)
    await page.goto('/tickets')
    await expect(page.getByRole('button', { name: /^Subject/ })).toBeVisible()
    await expect(page.getByRole('button', { name: /^Updated/ })).toHaveCount(0)
    await page.getByRole('button', { name: /^Filters/ }).click()
    const panel = page.getByRole('region', { name: 'Filters' })
    await expect(panel.getByLabel('Created from')).toBeVisible()
    await expect(panel.getByLabel('Assignee')).toHaveCount(0)
    await expect(panel.getByLabel('Requester')).toHaveCount(0)
  })
})

// The narrowest width that shows the Created and Updated columns.
test.describe('desktop, 1280x800', () => {
  test.use({ viewport: { width: 1280, height: 800 } })

  test('the Created and Updated columns fit without sideways scroll', async ({ page }) => {
    await mockApi(page, { me: ADMIN })
    await mockSearch(page)
    await page.goto('/tickets')
    await expect(page.getByRole('columnheader', { name: /Updated/ })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: /Created/ })).toBeVisible()
    await noHorizontalScroll(page)
  })
})

// Touch, so the (pointer: coarse) rules that make a tap target 44 px apply.
test.describe('phone, 390x844', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  async function openDialog(page: Page) {
    await mockApi(page, { me: ADMIN })
    const seen = await mockSearch(page)
    await page.goto(`/tickets?status=${ST_NEW}&priority=high&reporter=${DANA.id}&created_from=2026-10-01&created_to=2026-10-31&category=${CAT_HW}&type=${TY_PRINTERS}`)
    await page.getByRole('button', { name: /^Filters/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Filters' })
    await expect(dialog).toBeVisible()
    return { dialog, seen }
  }

  test('the filters are a full-screen dialog applied with Show results', async ({ page }) => {
    const { dialog, seen } = await openDialog(page)
    const before = seen.length

    const box = await dialog.boundingBox()
    expect(box!.width).toBeGreaterThanOrEqual(389)
    expect(box!.height).toBeGreaterThanOrEqual(843)

    await dialog.getByLabel('Critical').check()
    // Nothing is asked until Show results.
    await page.waitForTimeout(300)
    expect(seen.length).toBe(before)

    await shot(page, 'phone-filter-dialog', false)

    await dialog.getByRole('button', { name: 'Show results' }).click()
    await expect(dialog).toHaveCount(0)
    await expect.poll(() => seen.at(-1)?.getAll('priority').sort().join()).toBe('critical,high')
  })

  test('every control in the dialog is at least 44 px tall', async ({ page }) => {
    const { dialog } = await openDialog(page)
    await dialog.getByRole('button', { name: /^More filters/ }).click()

    const small = await dialog.evaluate(el => {
      const out: string[] = []
      for (const c of el.querySelectorAll('button, select, input:not([type="checkbox"]), label')) {
        const r = (c as HTMLElement).getBoundingClientRect()
        if (r.width === 0 || r.height === 0) continue
        // A checkbox is tapped through its label, which is the box measured.
        if (c.tagName === 'LABEL' && !c.querySelector('input[type="checkbox"]')) continue
        if (r.height < 43.5) out.push(`${c.tagName} "${(c.textContent || c.getAttribute('aria-label') || '').trim().slice(0, 30)}" ${r.height}px`)
      }
      return out
    })
    expect(small).toEqual([])
  })

  test('results are cards, the sort has a control, and chips wrap', async ({ page }) => {
    await mockApi(page, { me: STAFF })
    await mockSearch(page)
    await page.goto(`/tickets?priority=high&status=${ST_NEW}&created_from=2026-10-01&created_to=2026-10-31`)
    await expect(page.getByRole('group', { name: 'Active filters' })).toBeVisible()
    await expect(page.getByLabel('Sort by')).toBeVisible()
    await page.getByLabel('Sort by').selectOption('subject')
    await expect.poll(() => new URL(page.url()).searchParams.get('sort')).toBe('subject')
    await noHorizontalScroll(page)
    await shot(page, 'phone-results-chips')
  })

  test('a reply match shows under the subject on the card', async ({ page }) => {
    await mockApi(page, { me: STAFF })
    await mockSearch(page)
    await page.goto('/tickets?q=fuser')
    await expect(page.locator('mark:visible').first()).toBeVisible()
    await noHorizontalScroll(page)
    await shot(page, 'phone-reply-match')
  })
})

test.describe('nothing scrolls sideways at 320 px', () => {
  test.use({ viewport: { width: 320, height: 844 } })

  test('results with chips, and the open dialog', async ({ page }) => {
    await mockApi(page, { me: ADMIN })
    await mockSearch(page)
    await page.goto(`/tickets?q=fuser&reporter=${DANA.id}&priority=high&priority=low&status=${ST_NEW}&created_from=2026-10-01&created_to=2026-10-31&category=${CAT_HW}&type=${TY_PRINTERS}`)
    await expect(page.locator('mark:visible').first()).toBeVisible()
    await noHorizontalScroll(page)

    await page.getByRole('button', { name: /^Filters/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Filters' })
    await dialog.getByRole('button', { name: /^More filters/ }).click()
    const wide = await dialog.evaluate(el => {
      const body = el.querySelector('div.overflow-y-auto') as HTMLElement
      return body.scrollWidth - body.clientWidth
    })
    expect(wide).toBeLessThanOrEqual(0)
  })
})
