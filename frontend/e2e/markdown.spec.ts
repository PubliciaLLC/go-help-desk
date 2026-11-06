import { test, expect, type Page } from '@playwright/test'
import { mockApi, MARKDOWN_DETAIL, MARKDOWN_REPLIES, REPLIES, TICKET_DETAIL, ME_REPORTER } from './mock'

// #443: Markdown in ticket text, in a real browser. jsdom cannot compute
// layout and cannot fetch an image, so these are the two things it can't say:
// the page never scrolls sideways, and a paste never makes the browser request
// anything.

async function mainFits(page: Page) {
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')!
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, 'main is wider than its own box').toBeLessThanOrEqual(clientWidth)
}

for (const width of [320, 390]) {
  test.describe(`Markdown text at ${width}px`, () => {
    test.use({ viewport: { width, height: 844 } })

    test('a 300-character link, a wide table and a long code line do not widen the page', async ({ page }) => {
      await mockApi(page, { ticket: MARKDOWN_DETAIL, replies: MARKDOWN_REPLIES })
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      // Both the description and the reply, formatted (a lone first() could pass on one).
      await expect(page.locator('[data-md-body]')).toHaveCount(2)
      await mainFits(page)

      // The table and the code box scroll inside themselves, and can be focused to do it.
      const scrollers = page.locator('[data-md-body] pre, [data-md-body] div[tabindex="0"]')
      expect(await scrollers.count()).toBeGreaterThanOrEqual(2)
      const overflowing = await scrollers.evaluateAll((els) =>
        els.map((e) => ({ scrolls: e.scrollWidth > e.clientWidth, x: getComputedStyle(e).overflowX })),
      )
      expect(overflowing.every((o) => o.x === 'auto')).toBe(true)
      expect(overflowing.some((o) => o.scrolls), 'something should have needed to scroll').toBe(true)

      // The long link wraps rather than widening anything.
      const link = page.locator('[data-md-body] a').first()
      await expect(link).toBeVisible()
    })

    test('a plain record with the same text still wraps (unchanged)', async ({ page }) => {
      await mockApi(page)
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      await expect(page.getByText('Vendor thread:')).toBeVisible()
      await mainFits(page)
    })

    test('the editor and its toolbar fit on the new ticket form', async ({ page }) => {
      await mockApi(page)
      await page.goto('/tickets/new')
      await expect(page.getByRole('toolbar', { name: 'Formatting' })).toBeVisible()
      await mainFits(page)
      const tb = await page.getByRole('toolbar', { name: 'Formatting' }).boundingBox()
      expect(tb!.x + tb!.width).toBeLessThanOrEqual(width)
      // Eight buttons wrapped over more than one row at 320, never cut off.
      for (const b of await page.getByRole('toolbar').getByRole('button').all()) {
        const box = await b.boundingBox()
        expect(box!.x + box!.width).toBeLessThanOrEqual(width)
      }
    })
  })
}

test.describe('the editor in a real browser', () => {
  test.use({ viewport: { width: 1000, height: 800 } })

  // Nothing pasted may make the browser request anything. An HTML paste of an
  // <img> is parsed in a detached document; a plain-text paste of Markdown is
  // parsed and then serialised into the LIVE document, where a stock image
  // node would become an <img> and be fetched. Both must stay silent.
  test('pasting an image or a script makes no network request and saves neither', async ({ page }) => {
    await mockApi(page)
    const requested: string[] = []
    await page.route(/t\.example/, (route) => {
      requested.push(route.request().url())
      return route.abort()
    })
    page.on('request', (r) => {
      if (/t\.example/.test(r.url())) requested.push(r.url())
    })
    await page.goto('/tickets/new')
    const editor = page.locator('.md-editor [contenteditable="true"]')
    await expect(editor).toBeVisible()
    await editor.click()

    const paste = (data: Record<string, string>) =>
      page.evaluate((d) => {
        const dt = new DataTransfer()
        for (const [k, v] of Object.entries(d)) dt.setData(k, v)
        document
          .querySelector('.md-editor [contenteditable="true"]')!
          .dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
      }, data)

    await paste({ 'text/html': '<p>before</p><img src="//t.example/html.gif"><script>window.__x=1</script><p>after</p>', 'text/plain': 'before after' })
    await paste({ 'text/plain': 'see ![tracker](https://t.example/plain.gif) here' })
    await page.waitForTimeout(500)

    // The paste did happen (the text is there), so the silence below means something.
    await expect(editor).toContainText('before')
    await expect(editor).toContainText('tracker')
    expect(await editor.locator('img, script').count()).toBe(0)
    expect(await page.evaluate(() => (window as unknown as { __x?: number }).__x)).toBeUndefined()
    expect(requested, 'the browser asked for something it should not have').toEqual([])

    // What would be saved is the Markdown the editor holds, which the plain
    // editor shows: neither the script nor the pasted <img> is in it.
    await page.getByRole('button', { name: /switch to plain editor/i }).click()
    const saved = await page.getByRole('textbox', { name: 'Description' }).inputValue()
    expect(saved).not.toMatch(/<script|<img|html\.gif|__x/)
    expect(saved).toContain('before')
  })

  test('Tab leaves the editor, from a list item too', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets/new')
    const editor = page.locator('.md-editor [contenteditable="true"]')
    await expect(editor).toBeVisible()
    await editor.click()
    await page.keyboard.type('- one')
    await page.keyboard.press('Enter')
    await page.keyboard.type('two')
    await expect(editor.locator('li')).toHaveCount(2)
    await page.keyboard.press('Tab')
    const stillInside = await page.evaluate(() => !!document.activeElement?.closest('.md-editor'))
    expect(stillInside, 'Tab must move focus out of the editor').toBe(false)
    // The list was not indented by the Tab.
    await expect(editor.locator('li li')).toHaveCount(0)
  })

  test('typed Markdown rules become structure and a literal escape stays literal', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets/new')
    const editor = page.locator('.md-editor [contenteditable="true"]')
    await editor.click()
    await page.keyboard.type('# Title')
    await page.keyboard.press('Enter')
    await page.keyboard.type('**bold** and `code`')
    await expect(editor.locator('h1')).toHaveText('Title')
    await expect(editor.locator('strong')).toHaveText('bold')
  })

  test('a reporter sees the editor too', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await page.goto('/tickets/new')
    await expect(page.getByRole('toolbar', { name: 'Formatting' })).toBeVisible()
  })
})

