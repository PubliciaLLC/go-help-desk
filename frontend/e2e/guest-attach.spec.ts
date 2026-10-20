import { test, expect } from '@playwright/test'

// #445: a guest attaches a small file from the ticket page at 320 CSS pixels,
// the narrowest viewport DESIGN.md's Small screens section covers, without the
// page growing a horizontal scrollbar.
test.use({ viewport: { width: 320, height: 640 } })

const TOKEN = 'e2e-guest-token'

test('a guest attaches a small file at 320px', async ({ page, browserName }) => {
  let uploaded: { auth: string | undefined; body: string } | null = null

  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (path === '/api/v1/site') {
      await route.fulfill({ json: {
        name: 'Go Help Desk', logo_url: '', version: '1.3.0',
        guest_submission_enabled: true, guest_attachments_enabled: true, guest_attachment_max_bytes: 5 * 1024 * 1024,
      } })
    } else if (path === '/api/v1/guest/ticket') {
      await route.fulfill({ json: {
        tracking_number: 'GHD-2026-000042', subject: 'Printer jammed', description: 'again',
        status: 'In Progress', created_at: '2026-09-20T10:00:00Z', replies: [],
      } })
    } else if (path === '/api/v1/guest/attachments' && req.method() === 'POST') {
      uploaded = { auth: req.headers()['authorization'], body: req.postData() ?? '' }
      await route.fulfill({ status: 201, json: { id: 'a1', filename: 'printer.txt' } })
    } else {
      await route.fulfill({ json: [] })
    }
  })

  await page.goto(`/g#${TOKEN}`)
  await expect(page.getByRole('heading', { name: 'Printer jammed' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Add files' })).toBeVisible()

  await page.locator('input[type=file]').setInputFiles({
    name: 'printer.txt', mimeType: 'text/plain', buffer: Buffer.from('paper jam log'),
  })
  await expect(page.getByRole('status')).toContainText('printer.txt')

  expect(uploaded).not.toBeNull()
  expect(uploaded!.auth).toBe(`Guest ${TOKEN}`)
  // Linux WebKit hands an intercepted multipart request to postData() without
  // the file's bytes (seen on CI; macOS WebKit and Chromium include them). The
  // upload happening, with the guest's token, is still asserted everywhere.
  if (browserName !== 'webkit') expect(uploaded!.body).toContain('paper jam log')

  const { scrollWidth, clientWidth } = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth,
  }))
  expect(scrollWidth, 'the page is wider than 320px').toBeLessThanOrEqual(clientWidth)
})
