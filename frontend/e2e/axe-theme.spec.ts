import AxeBuilder from '@axe-core/playwright'
import { test, expect, type Page } from '@playwright/test'
import { mockApi, mockAdminApi, mockCti, ME_ADMIN, ME_STAFF, STATUSES, TICKET_DETAIL } from './mock'

// #709: the token contrast test proves the palette; this proves the pages use it.
// A token on the wrong surface (text-fg-faint on bg-muted-strong), a light-only
// colour that slipped past the guard, an icon button with no name: axe sees what
// the page really paints. Every key page, in both themes, against the mocked API.
// Chromium only (axe results do not differ by engine).
//
// Rules: colour contrast, form labels, button names and every aria-* rule. A
// violation is fixed at the token or in the component, never silenced here; the
// only entry in KNOWN is a design trade-off that is reported instead.

const RULES = [
  'color-contrast',
  'label',
  'button-name',
  'link-name',
  'select-name',
  'aria-allowed-attr',
  'aria-allowed-role',
  'aria-command-name',
  'aria-dialog-name',
  'aria-hidden-body',
  'aria-hidden-focus',
  'aria-input-field-name',
  'aria-meter-name',
  'aria-progressbar-name',
  'aria-prohibited-attr',
  'aria-required-attr',
  'aria-required-children',
  'aria-required-parent',
  'aria-roledescription',
  'aria-roles',
  'aria-text',
  'aria-toggle-field-name',
  'aria-tooltip-name',
  'aria-treeitem-name',
  'aria-valid-attr',
  'aria-valid-attr-value',
]

// A violation that is a design trade-off, not a defect: "<page> <theme> <rule> <selector fragment>".
// Empty on purpose; an entry needs a reason next to it.
const KNOWN: string[] = []

type Theme = 'light' | 'dark'

const unauthenticated = { status: 401, json: { error: { code: 'unauthorized', message: 'no session' } } }
const SITE = { name: 'Go Help Desk', logo_url: '', version: '1.3.0', guest_submission_enabled: true, registration_enabled: true }

async function signedOut(page: Page) {
  await mockApi(page)
  await page.route('**/api/v1/site', (route) => route.fulfill({ json: SITE }))
  await page.route('**/api/v1/me', (route) => route.fulfill(unauthenticated))
}

// An administrator's own status colour is drawn as chosen in the light theme
// (DESIGN.md, Appearance: the colour is data, and light is unchanged), so a pale
// choice can fail AA there and this spec does not judge it. These are AA on white;
// in dark the helper makes any colour readable, which theme.spec.ts checks with a navy.
const READABLE_STATUSES = STATUSES.map((s, i) => ({ ...s, color: ['#1d4ed8', '#15803d', '#4b5563'][i] }))

interface Case {
  name: string
  url: string
  /** Who is signed in; the account's theme is set to the one under test. Absent: signed out. */
  me?: typeof ME_STAFF
  setup: (page: Page) => Promise<void>
  /** Something that is only on screen once the page has its data. */
  ready: (page: Page) => Promise<void>
}

const main = (page: Page) => page.locator('main h1, main h2').first()
const heading = (name: string | RegExp) => (page: Page) => expect(page.getByRole('heading', { name }).first()).toBeVisible()

