import { test, expect, type Page } from '@playwright/test'
import { mockAdminApi, mockApi, ME_REPORTER, ME_STAFF } from './mock'

// The admin sidebar is one expanding Admin item holding four collapsible
// sections. Layout tests only: the
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

// Room to spare, not just a fit. The same page measures about 11px taller on
// the Linux CI runner than on a Mac (system fonts lay the sidebar out
// differently), and a test that passes with 0.5px to spare on one fails on the
// other. Every heading, and the last link, must sit this far above the bottom
// of the nav.
const HEADROOM = 24

test.describe('the headings fit with headroom to spare, notice bar showing', () => {
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
        // Admin and only the current section are open: this is the state the
        // budget is for.
        await expect(nav.getByRole('button', { name: 'Admin', exact: true })).toHaveAttribute('aria-expanded', 'true')
        for (const s of SECTIONS) {
          await expect(nav.getByRole('button', { name: s.name })).toHaveAttribute('aria-expanded', String(s.name === p.open))
        }

        const navBox = (await nav.boundingBox())!
        const navBottom = navBox.y + navBox.height
        const headroom: Record<string, number> = {}
        for (const name of ['Admin', ...SECTIONS.map((s) => s.name)]) {
          const h = nav.getByRole('button', { name, exact: true })
          // IntersectionObserver honours clipping by the nav's own scroll box,
          // which a plain bounding-box comparison with the window would not.
          await expect(h, `${name} heading is not fully visible`).toBeInViewport({ ratio: 1 })
          const box = (await h.boundingBox())!
          expect(box.y, `${name} is above the nav`).toBeGreaterThanOrEqual(navBox.y)
          expect(box.y + box.height, `${name} is below the window`).toBeLessThanOrEqual(height)
          headroom[name] = navBottom - (box.y + box.height)
        }
        // The last thing in the nav: a link when the section open is System.
        const lastLinkBottom = await nav.evaluate((el) =>
          Math.max(...Array.from(el.querySelectorAll('a')).filter((a) => a.getClientRects().length > 0).map((a) => a.getBoundingClientRect().bottom)),
        )
        headroom['last link'] = navBottom - lastLinkBottom

        for (const [name, room] of Object.entries(headroom)) {
          expect(room, `${name} has ${Math.round(room)}px above the bottom of the nav; wants ${HEADROOM}`).toBeGreaterThanOrEqual(HEADROOM)
        }
        // In the log, so a run on a different machine says how much room it had.
        console.log(`headroom 1440x${height} ${p.path}: smallest ${Math.round(Math.min(...Object.values(headroom)))}px of ${HEADROOM}px wanted`)
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
        const admin = nav.getByRole('button', { name: 'Admin', exact: true })
        const heading = nav.getByRole('button', { name: s.name, exact: true })
        await expect(admin).toHaveAttribute('aria-expanded', 'false')
        // Closed Admin hides its sections, and so their links.
        await expect(heading).toBeHidden()
        await expect(nav.getByRole('link', { name: label, exact: true })).toBeHidden()

        await admin.click()
        await expect(admin).toHaveAttribute('aria-expanded', 'true')
        await expect(heading).toHaveAttribute('aria-expanded', 'false')
        await expect(nav.getByRole('link', { name: label, exact: true })).toBeHidden()

        await heading.click()
        await expect(heading).toHaveAttribute('aria-expanded', 'true')
        await nav.getByRole('link', { name: label, exact: true }).click()

        await expect(page).toHaveURL(new RegExp(`${href}$`))
        await expect(admin).toHaveAttribute('aria-expanded', 'true')
        await expect(nav.getByRole('button', { name: s.name, exact: true })).toHaveAttribute('aria-expanded', 'true')
      })
    }
  }
})

// Safari's own rule, which WebKit on macOS follows: Tab skips buttons (and,
// here, links) unless "full keyboard access" is on; Option-Tab is the key that
// visits every control. Elsewhere plain Tab does.
const tabKey = (browserName: string) => (browserName === 'webkit' && process.platform === 'darwin' ? 'Alt+Tab' : 'Tab')

