import { test, expect } from '@playwright/test'
import { mockAdminApi } from './mock'

// "Require SAML for staff and users" on while SAML is not loaded: the server
// says so in GET /admin/security-warnings (saml_required_not_loaded) and an
// administrator sees it in the server problems bar and on the dashboard's
// health card (docs/DESIGN.md, SAML, "A server problem while saml_enabled is on
// and SAML is not loaded"). Every backend call is mocked.

const TITLE = 'Staff and users cannot sign in: SAML is required but is not working'

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

  test('the health card lists it, in red, with a link to the SAML section', async ({ page }) => {
    await mockAdminApi(page, { short: true, warnings: PROBLEM })
    await page.goto('/dashboard')

    const card = page.getByRole('region', { name: 'Health' })
    await expect(card).toContainText(
      'SAML is required for staff and users but is not working, so they cannot sign in. Administrators can still sign in with a password.',
    )
    // Red: something is broken now.
    await expect(card.getByRole('listitem')).toHaveClass(/border-red-200/)
    const link = card.getByRole('link', { name: 'Authentication → SAML' })
    await expect(link).toHaveAttribute('href', '/admin/settings/authentication')

    await link.click()
    await expect(page).toHaveURL(/\/admin\/settings\/authentication$/)
  })
})

// Saving the fix is what clears it: no refocus, no Details click, no reload.
test('it goes away by itself when Save SAML config loads SAML', async ({ page }) => {
  await mockAdminApi(page, { short: true, warnings: PROBLEM })
  await page.route('**/api/v1/admin/saml', async (route) => {
    if (route.request().method() !== 'PUT') return route.fallback()
    // SAML loads: from now on the server stops reporting the problem.
    await page.route('**/api/v1/admin/security-warnings', (r) => r.fulfill({ json: QUIET }))
    await route.fulfill({ status: 204, body: '' })
  })
  await page.goto('/admin/settings/authentication')
  const bar = page.getByRole('region', { name: 'Server notices' })
  await expect(bar).toContainText(TITLE)

  await page.getByRole('button', { name: 'Save SAML config' }).click()

  await expect(bar).toHaveCount(0)
})

test('it goes away by itself when the page Save turns the requirement off', async ({ page }) => {
  await mockAdminApi(page, { short: true, warnings: PROBLEM, settings: { saml_enabled: true } })
  await page.route('**/api/v1/admin/settings', async (route) => {
    if (route.request().method() !== 'PATCH') return route.fallback()
    await page.route('**/api/v1/admin/security-warnings', (r) => r.fulfill({ json: QUIET }))
    await route.fulfill({ status: 204, body: '' })
  })
  await page.goto('/admin/settings/authentication')
  const bar = page.getByRole('region', { name: 'Server notices' })
  await expect(bar).toContainText(TITLE)

  await page.getByRole('switch', { name: 'Require SAML for staff and users' }).click()
  await page.getByRole('button', { name: 'Save changes' }).last().click()

  await expect(bar).toHaveCount(0)
})

test('with nothing to report the card is one muted line and the bar is absent', async ({ page }) => {
  await mockAdminApi(page, { short: true, warnings: QUIET })
  await page.goto('/dashboard')

  await expect(page.getByText('No problems to report.')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Server notices' })).toHaveCount(0)
})
