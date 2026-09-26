import { test, expect, type Page } from '@playwright/test'
import { mockApi, ME_REPORTER, TICKET_DETAIL } from './mock'

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
  const { scrollWidth, innerWidth } = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    innerWidth: window.innerWidth,
  }))
  expect(scrollWidth, 'page is wider than the viewport at 390px').toBeLessThanOrEqual(innerWidth)
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
})

test.describe('tap targets are large enough to hit with a thumb', () => {
  test('ticket detail: SHA-256 copy and assignee toggle', async ({ page }) => {
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