test('when the formatting worker cannot start, the post is plain and says why', async ({ page }) => {
  // The worker's own script (dev: ?worker_file; build: assets/markdown.worker-*.js).
  await page.route(/markdown\.worker\.ts\?worker_file|assets\/markdown\.worker-/, (route) => route.abort())
  await mockApi(page, { replies: [{ ...REPLIES[0], body: 'a **bold** word', body_format: 'markdown' }] })
  await page.goto(`/tickets/${TICKET_DETAIL.id}`)
  await expect(page.getByText('Shown as plain text: formatting unavailable.').first()).toBeVisible({ timeout: 8000 })
  await expect(page.getByRole('button', { name: 'Plain text' }).first()).toBeVisible()
  await expect(page.locator('[data-md-body] strong')).toHaveCount(0)
})

// On a phone a tap leaves :hover on the button it touched, and a link just
// inserted used to leave the caret inside it. Either made a toolbar button look
// pressed when nothing was selected (seen on an iPhone).
test.describe('the toolbar on a touch screen', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test('Link with nothing selected inserts the address after a space, and leaves no button shaded', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets/new')
    const editor = page.locator('.md-editor [contenteditable="true"]')
    await expect(editor).toBeVisible()
    await editor.tap()
    await page.keyboard.type('Link')

    const link = page.getByRole('button', { name: 'Link', exact: true })
    await link.tap()
    await page.getByRole('textbox', { name: 'Link address' }).fill('https://www.google.com')
    await page.getByRole('button', { name: 'Add', exact: true }).tap()

    await expect(editor).toContainText('Link https://www.google.com')
    await expect(editor.locator('a')).toHaveText('https://www.google.com')
    await expect(link).toHaveAttribute('aria-pressed', 'false')
    // Nothing is selected and nothing is active, so no button is shaded: not by
    // being active, and not by a hover the touch left behind.
    const shaded = await page.getByRole('toolbar').getByRole('button').evaluateAll((els) =>
      els
        .filter((e) => e.getAttribute('aria-pressed') !== 'true')
        .map((e) => ({ name: e.getAttribute('aria-label'), bg: getComputedStyle(e).backgroundColor }))
        .filter((b) => b.bg !== 'rgba(0, 0, 0, 0)'),
    )
    expect(shaded).toEqual([])
    // The next word typed is not part of the link.
    await page.keyboard.type(' next')
    await expect(editor.locator('a')).toHaveText('https://www.google.com')
  })

  test('tapping every toolbar button leaves only the buttons that are really active shaded', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets/new')
    const editor = page.locator('.md-editor [contenteditable="true"]')
    await editor.tap()
    for (const name of ['Bold', 'Italic', 'Inline code', 'Link', 'Bulleted list', 'Numbered list', 'Quote', 'Code block']) {
      await page.getByRole('button', { name, exact: true }).tap()
    }
    const wrong = await page.getByRole('toolbar').getByRole('button').evaluateAll((els) =>
      els
        .map((e) => ({
          name: e.getAttribute('aria-label'),
          pressed: e.getAttribute('aria-pressed') === 'true',
          shaded: getComputedStyle(e).backgroundColor !== 'rgba(0, 0, 0, 0)',
        }))
        .filter((b) => b.pressed !== b.shaded),
    )
    expect(wrong, 'a button is shaded without being pressed, or pressed without being shaded').toEqual([])
  })
})