test.describe('the keyboard', () => {
  test('Tab reaches Admin and then every section, Enter and Space toggle them', async ({ page, browserName }) => {
    const tab = tabKey(browserName)
    await page.setViewportSize({ width: 1440, height: 800 })
    await mockAdminApi(page, { short: true })
    await page.goto('/dashboard')
    const nav = page.locator('aside nav')
    await expect(nav.getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()
    const admin = nav.getByRole('button', { name: 'Admin', exact: true })

    const focusedControl = () =>
      page.evaluate(() => (document.activeElement?.hasAttribute('aria-controls') ? document.activeElement.textContent?.trim() : null))

    // Closed, Admin is the only toggle in the tab order: its sections are
    // hidden inside it.
    let first: string | null | undefined = null
    for (let i = 0; i < 40 && !first; i++) {
      await page.keyboard.press(tab)
      first = await focusedControl()
    }
    expect(first).toBe('Admin')
    await expect(admin).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(admin).toHaveAttribute('aria-expanded', 'true')

    const reached: string[] = []
    for (let i = 0; i < 40 && reached.length < SECTIONS.length; i++) {
      await page.keyboard.press(tab)
      const name = await focusedControl()
      if (name) reached.push(name)
    }
    expect(reached).toEqual(SECTIONS.map((s) => s.name))

    // Focus is on System. Shift+Tab back to People, then toggle it.
    for (let i = 0; i < SECTIONS.length - 1; i++) await page.keyboard.press(`Shift+${tab}`)
    const people = nav.getByRole('button', { name: 'People', exact: true })
    await expect(people).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(people).toHaveAttribute('aria-expanded', 'true')
    await page.keyboard.press('Space')
    await expect(people).toHaveAttribute('aria-expanded', 'false')

    // And Space closes Admin itself.
    await admin.focus()
    await page.keyboard.press('Space')
    await expect(admin).toHaveAttribute('aria-expanded', 'false')
  })
})

test.describe('staff and requesters have no Admin menu and no Audit Log', () => {
  for (const [who, me] of [['staff', ME_STAFF], ['requester', ME_REPORTER]] as const) {
    test(who, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 800 })
      await mockApi(page, { me })
      await page.goto('/dashboard')
      const nav = page.locator('aside nav')
      await expect(nav.getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()
      await expect(nav.getByRole('link')).toHaveText(['Dashboard', 'Tickets'])
      await expect(nav.getByRole('button')).toHaveCount(0)
      await expect(nav.getByRole('link', { name: 'Audit Log' })).toHaveCount(0)

      // The route turns them away as it does the other admin pages.
      await page.goto('/admin/audit')
      await expect(page).toHaveURL(/\/dashboard$/)
    })
  }
})

test.describe('on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test('the drawer holds Admin and its sections, their toggles are 44px, and choosing a page closes it', async ({ page }) => {
    await mockAdminApi(page, { short: true })
    await page.goto('/dashboard')
    await page.getByRole('button', { name: 'Open navigation menu' }).click()

    const drawer = page.getByRole('dialog')
    const admin = drawer.getByRole('button', { name: 'Admin', exact: true })
    await expect(admin).toBeVisible()
    expect((await admin.boundingBox())!.height, 'Admin is shorter than 44px').toBeGreaterThanOrEqual(44)
    await admin.click()
    for (const s of SECTIONS) {
      const h = drawer.getByRole('button', { name: s.name, exact: true })
      await expect(h).toBeVisible()
      const box = (await h.boundingBox())!
      expect(box.height, `${s.name} is shorter than 44px`).toBeGreaterThanOrEqual(44)
    }

    await drawer.getByRole('button', { name: 'Integrations', exact: true }).click()
    const webhooks = drawer.getByRole('link', { name: 'Webhooks', exact: true })
    expect((await webhooks.boundingBox())!.height, 'a section link is shorter than 24px').toBeGreaterThanOrEqual(24)
    await webhooks.click()

    await expect(page).toHaveURL(/\/admin\/webhooks$/)
    await expect(page.getByRole('dialog')).toBeHidden()

    // The next time the drawer opens, the page's own Admin and section are already open.
    await page.getByRole('button', { name: 'Open navigation menu' }).click()
    const again = page.getByRole('dialog')
    await expect(again.getByRole('button', { name: 'Admin', exact: true })).toHaveAttribute('aria-expanded', 'true')
    await expect(again.getByRole('button', { name: 'Integrations', exact: true })).toHaveAttribute('aria-expanded', 'true')
  })
})
