import { test, expect } from '@playwright/test'
import { mockAdminApi } from '../e2e/mock'

// #158 PR 2: the HTML preview under the backend's REAL Content-Security-Policy
// (default-src 'self' gives frame-src 'self'). Two things only a browser under
// that policy can tell us: that a srcdoc frame is not blocked by it, and that
// the frame still runs nothing when the policy and the sandbox are both there.

const DETAIL = {
  name: 'ticket_created', label: 'Ticket received', group: 'requester', shell: false, customised: true, broken: false,
  updated_at: null, updated_by_name: '', subject: 'Hi', body: '{{.TicketURL}}', default_subject: 'Hi', default_body: '{{.TicketURL}}',
  required: 'TicketURL', variables: [{ name: 'TicketURL', description: 'The link.' }], max_body_bytes: 8192, max_subject_bytes: 200,
  html: '<p>x</p>', max_html_bytes: 65536,
}
const HOSTILE =
  '<p id="visible">frame content</p><script>parent.postMessage("script-ran", "*")</script>' +
  '<img src="x" onerror="parent.postMessage(\'handler-ran\', \'*\')">'

test('the sandboxed preview is not blocked by the page policy, and runs nothing', async ({ page }) => {
  const problems: string[] = []
  page.on('console', (m) => {
    if (/frame-src|child-src|Refused to (frame|display)/i.test(m.text())) problems.push(m.text())
  })
  await page.addInitScript(() => {
    ;(window as unknown as { __m: string[] }).__m = []
    window.addEventListener('message', (e) => (window as unknown as { __m: string[] }).__m.push(String(e.data)))
  })
  await mockAdminApi(page, { short: true })
  await page.route('**/api/v1/admin/email-templates**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/preview')) {
      await route.fulfill({ json: { valid: true, errors: [], subject: 'Hi', body: 'x', html: HOSTILE } })
    } else {
      await route.fulfill({ json: DETAIL })
    }
  })
  await page.goto('/admin/email-templates?t=ticket_created')

  const frame = page.locator('iframe[title="HTML preview"]')
  await expect(frame).toHaveAttribute('sandbox', '')
  await expect(page.frameLocator('iframe[title="HTML preview"]').locator('#visible')).toHaveText('frame content')
  await page.waitForTimeout(800)
  expect(await page.evaluate(() => (window as unknown as { __m: string[] }).__m)).toEqual([])
  expect(problems).toEqual([])
})
