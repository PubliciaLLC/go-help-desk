import { test, expect, type Page } from '@playwright/test'
import { ME_ADMIN, STATUSES, CATEGORIES, TICKETS } from './mock'

// #170 / the 1.3.0 post-upgrade wizard at the narrowest phone we support
// (320 CSS pixels): every step reachable with the keyboard, nothing wider than
// the screen, and finishing records it as seen so a reload goes straight on to
// the dashboard.
test.use({ viewport: { width: 320, height: 640 } })

// A stateful stand-in for the server: the settings an install upgraded from 1.2
// has (staff email forced off by migration 000052, no post_upgrade_130_seen),
// and every PATCH it receives.
async function mockUpgradedInstall(page: Page, extra: Record<string, unknown> = {}) {
  const settings: Record<string, unknown> = {
    ...extra,
    staff_email_assigned_override: 'off',
    staff_email_activity_override: 'off',
    staff_email_group_override: 'off',
  }
  const patches: Record<string, unknown>[] = []

  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (path === '/api/v1/admin/settings') {
      if (req.method() === 'PATCH') {
        const body = req.postDataJSON() as Record<string, unknown>
        patches.push(body)
        Object.assign(settings, body)
        await route.fulfill({ status: 204 })
      } else {
        await route.fulfill({ json: settings })
      }
      return
    }
    const fixed: Record<string, unknown> = {
      '/api/v1/me': ME_ADMIN,
      '/api/v1/site': { name: 'Go Help Desk', logo_url: '', version: '1.3.0' },
      '/api/v1/admin/security-warnings': { insecure_secrets: [], email: { enabled: false, missing: ['SMTP_HOST'] } },
      '/api/v1/admin/post-upgrade': { old_password_accounts: 12 },
      '/api/v1/statuses': STATUSES,
      '/api/v1/categories': CATEGORIES,
      '/api/v1/tickets': TICKETS,
    }
    await route.fulfill({ json: fixed[path] ?? [] })
  })
  return patches
}

async function noHorizontalOverflow(page: Page) {
  const { scrollWidth, clientWidth } = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }))
  expect(scrollWidth, 'the page is wider than the 320px screen').toBeLessThanOrEqual(clientWidth)
  // Every control must be fully on screen, not clipped by an overflow-hidden parent.
  const clipped = await page.evaluate(() =>
    [...document.querySelectorAll('button, input, select, a')]
      .filter((el) => {
        const r = el.getBoundingClientRect()
        return r.width > 0 && (r.right > window.innerWidth + 0.5 || r.left < -0.5)
      })
      .map((el) => el.outerHTML.slice(0, 80)),
  )
  expect(clipped).toEqual([])
}

test('an upgraded install is walked through the wizard once, at 320px', async ({ page }) => {
  const patches = await mockUpgradedInstall(page)

  await page.goto('/dashboard')
  await expect(page).toHaveURL(/\/after-upgrade$/)
  await expect(page.getByRole('heading', { level: 1, name: 'After upgrading to 1.3.0' })).toBeVisible()

  // Step 1: the box starts unchecked; tick it by keyboard.
  await expect(page.getByRole('heading', { level: 2 })).toHaveText('Weekly check for new releases')
  const box = page.getByLabel('Check for new releases (contacts GitHub once a week; sends nothing about your server)')
  await expect(box).not.toBeChecked()
  await noHorizontalOverflow(page)
  await box.focus()
  await page.keyboard.press('Space')
  await expect(box).toBeChecked()
  await page.getByRole('button', { name: 'Next' }).click()

  // Step 2: skipped.
  await expect(page.getByRole('heading', { level: 2 })).toHaveText('Staff email notifications')
  await expect(page.getByRole('heading', { level: 2 })).toBeFocused()
  await noHorizontalOverflow(page)
  await page.getByRole('button', { name: 'Skip' }).click()

  // Step 3: the slider is the widest control.
  await expect(page.getByRole('heading', { level: 2 })).toHaveText('Staff API keys')
  await noHorizontalOverflow(page)
  await page.getByRole('button', { name: 'Skip' }).click()

  // Step 4.
  await expect(page.getByRole('heading', { level: 2 })).toHaveText('Reset window for old passwords')
  await expect(page.getByText('12 accounts are still on an old password.')).toBeVisible()
  await expect(page.getByText(/email is not set up/i)).toBeVisible()
  await noHorizontalOverflow(page)
  await page.getByRole('button', { name: 'Finish' }).click()

  await expect(page).toHaveURL(/\/dashboard$/)
  expect(patches).toEqual([
    { update_check_enabled: true },
    { post_upgrade_130_seen: true },
  ])

  // Seen: a reload does not bring it back.
  await page.reload()
  await expect(page).toHaveURL(/\/dashboard$/)
})

test('with guest submission on, the guest uploads step fits at 320px and saves', async ({ page }) => {
  const patches = await mockUpgradedInstall(page, { guest_submission_enabled: true })

  await page.goto('/dashboard')
  await expect(page).toHaveURL(/\/after-upgrade$/)
  await expect(page.getByText('Step 1 of 5')).toBeVisible()
  for (let i = 0; i < 4; i++) await page.getByRole('button', { name: 'Skip' }).click()

  await expect(page.getByRole('heading', { level: 2 })).toHaveText('Guest file uploads')
  await expect(page.getByText('Step 5 of 5')).toBeVisible()
  await noHorizontalOverflow(page)

  const box = page.getByLabel('Guests may attach files')
  await box.focus()
  await page.keyboard.press('Space')
  await expect(box).toBeChecked()
  await page.getByLabel('Uploads per guest link per hour').fill('5')
  await noHorizontalOverflow(page)
  await page.getByRole('button', { name: 'Finish' }).click()

  await expect(page).toHaveURL(/\/dashboard$/)
  expect(patches).toEqual([
    { guest_attachments_enabled: true, guest_attachment_uploads_per_hour: 5, post_upgrade_130_seen: true },
  ])
})

test('with guest submission off there is no guest step, and the wizard says why', async ({ page }) => {
  await mockUpgradedInstall(page, { guest_submission_enabled: false })
  await page.goto('/dashboard')
  await expect(page.getByText('Step 1 of 4')).toBeVisible()
  for (let i = 0; i < 3; i++) await page.getByRole('button', { name: 'Skip' }).click()
  await expect(page.getByText(/guest file uploads stay off/i)).toBeVisible()
  await noHorizontalOverflow(page)
})
