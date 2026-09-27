import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_REPORTER, TICKET_DETAIL, TICKETS } from './mock'

// #296: the shell has a fixed 240px sidebar with no breakpoint, so every
// page inside it is unusable — not overflowing, just crushed — at a phone
// viewport. DESIGN.md's Small screens section states the rule this pins:
// no horizontal scrolling at 390 CSS pixels, and tap targets large enough
// to hit with a thumb, on the four in-scope pages (Dashboard, TicketList,
// TicketDetail, NewTicket). Admin pages are explicitly out of scope.
//
// iPhone 13: 390x844 CSS pixels. This is the exact viewport DESIGN.md and
// issue #296 measured against.
test.use({ viewport: { width: 390, height: 844 } })

async function noHorizontalScroll(page: Page) {
  // NOT document.documentElement: every page's content sits inside
  // <main class="overflow-auto"> in a shell whose row is overflow-hidden, so
  // the document itself can never be wider than the viewport regardless of
  // what overflows inside main — exactly the shape of check #296 warned
  // against ("scrollWidth == innerWidth ... passed this and did"). Measuring
  // main's own box is what actually catches a too-wide child of it.
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, 'main is wider than its own box at 390px').toBeLessThanOrEqual(clientWidth)
}

test.describe('shell + pages fit at 390 CSS pixels', () => {
  test('dashboard', async ({ page }) => {
    await mockApi(page)
    await page.goto('/dashboard')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('ticket list (staff)', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets')
    // The desktop table and the mobile card both exist in the DOM (CSS
    // picks one), so a bare text query is ambiguous — scoped to the card,
    // which is the thing this page is actually testing.
    await expect(page.locator('li').filter({ hasText: 'Printer on fire, second floor' })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('ticket list (reporter)', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await page.goto('/tickets')
    await expect(page.locator('li').filter({ hasText: 'Printer on fire, second floor' })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('ticket detail', async ({ page }) => {
    await mockApi(page)
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    await expect(page.getByText(TICKET_DETAIL.subject)).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('new ticket (staff)', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets/new')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('new ticket (reporter)', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await page.goto('/tickets/new')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await noHorizontalScroll(page)
  })
})

test.describe('the sidebar is reachable, not just absent', () => {
  test('a drawer trigger opens the navigation, and it closes again', async ({ page }) => {
    await mockApi(page)
    await page.goto('/dashboard')

    // The desktop sidebar's own nav must not be sitting in the page,
    // consuming width, at this viewport.
    const nav = page.getByRole('navigation')
    await expect(nav).toBeHidden()

    const trigger = page.getByRole('button', { name: /menu|navigation/i })
    await expect(trigger).toBeVisible()
    const box = await trigger.boundingBox()
    expect(box?.width ?? 0, 'drawer trigger is smaller than a 24px tap target').toBeGreaterThanOrEqual(24)
    expect(box?.height ?? 0, 'drawer trigger is smaller than a 24px tap target').toBeGreaterThanOrEqual(24)

    // Scoped to the nav landmark, not the whole page: DashboardPage has its
    // own cards linking to /tickets, so an unscoped name query is ambiguous.
    await trigger.click()
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()

    await page.keyboard.press('Escape')
    await expect(page.getByRole('navigation')).toBeHidden()
  })

  test('resizing past md closes an open drawer instead of leaving it trapped behind the sidebar', async ({ page }) => {
    await mockApi(page)
    await page.goto('/dashboard')

    await page.getByRole('button', { name: /menu|navigation/i }).click()
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()

    // Widening past md (768px) is what a phone rotating to landscape, or a
    // window being resized, looks like. Without a listener for this, the
    // drawer stays open and its focus trap and overlay keep covering the now
    // otherwise-usable desktop layout underneath.
    await page.setViewportSize({ width: 1024, height: 800 })
    await expect(page.getByRole('navigation')).toBeVisible()

    // Back below md: the drawer must not have silently reopened — the
    // permanent sidebar is what took its place, and only its own trigger
    // reopens the drawer again.
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(page.getByRole('navigation')).toBeHidden()
  })
})

test.describe('bulk ticket selection stays desktop-only', () => {
  test('a selection made at desktop width does not survive into the mobile card view', async ({ page }) => {
    await mockApi(page)
    // Wide first: bulk selection is a table feature, not reachable from the
    // card view at all, so the selection itself has to be made at desktop
    // width before resizing down to prove anything about what happens next.
    await page.setViewportSize({ width: 1024, height: 800 })
    await page.goto('/tickets')

    await page
      .getByRole('checkbox', { name: `Select ticket ${TICKETS[0].tracking_number}` })
      .check()
    await expect(page.getByText('1 selected')).toBeVisible()

    // Narrow to the phone viewport this whole file otherwise runs at,
    // without reloading — a resize, not a fresh navigation, since a stale
    // selection surviving a page load was never the failure mode here.
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(page.getByText('1 selected')).toBeHidden()
    await noHorizontalScroll(page)
  })
})

test.describe('tap targets are large enough to hit with a thumb', () => {
  // mock.ts populates this ticket with an assignee, a tag, a linked ticket, a
  // custom field and an attachment pair (one clean, one quarantined with two
  // reputation providers) specifically so this scan reaches the controls
  // those panels only render once populated — AssigneePanel's "Clear
  // assignment", CustomFieldsPanel's "Edit", TagInput's "Remove tag",
  // LinkedTicketsPanel's link anchor and "Remove link", and AttachmentList's
  // "Look up on VirusTotal", "Show all N services" and "Open … report".
  // Against the empty-array fixture this test used before, none of those
  // elements exist to measure, so the pass they gate never actually ran on
  // them.
  test('ticket detail: every control, once every panel actually renders', async ({ page }) => {
    await mockApi(page)
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    await expect(page.getByText(TICKET_DETAIL.subject)).toBeVisible()

    const targets = await page.evaluate(() => {
      const els = Array.from(document.querySelectorAll('button, a[href], input[type="checkbox"], [role="button"]'))
      return els
        .map((el) => {
          // A checkbox wrapped in a <label> with its own text is reachable
          // by tapping that whole label, natively, with no JS behind it — the
          // effective target is the label's box, not the 16px checkbox alone.
          const target = el.closest('label') ?? el
          const r = target.getBoundingClientRect()
          return { text: (el.textContent || el.getAttribute('aria-label') || '').trim().slice(0, 40), w: r.width, h: r.height }
        })
        .filter((t) => t.w > 0 && t.h > 0)
    })

    const tooSmall = targets.filter((t) => t.w < 24 || t.h < 24)
    expect(
      tooSmall,
      `controls under 24x24px: ${JSON.stringify(tooSmall)}`,
    ).toEqual([])
  })
})