const CASES: Case[] = [
  { name: 'login', url: '/login', setup: signedOut, ready: heading(/Sign in/) },
  { name: 'signup', url: '/signup', setup: signedOut, ready: (p) => expect(p.getByText('Create an account')).toBeVisible() },
  {
    name: 'guest submit',
    url: '/submit',
    setup: async (page) => {
      await signedOut(page)
      await mockCti(page)
    },
    ready: heading('Submit a request'),
  },
  { name: 'dashboard', me: ME_STAFF, url: '/dashboard', setup: (p) => mockApi(p, { me: ME_STAFF }), ready: (p) => expect(main(p)).toBeVisible() },
  { name: 'ticket list', me: ME_STAFF, url: '/tickets', setup: (p) => mockApi(p, { me: ME_STAFF }), ready: (p) => expect(p.locator('a:visible', { hasText: 'Printer on fire, second floor' }).first()).toBeVisible() },
  { name: 'ticket detail', me: ME_STAFF, url: `/tickets/${TICKET_DETAIL.id}`, setup: (p) => mockApi(p, { me: ME_STAFF }), ready: (p) => expect(p.getByText(TICKET_DETAIL.subject).first()).toBeVisible() },
  { name: 'new ticket', me: ME_STAFF, url: '/tickets/new', setup: (p) => mockApi(p, { me: ME_STAFF }), ready: (p) => expect(p.locator('main h1').first()).toBeVisible() },
  { name: 'reports', me: ME_ADMIN, url: '/reports', setup: (p) => mockApi(p, { me: ME_ADMIN }), ready: (p) => expect(p.getByRole('region', { name: 'Workload' })).toBeVisible() },
  { name: 'your account', me: ME_STAFF, url: '/account', setup: (p) => mockApi(p, { me: ME_STAFF }), ready: heading('Appearance') },
  { name: 'admin: general settings', me: ME_ADMIN, url: '/admin/settings/general', setup: (p) => mockAdminApi(p, { short: true }), ready: (p) => expect(main(p)).toBeVisible() },
  { name: 'admin: branding', me: ME_ADMIN, url: '/admin/settings/branding', setup: (p) => mockAdminApi(p, { short: true }), ready: (p) => expect(main(p)).toBeVisible() },
  { name: 'admin: authentication', me: ME_ADMIN, url: '/admin/settings/authentication', setup: (p) => mockAdminApi(p, { short: true }), ready: (p) => expect(main(p)).toBeVisible() },
  { name: 'admin: categories', me: ME_ADMIN, url: '/admin/categories', setup: (p) => mockAdminApi(p, { short: true }), ready: (p) => expect(main(p)).toBeVisible() },
  { name: 'admin: users', me: ME_ADMIN, url: '/admin/users', setup: (p) => mockAdminApi(p, { short: true }), ready: (p) => expect(p.getByText('a@b.co').first()).toBeVisible() },
  { name: 'admin: statuses', me: ME_ADMIN, url: '/admin/statuses', setup: (p) => mockAdminApi(p, { short: true }), ready: (p) => expect(main(p)).toBeVisible() },
  { name: 'admin: webhooks', me: ME_ADMIN, url: '/admin/webhooks', setup: (p) => mockAdminApi(p, { short: true }), ready: (p) => expect(main(p)).toBeVisible() },
]

async function violationsOn(page: Page, theme: Theme, c: Case) {
  await page.addInitScript((t) => {
    try {
      window.localStorage.setItem('ghd.theme', t)
    } catch {
      /* the page then follows the device, which is light here */
    }
  }, theme)
  await c.setup(page)
  await page.route('**/api/v1/statuses', (route) => route.fulfill({ json: READABLE_STATUSES }))
  if (c.me) {
    // The account's own value wins on sign-in, so it has to say the theme under test.
    await page.route('**/api/v1/me', (route) =>
      route.request().method() === 'GET' ? route.fulfill({ json: { ...c.me, theme } }) : route.fallback(),
    )
  }
  await page.goto(c.url)
  await c.ready(page)
  await page.waitForLoadState('networkidle')
  // The test is only worth anything if the page is in the theme it claims.
  expect(await page.evaluate(() => document.documentElement.classList.contains('dark'))).toBe(theme === 'dark')

  const { violations } = await new AxeBuilder({ page }).withRules(RULES).analyze()
  return violations.flatMap((v) =>
    v.nodes
      .map((n) => ({ rule: v.id, target: n.target.join(' '), why: n.failureSummary?.replace(/\s+/g, ' ') ?? '' }))
      .filter((n) => !KNOWN.some((k) => k === `${c.name} ${theme} ${n.rule} ${n.target}`)),
  )
}

for (const theme of ['light', 'dark'] as Theme[]) {
  test.describe(`axe, ${theme} theme, 1280px`, () => {
    test.use({ viewport: { width: 1280, height: 900 }, colorScheme: 'light' })
    for (const c of CASES) {
      test(c.name, async ({ page }) => {
        const found = await violationsOn(page, theme, c)
        expect(found, JSON.stringify(found, null, 1)).toEqual([])
      })
    }
  })

  test.describe(`axe, ${theme} theme, 375px`, () => {
    test.use({ viewport: { width: 375, height: 800 }, colorScheme: 'light' })
    for (const c of CASES.filter((x) => x.name === 'login' || x.name === 'ticket list')) {
      test(c.name, async ({ page }) => {
        const found = await violationsOn(page, theme, c)
        expect(found, JSON.stringify(found, null, 1)).toEqual([])
      })
    }
  })
}
