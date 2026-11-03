import { test, expect, type Page } from '@playwright/test'
import { mockAdminApi } from './mock'

// The admin sidebar is four collapsible sections. Layout tests only: the
// component tests (src/components/AdminNavSections.test.tsx) own who sees
// what; this file owns what jsdom cannot compute, which is whether the
// headings actually fit on a laptop screen, and whether a real browser lets
// you reach every page through them.

const SECTIONS = [
  { name: 'People', links: [['Users', '/admin/users'], ['Groups', '/admin/groups'], ['Roles', '/admin/roles']] },
  {
    name: 'Tickets',
    links: [
      ['Categories', '/admin/categories'], ['Statuses', '/admin/statuses'], ['Tags', '/admin/tags'],
      ['Custom Fields', '/admin/custom-fields'], ['Canned Responses', '/admin/canned-responses'], ['Routing', '/admin/routing'],
    ],
  },
  {
    name: 'Integrations',
    links: [
      ['API Keys', '/admin/api-keys'], ['OAuth Clients', '/admin/oauth-clients'],
      ['Webhooks', '/admin/webhooks'], ['Email Templates', '/admin/email-templates'],
    ],
  },
  { name: 'System', links: [['Settings', '/admin/settings'], ['Audit Log', '/admin/audit']] },
] as const

// The collapsed server-notice bar above the shell: two problems, so the bar
// is showing and eats its share of the height, which is what the fit test is
// about. Collapsed it is one slim line.
async function showServerNotices(page: Page) {
  await page.route('**/api/v1/admin/security-warnings', (route) =>
    route.fulfill({
      json: {
        insecure_secrets: [],
        attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '' },
        attachment_storage: { writable: false, problem: 'permission denied' },
        passkeys: { available: false, reason: 'BASE_URL host example.internal has no public suffix' },
      },
    }),
  )
}

async function openAdmin(page: Page, path: string) {
  await mockAdminApi(page, { short: true })
  await showServerNotices(page)
  await page.goto(path)
}

test.describe('the headings fit without scrolling, notice bar showing', () => {
  // One page in each section, so each section gets its turn as the open one.
  // Tickets is the long one (six links) and the case that decides the height.
  const pages = [
    { open: 'People', path: '/admin/users' },
    { open: 'Tickets', path: '/admin/categories' },
    { open: 'Integrations', path: '/admin/webhooks' },
    { open: 'System', path: '/admin/settings' },
  ]
  for (const height of [768, 800, 900]) {
    for (const p of pages) {
      test(`1440x${height}, on ${p.path}`, async ({ page }) => {
        await page.setViewportSize({ width: 1440, height })
        await openAdmin(page, p.path)
        await expect(page.getByText(/2 server problems need attention/)).toBeVisible()

        const nav = page.locator('aside nav')
        // Only the current section is open: this is the state the budget is for.
        for (const s of SECTIONS) {
          await expect(nav.getByRole('button', { name: s.name })).toHaveAttribute('aria-expanded', String(s.name === p.open))
        }

        const navBox = (await nav.boundingBox())!
        for (const s of SECTIONS) {
          const h = nav.getByRole('button', { name: s.name })
          // IntersectionObserver honours clipping by the nav's own scroll box,
          // which a plain bounding-box comparison with the window would not.
          await expect(h, `${s.name} heading is not fully visible`).toBeInViewport({ ratio: 1 })
          const box = (await h.boundingBox())!
          expect(box.y, `${s.name} is above the nav`).toBeGreaterThanOrEqual(navBox.y)
          expect(box.y + box.height, `${s.name} is below the nav's bottom edge`).toBeLessThanOrEqual(navBox.y + navBox.height)
          expect(box.y + box.height, `${s.name} is below the window`).toBeLessThanOrEqual(height)
        }
        // The nav itself needs no scrollbar either.
        const { scrollHeight, clientHeight } = await nav.evaluate((el) => ({ scrollHeight: el.scrollHeight, clientHeight: el.clientHeight }))
        expect(scrollHeight, 'the nav scrolls').toBeLessThanOrEqual(clientHeight)
      })
    }
  }
})

