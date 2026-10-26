import { test, expect, type Page } from '@playwright/test'
import { mockAdminApi } from './mock'

// #158: the email template editor at 320 CSS pixels, in both engines (the
// chromium and webkit projects run every spec). One column, buttons that wrap
// rather than run off the screen, the preview below the editor, and the live
// check working in a real browser: an invalid draft shows its error beside the
// field and Save stays disabled.

test.use({ viewport: { width: 320, height: 844 } })

const LIST = [
  { name: 'ticket_created', label: 'Ticket received', group: 'requester', shell: false, customised: false, broken: false, updated_at: null, updated_by_name: '' },
  { name: 'password_reset', label: 'Reset your password', group: 'account', shell: false, customised: true, broken: true, updated_at: '2026-10-25T10:00:00Z', updated_by_name: 'Ada Admin' },
  { name: '_header', label: 'Header', group: 'shared', shell: true, customised: false, broken: false, updated_at: null, updated_by_name: '' },
]

const DETAIL = {
  ...LIST[0],
  customised: true,
  subject: 'We have received {{if .TrackingNumber}}[{{.TrackingNumber}}]{{else}}your ticket{{end}}',
  body: 'We have received your ticket.\n\nOpen it:\n{{.TicketURL}}\n',
  default_subject: 'We have received {{if .TrackingNumber}}[{{.TrackingNumber}}]{{else}}your ticket{{end}}',
  default_body: 'We have received your ticket.\n\nOpen it:\n{{.TicketURL}}\n',
  required: 'TicketURL',
  variables: [
    { name: 'TrackingNumber', description: 'The ticket tracking number, such as GHD-2026-000042. It can be empty.' },
    { name: 'TicketURL', description: 'The link to the ticket. For a guest it is their own link, and it replaces any earlier one.' },
  ],
  max_body_bytes: 8192,
  max_subject_bytes: 200,
}

const LONG = 'L'.repeat(300)

// previewHTML, when set, is what the preview endpoint returns as the HTML part
// whatever the draft holds; a hostile string here proves the frame, not the server.
async function mockTemplates(page: Page, opts: { previewHTML?: string } = {}) {
  await mockAdminApi(page, { short: true })
  const saved: unknown[] = []
  await page.route('**/api/v1/admin/email-templates**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (path.endsWith('/preview')) {
      const draft = req.postDataJSON() as { subject: string; body: string; html?: string }
      // The same rule the server applies, enough for the layout: no link, no go.
      if (!draft.body.includes('{{.TicketURL}}')) {
        await route.fulfill({
          json: {
            valid: false,
            errors: [{ field: 'body', line: 1, col: 1, message: `must include {{.TicketURL}} outside any {{if}}; ${LONG}` }],
          },
        })
        return
      }
      await route.fulfill({
        json: {
          valid: true, errors: [], subject: 'We have received [GHD-2026-000042]', body: `Sample ${draft.body}`,
          ...(draft.html || opts.previewHTML ? { html: opts.previewHTML ?? draft.html } : {}),
        },
      })
      return
    }
    if (req.method() === 'PUT') {
      saved.push(req.postDataJSON())
      await route.fulfill({ json: { ...DETAIL, body: (req.postDataJSON() as { body: string }).body } })
      return
    }
    if (path.endsWith('/email-templates')) {
      await route.fulfill({ json: LIST })
      return
    }
    await route.fulfill({ json: DETAIL })
  })
  return saved
}

async function fits(page: Page) {
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, 'main is wider than its own box at 320px').toBeLessThanOrEqual(clientWidth)
}

test('the list fits at 320px', async ({ page }) => {
  await mockTemplates(page)
  await page.goto('/admin/email-templates')
  await expect(page.getByRole('heading', { level: 1, name: 'Email templates' })).toBeVisible()
  await expect(page.getByRole('button', { name: /Reset your password/ })).toBeVisible()
  await expect(page.getByText('Broken')).toBeVisible()
  await fits(page)
})

test('the editor is one column, its buttons wrap, and the preview sits below it', async ({ page }) => {
  await mockTemplates(page)
  await page.goto('/admin/email-templates?t=ticket_created')
  const body = page.getByLabel('Body')
  await expect(body).toBeVisible()
  await fits(page)

  for (const name of ['Save', 'Send test', 'Revert to default']) {
    const box = await page.getByRole('button', { name }).boundingBox()
    expect(box, name).not.toBeNull()
    expect(box!.x + box!.width, `${name} runs off the screen`).toBeLessThanOrEqual(320)
    expect(box!.x).toBeGreaterThanOrEqual(0)
  }
  // Wrapped, not squeezed side by side: at 320px three buttons cannot share a row.
  const save = await page.getByRole('button', { name: 'Save' }).boundingBox()
  const revert = await page.getByRole('button', { name: 'Revert to default' }).boundingBox()
  expect(revert!.y, 'the buttons did not wrap').toBeGreaterThan(save!.y)

  await expect(page.getByText(/Sample We have received your ticket/)).toBeVisible()
  const bodyBox = await body.boundingBox()
  const preview = await page.getByText('Preview', { exact: true }).boundingBox()
  expect(preview!.y, 'the preview is not below the editor').toBeGreaterThan(bodyBox!.y + bodyBox!.height)
  await fits(page)
})

