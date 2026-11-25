import { test, expect } from '@playwright/test'
import { mockAdminApi } from './mock'

// System -> Authentication, SAML section, in a real browser at phone width. The
// SAML form is always on the page (Save SAML config is what turns SAML on); the
// switch is a separate "Require SAML for staff and users" saved by the page
// Save. The two values for the identity provider sit on the page with the
// switch off, a long URL must not push the page sideways, and a warning the
// server returns for a save shows.

test.use({ viewport: { width: 320, height: 844 } })

const WARNING =
  'Password sign-in is limited to administrators, but SAML is not loaded: staff and users cannot sign in.'

test('the SAML form and the IdP values are shown with the switch off, without sideways scroll', async ({ page }) => {
  await mockAdminApi(page)
  await page.goto('/admin/settings/authentication')

  const toggle = page.getByRole('switch', { name: 'Require SAML for staff and users' })
  await expect(toggle).toHaveAttribute('aria-checked', 'false')
  await expect(page.getByRole('button', { name: 'Save SAML config' })).toBeVisible()
  await expect(page.getByText('Entity ID', { exact: true })).toBeVisible()
  await expect(page.getByText('ACS URL', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Upload .pem / .key' })).toBeVisible()

  const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth)
  expect(scrollWidth, 'the page scrolls sideways at 320px').toBeLessThanOrEqual(320)
})

test('Save sends saml_enabled and shows the warning the server returns', async ({ page }) => {
  await mockAdminApi(page)
  let body: unknown
  await page.route('**/api/v1/admin/settings', async (route) => {
    if (route.request().method() !== 'PATCH') return route.fallback()
    body = route.request().postDataJSON()
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ warning: WARNING }) })
  })
  await page.goto('/admin/settings/authentication')

  await page.getByRole('switch', { name: 'Require SAML for staff and users' }).click()
  await page.getByRole('button', { name: 'Save changes' }).last().click()

  await expect(page.getByText(WARNING)).toBeVisible()
  expect(body).toEqual({ saml_enabled: true })
})