test.describe('every admin page is reachable through its section', () => {
  for (const s of SECTIONS) {
    for (const [label, href] of s.links) {
      test(`${s.name} -> ${label}`, async ({ page }) => {
        await page.setViewportSize({ width: 1440, height: 800 })
        await mockAdminApi(page, { short: true })
        await page.goto('/dashboard')

        const nav = page.locator('aside nav')
        const heading = nav.getByRole('button', { name: s.name })
        await expect(heading).toHaveAttribute('aria-expanded', 'false')
        await expect(nav.getByRole('link', { name: label, exact: true })).toBeHidden()

        await heading.click()
        await expect(heading).toHaveAttribute('aria-expanded', 'true')
        await nav.getByRole('link', { name: label, exact: true }).click()

        await expect(page).toHaveURL(new RegExp(`${href}$`))
        await expect(nav.getByRole('button', { name: s.name })).toHaveAttribute('aria-expanded', 'true')
      })
    }
  }
})

// Safari's own rule, which WebKit on macOS follows: Tab skips buttons (and,
// here, links) unless "full keyboard access" is on; Option-Tab is the key that
// visits every control. Elsewhere plain Tab does.
const tabKey = (browserName: string) => (browserName === 'webkit' && process.platform === 'darwin' ? 'Alt+Tab' : 'Tab')

test.describe('the keyboard', () => {
  test('Tab reaches every heading, Enter and Space toggle it', async ({ page, browserName }) => {
    const tab = tabKey(browserName)
    await page.setViewportSize({ width: 1440, height: 800 })
    await mockAdminApi(page, { short: true })
    await page.goto('/dashboard')
    await expect(page.locator('aside nav').getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()

    const reached: string[] = []
    for (let i = 0; i < 40 && reached.length < SECTIONS.length; i++) {
      await page.keyboard.press(tab)
      const name = await page.evaluate(() => (document.activeElement?.hasAttribute('aria-controls') ? document.activeElement.textContent : null))
      if (name) reached.push(name.trim())
    }
    expect(reached).toEqual(SECTIONS.map((s) => s.name))

    // Focus is on System. Shift+Tab to People, then toggle it.
    for (let i = 0; i < SECTIONS.length - 1; i++) await page.keyboard.press(`Shift+${tab}`)
    const people = page.locator('aside nav').getByRole('button', { name: 'People' })
    await expect(people).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(people).toHaveAttribute('aria-expanded', 'true')
    await page.keyboard.press('Space')
    await expect(people).toHaveAttribute('aria-expanded', 'false')
  })
})

test.describe('on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test('the drawer holds the sections, their headings are 44px, and choosing a page closes it', async ({ page }) => {
    await mockAdminApi(page, { short: true })
    await page.goto('/dashboard')
    await page.getByRole('button', { name: 'Open navigation menu' }).click()

    const drawer = page.getByRole('dialog')
    for (const s of SECTIONS) {
      const h = drawer.getByRole('button', { name: s.name })
      await expect(h).toBeVisible()
      const box = (await h.boundingBox())!
      expect(box.height, `${s.name} is shorter than 44px`).toBeGreaterThanOrEqual(44)
    }

    await drawer.getByRole('button', { name: 'Integrations' }).click()
    const webhooks = drawer.getByRole('link', { name: 'Webhooks', exact: true })
    expect((await webhooks.boundingBox())!.height, 'a section link is shorter than 24px').toBeGreaterThanOrEqual(24)
    await webhooks.click()

    await expect(page).toHaveURL(/\/admin\/webhooks$/)
    await expect(page.getByRole('dialog')).toBeHidden()

    // The next time the drawer opens, the page's own section is already open.
    await page.getByRole('button', { name: 'Open navigation menu' }).click()
    await expect(page.getByRole('dialog').getByRole('button', { name: 'Integrations' })).toHaveAttribute('aria-expanded', 'true')
  })
})
