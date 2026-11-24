import { test, expect } from '@playwright/test'
import { mockAdminApi } from './mock'

// "Require SAML for staff and users" on while SAML is not loaded: the server
// says so in GET /admin/security-warnings (saml_required_not_loaded) and an
// administrator sees it in the server problems bar and on the dashboard's
// health card (docs/DESIGN.md, SAML, "A server problem while saml_enabled is on
// and SAML is not loaded"). Every backend call is mocked.

const TITLE = 'Staff and users cannot sign in: SAML is required but not set up'

const QUIET = {
  insecure_secrets: [],
  attachment_scanning: { policy: 'off', configured: false, reachable: false, effect: '', policy_explicit: false, stale_off: false },
}
const PROBLEM = { ...QUIET, saml_required_not_loaded: { reason: 'incomplete' } }

test.describe('at 320px', () => {
  test.use({ viewport: { width: 320, height: 844 } })

  test('the bar names it, Details explains it and links to Authentication, and nothing scrolls sideways', async ({ page }) => {
    await mockAdminApi(page, { short: true, warnings: PROBLEM })
    await page.goto('/dashboard')

    const bar = page.getByRole('region', { name: 'Server notices' })
    await expect(bar).toContainText(TITLE)

    await page.getByRole('button', { name: /details/i }).click()
    await expect(bar).toContainText('Administrators can still sign in with a password')
    const link = bar.getByRole('link', { name: /System → Authentication → SAML/ })
    await expect(link).toHaveAttribute('href', '/admin/settings/authentication')

    expect(await page.evaluate(() => document.documentElement.scrollWidth), 'the page scrolls sideways at 320px').toBeLessThanOrEqual(320)
  })

  test('the health card lists it, red, with a link to the SAML section', async ({ page }) => {
    await mockAdminApi(page, { short: true, warnings: PROBLEM })
    await page.goto('/dashboard')

    const card = page.getByRole('region', { name: 'Health' })
    await expect(card).toContainText('SAML sign-in is required for staff and users, but SAML is not set up, so they cannot sign in.')
    const link = card.getByRole('link', { name: 'Authentication → SAML' })
    await expect(link).toHaveAttribute('href', '/admin/settings/authentication')

    await link.click()
    await expect(page).toHaveURL(/\/admin\/settings\/authentication$/)
  })
})

test('it goes away by itself once the server stops reporting it', async ({ page }) => {
  await mockAdminApi(page, { short: true, warnings: PROBLEM })
  await page.goto('/dashboard')
  const bar = page.getByRole('region', { name: 'Server notices' })
  await expect(bar).toContainText(TITLE)

  // SAML loaded (or the switch turned off) meanwhile: the next read has no field.
  await page.route('**/api/v1/admin/security-warnings', (route) => route.fulfill({ json: QUIET }))
  await page.getByRole('button', { name: /details/i }).click()

  await expect(bar).toHaveCount(0)
})

test('with nothing to report the card is one muted line and the bar is absent', async ({ page }) => {
  await mockAdminApi(page, { short: true, warnings: QUIET })
  await page.goto('/dashboard')

  await expect(page.getByText('No problems to report.')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Server notices' })).toHaveCount(0)
})