test('an invalid draft shows its error beside the field and cannot be saved', async ({ page }) => {
  const saved = await mockTemplates(page)
  await page.goto('/admin/email-templates?t=ticket_created')
  const body = page.getByLabel('Body')
  const save = page.getByRole('button', { name: 'Save' })
  await expect(body).toBeVisible()

  await body.fill('No link in this one.')
  await expect(save).toBeDisabled() // not checked yet, or checked and invalid: either way
  const error = page.getByText(/Line 1, column 1/)
  await expect(error).toBeVisible()
  await expect(body).toHaveAttribute('aria-invalid', 'true')
  await expect(save).toBeDisabled()
  // The error is beside the field and a long word in it wraps instead of widening the page.
  await fits(page)
  const errBox = await error.boundingBox()
  expect(errBox!.x + errBox!.width).toBeLessThanOrEqual(320)

  await save.click({ force: true })
  expect(saved, 'an invalid draft reached the server').toHaveLength(0)

  await body.fill('Fixed. {{.TicketURL}}')
  await expect(save).toBeEnabled()
  await expect(body).not.toHaveAttribute('aria-invalid', 'true')
  await save.click()
  await expect.poll(() => saved.length).toBe(1)
  await fits(page)
})

test('a 300 character word in the draft does not widen the page', async ({ page }) => {
  await mockTemplates(page)
  await page.goto('/admin/email-templates?t=ticket_created')
  const body = page.getByLabel('Body')
  await body.fill(`{{.TicketURL}} ${LONG}`)
  await expect(page.getByText(/Sample/)).toBeVisible()
  await fits(page)
})

// The HTML preview (#158, PR 2): markup an administrator wrote, shown on the
// origin staff sessions live on. It must sit in a frame with an EMPTY sandbox,
// and a script or handler inside it must not run. A frame cannot be inspected
// from outside once it is sandboxed, so the test listens for postMessage, the
// one thing a sandboxed script CAN still do: a message arriving means it ran.
const HOSTILE =
  '<p id="visible">frame content</p><script>parent.postMessage("script-ran", "*")</script>' +
  '<img src="x" onerror="parent.postMessage(\'handler-ran\', \'*\')">'

test('the HTML preview is sandboxed and a script inside it does not run', async ({ page }) => {
  await page.addInitScript(() => {
    ;(window as unknown as { __messages: string[] }).__messages = []
    window.addEventListener('message', (e) => (window as unknown as { __messages: string[] }).__messages.push(String(e.data)))
  })
  await mockTemplates(page, { previewHTML: HOSTILE })
  await page.goto('/admin/email-templates?t=ticket_created')
  await page.getByLabel('HTML').fill('<p>draft</p>')
  const frame = page.locator('iframe[title="HTML preview"]')
  await expect(frame).toBeVisible()

  // The attributes.
  await expect(frame).toHaveAttribute('sandbox', '')
  const srcdoc = (await frame.getAttribute('srcdoc')) ?? ''
  expect(srcdoc.startsWith(`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:">`)).toBe(true)
  expect(await frame.getAttribute('sandbox')).not.toMatch(/allow-/)

  // The content shows, and nothing in it ran.
  await expect(page.frameLocator('iframe[title="HTML preview"]').locator('#visible')).toHaveText('frame content')
  await page.waitForTimeout(800)
  expect(await page.evaluate(() => (window as unknown as { __messages: string[] }).__messages)).toEqual([])

  // Control: the same listener does hear a frame that is allowed to run scripts,
  // so the silence above is the sandbox and not a broken probe.
  await page.evaluate(() => {
    const f = document.createElement('iframe')
    f.setAttribute('sandbox', 'allow-scripts')
    f.srcdoc = '<script>parent.postMessage("control", "*")</script>'
    document.body.appendChild(f)
  })
  await expect.poll(() => page.evaluate(() => (window as unknown as { __messages: string[] }).__messages)).toContain('control')

  // Nothing of it was written into the page itself.
  expect(await page.locator('#visible').count()).toBe(0)
  await fits(page)
})

test('the HTML preview fits at 320px', async ({ page }) => {
  await mockTemplates(page, { previewHTML: '<table width="900"><tr><td>wide</td></tr></table>' })
  await page.goto('/admin/email-templates?t=ticket_created')
  await page.getByLabel('HTML').fill('<p>draft</p>')
  const frame = page.locator('iframe[title="HTML preview"]')
  await expect(frame).toBeVisible()
  const box = await frame.boundingBox()
  expect(box!.x + box!.width).toBeLessThanOrEqual(320)
  await fits(page)
})

// Touch targets: 44 px on a touch screen (a coarse pointer), on this page only.
test.describe('touch targets', () => {
  test.use({ hasTouch: true, isMobile: true })

  for (const width of [320, 375]) {
    test(`every control is at least 44px tall at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await mockTemplates(page)
      await page.goto('/admin/email-templates?t=ticket_created')
      await expect(page.getByLabel('Body')).toBeVisible()
      // Send test and Save are disabled until the first check lands; they still have a size.
      const targets = [
        page.getByRole('button', { name: 'All templates' }),
        page.getByRole('button', { name: 'Save' }),
        page.getByRole('button', { name: 'Send test' }),
        page.getByRole('button', { name: 'Revert to default' }),
        page.getByRole('button', { name: 'Start from a basic layout' }),
        page.getByRole('button', { name: '{{.TicketURL}}' }),
        page.getByRole('button', { name: '{{.TrackingNumber}}' }),
      ]
      for (const t of targets) {
        const box = await t.boundingBox()
        expect(box, 'not found').not.toBeNull()
        expect(box!.height, `${await t.innerText()} is shorter than 44px`).toBeGreaterThanOrEqual(44)
      }
    })
  }
})
