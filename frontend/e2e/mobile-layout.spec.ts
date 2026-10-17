import { test, expect, type Locator, type Page } from '@playwright/test'
import {
  mockApi,
  DESCRIPTION_PROSE,
  DESCRIPTION_URL,
  ME_ADMIN,
  ME_REPORTER,
  ME_STAFF,
  ATTACHMENTS,
  CUSTOM_FIELDS,
  LINKED_TICKET,
  REPLIES,
  TAGS,
  REPLY_PROSE,
  REPLY_URL,
  TICKET_DETAIL,
  TICKETS,
} from './mock'

// #296: the shell has a fixed 240px sidebar with no breakpoint, so every
// page inside it is unusable — not overflowing, just crushed — at a phone
// viewport. DESIGN.md's Small screens section states the rule this pins:
// no horizontal scrolling at 390 CSS pixels, and tap targets large enough
// to hit with a thumb, on the four in-scope pages (Dashboard, TicketList,
// TicketDetail, NewTicket). Admin pages are explicitly out of scope.
//
// iPhone 13: 390x844 CSS pixels. This is the exact viewport DESIGN.md and
// issue #296 measured against.
test.use({ viewport: { width: 390, height: 844 } })

async function noHorizontalScroll(page: Page) {
  // NOT document.documentElement: every page's content sits inside
  // <main class="overflow-auto"> in a shell whose row is overflow-hidden, so
  // the document itself can never be wider than the viewport regardless of
  // what overflows inside main — exactly the shape of check #296 warned
  // against ("scrollWidth == innerWidth ... passed this and did"). Measuring
  // main's own box is what actually catches a too-wide child of it.
  const { scrollWidth, clientWidth } = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return { scrollWidth: main.scrollWidth, clientWidth: main.clientWidth }
  })
  expect(scrollWidth, 'main is wider than its own box at 390px').toBeLessThanOrEqual(clientWidth)
}

test.describe('shell + pages fit at 390 CSS pixels', () => {
  test('dashboard', async ({ page }) => {
    await mockApi(page)
    await page.goto('/dashboard')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('ticket list (staff)', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets')
    // The desktop table and the mobile card both exist in the DOM (CSS
    // picks one), so a bare text query is ambiguous — scoped to the card,
    // which is the thing this page is actually testing.
    await expect(page.locator('li').filter({ hasText: 'Printer on fire, second floor' })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('ticket list (reporter)', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await page.goto('/tickets')
    await expect(page.locator('li').filter({ hasText: 'Printer on fire, second floor' })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('ticket detail', async ({ page }) => {
    await mockApi(page)
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    await expect(page.getByText(TICKET_DETAIL.subject)).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('new ticket (staff)', async ({ page }) => {
    await mockApi(page)
    await page.goto('/tickets/new')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await noHorizontalScroll(page)
  })

  test('new ticket (reporter)', async ({ page }) => {
    await mockApi(page, { me: ME_REPORTER })
    await page.goto('/tickets/new')
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await noHorizontalScroll(page)
  })
})

test.describe('the sidebar is reachable, not just absent', () => {
  test('a drawer trigger opens the navigation, and it closes again', async ({ page }) => {
    await mockApi(page)
    await page.goto('/dashboard')

    // The desktop sidebar's own nav must not be sitting in the page,
    // consuming width, at this viewport.
    const nav = page.getByRole('navigation')
    await expect(nav).toBeHidden()

    const trigger = page.getByRole('button', { name: /menu|navigation/i })
    await expect(trigger).toBeVisible()
    const box = await trigger.boundingBox()
    expect(box?.width ?? 0, 'drawer trigger is smaller than a 24px tap target').toBeGreaterThanOrEqual(24)
    expect(box?.height ?? 0, 'drawer trigger is smaller than a 24px tap target').toBeGreaterThanOrEqual(24)

    // Scoped to the nav landmark, not the whole page: DashboardPage has its
    // own cards linking to /tickets, so an unscoped name query is ambiguous.
    await trigger.click()
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()

    await page.keyboard.press('Escape')
    await expect(page.getByRole('navigation')).toBeHidden()
  })

  test('resizing past md closes an open drawer instead of leaving it trapped behind the sidebar', async ({ page }) => {
    await mockApi(page)
    await page.goto('/dashboard')

    await page.getByRole('button', { name: /menu|navigation/i }).click()
    await expect(page.getByRole('navigation').getByRole('link', { name: 'Tickets', exact: true })).toBeVisible()

    // Widening past md (768px) is what a phone rotating to landscape, or a
    // window being resized, looks like. Without a listener for this, the
    // drawer stays open and its focus trap and overlay keep covering the now
    // otherwise-usable desktop layout underneath.
    await page.setViewportSize({ width: 1024, height: 800 })
    await expect(page.getByRole('navigation')).toBeVisible()

    // Back below md: the drawer must not have silently reopened — the
    // permanent sidebar is what took its place, and only its own trigger
    // reopens the drawer again.
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(page.getByRole('navigation')).toBeHidden()
  })
})

test.describe('bulk ticket selection stays desktop-only', () => {
  test('a selection made at desktop width does not survive into the mobile card view', async ({ page }) => {
    await mockApi(page)
    // Wide first: bulk selection is a table feature, not reachable from the
    // card view at all, so the selection itself has to be made at desktop
    // width before resizing down to prove anything about what happens next.
    await page.setViewportSize({ width: 1024, height: 800 })
    await page.goto('/tickets')

    await page
      .getByRole('checkbox', { name: `Select ticket ${TICKETS[0].tracking_number}` })
      .check()
    await expect(page.getByText('1 selected')).toBeVisible()

    // Narrow to the phone viewport this whole file otherwise runs at,
    // without reloading — a resize, not a fresh navigation, since a stale
    // selection surviving a page load was never the failure mode here.
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(page.getByText('1 selected')).toBeHidden()
    await noHorizontalScroll(page)
  })
})

test.describe('tap targets are large enough to hit with a thumb', () => {
  // mock.ts populates this ticket with an assignee, a tag, a linked ticket, a
  // custom field and an attachment pair (one clean, one quarantined with two
  // reputation providers) specifically so this scan reaches the controls
  // those panels only render once populated — AssigneePanel's "Clear
  // assignment", CustomFieldsPanel's "Edit", TagInput's "Remove tag",
  // LinkedTicketsPanel's link anchor and "Remove link", and AttachmentList's
  // "Look up on VirusTotal", "Show all N services" and "Open … report".
  // Against the empty-array fixture this test used before, none of those
  // elements exist to measure, so the pass they gate never actually ran on
  // them.
  test('ticket detail: every control, once every panel actually renders', async ({ page }) => {
    await mockApi(page)
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    await expect(page.getByText(TICKET_DETAIL.subject)).toBeVisible()

    const targets = await page.evaluate(() => {
      const els = Array.from(document.querySelectorAll('button, a[href], input[type="checkbox"], [role="button"]'))
      return els
        .map((el) => {
          // A checkbox wrapped in a <label> with its own text is reachable
          // by tapping that whole label, natively, with no JS behind it — the
          // effective target is the label's box, not the 16px checkbox alone.
          const target = el.closest('label') ?? el
          const r = target.getBoundingClientRect()
          return { text: (el.textContent || el.getAttribute('aria-label') || '').trim().slice(0, 40), w: r.width, h: r.height }
        })
        .filter((t) => t.w > 0 && t.h > 0)
    })

    const tooSmall = targets.filter((t) => t.w < 24 || t.h < 24)
    expect(
      tooSmall,
      `controls under 24x24px: ${JSON.stringify(tooSmall)}`,
    ).toEqual([])
  })
})

// The overflow check above only asks whether something is wider than <main>.
// The two helpers below ask how text is actually broken into lines, which is
// what #414 and #426 are about: nothing overflowed, it just broke in the
// wrong place.

// How many lines the element's text is drawn on. A Range over the text, not
// the element's own box: a flex item is a block, so its box is one rect no
// matter how many lines its text wraps onto.
async function lineCount(locator: Locator): Promise<number> {
  return locator.evaluate((el) => {
    const range = document.createRange()
    range.selectNodeContents(el)
    return new Set(Array.from(range.getClientRects(), (r) => Math.round(r.top))).size
  })
}

// Every whitespace-separated token in the element's text that is drawn across
// more than one line, i.e. was broken in the middle.
async function tokensBrokenAcrossLines(locator: Locator): Promise<string[]> {
  return locator.evaluate((el) => {
    const broken: string[] = []
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      for (const m of (node.textContent ?? '').matchAll(/\S+/g)) {
        const range = document.createRange()
        range.setStart(node, m.index)
        range.setEnd(node, m.index + m[0].length)
        const tops = new Set(Array.from(range.getClientRects(), (r) => Math.round(r.top)))
        if (tops.size > 1) broken.push(m[0])
      }
    }
    return broken
  })
}

test.describe('#414: the ticket list toolbar stacks instead of squeezing the search box', () => {
  const cases = [
    { name: 'reporter', me: ME_REPORTER, below: ['Include closed'] },
    { name: 'staff', me: ME_STAFF, below: ['Include closed', 'Jump to ticket'] },
    { name: 'admin', me: ME_ADMIN, below: ['mine', 'Include closed', 'Jump to ticket'] },
  ]
  for (const c of cases) {
    test(`${c.name}: search has its own row, the other controls wrap below it`, async ({ page }) => {
      await mockApi(page, { me: c.me })
      await page.goto('/tickets')
      const search = await page.getByPlaceholder(/^Search by tracking number/).boundingBox()
      if (!search) throw new Error('search input not rendered')
      // main's content box is 358px at 390 (p-4 either side). 300px leaves
      // the icon and the start of the placeholder readable; before #414 the
      // box was squeezed to show "Sea…".
      expect(search.width, 'search input squeezed below a usable width').toBeGreaterThanOrEqual(300)
      for (const name of c.below) {
        const box = await page.getByText(name, { exact: true }).boundingBox()
        expect(box?.y ?? -1, `"${name}" is not on a row below the search box`).toBeGreaterThanOrEqual(
          search.y + search.height,
        )
      }
      await noHorizontalScroll(page)
    })
  }

  // 768 and 1024 are where the row is tight (the sidebar takes 240px): the
  // search box must keep shrinking to fit beside the others, not take a row
  // of its own. At 1280 there is room to spare, so it grows into the free
  // space and stops at max-w-lg (32rem), as it did before #414.
  for (const width of [768, 1024, 1280]) {
    test(`from md up the toolbar is still one row (unchanged): ${width}px`, async ({ page }) => {
      await mockApi(page)
      await page.setViewportSize({ width, height: 800 })
      await page.goto('/tickets')
      const search = await page.getByPlaceholder(/^Search by tracking number/).boundingBox()
      const jump = await page.getByRole('button', { name: 'Jump to ticket' }).boundingBox()
      if (!search || !jump) throw new Error('toolbar not rendered')
      expect(Math.abs(search.y + search.height / 2 - (jump.y + jump.height / 2))).toBeLessThanOrEqual(2)
      if (width === 1280) {
        expect(search.width, 'search box no longer fills the row up to max-w-lg').toBeCloseTo(512, 0)
      }
    })
  }
})

test.describe('#414: the tracking number never breaks inside itself', () => {
  test('ticket detail header', async ({ page }) => {
    await mockApi(page)
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    const number = page.getByText(TICKET_DETAIL.tracking_number, { exact: true })
    await expect(number).toBeVisible()
    expect(await lineCount(number), `${TICKET_DETAIL.tracking_number} wraps onto more than one line`).toBe(1)
    await noHorizontalScroll(page)
  })
})

// #480: the header's action buttons sat beside the title in a flex row, so a
// group like Reopen + Create follow-up took ~250px and left the title and
// tracking number a column of 123px at 375px and less below. Below md the
// group now wraps onto its own row under the title block, and the title block
// gets the full width.
test.describe('#480: header actions get their own row below md', () => {
  for (const width of [320, 360, 375, 414, 767]) {
    test(`staff, closed ticket, can_reopen: ${width}px`, async ({ page }) => {
      await mockApi(page)
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}`, (route) =>
        route.fulfill({ json: { ...TICKET_DETAIL, status_id: 'st-closed', can_reopen: true } }),
      )
      await page.setViewportSize({ width, height: 844 })
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      const number = page.getByText(TICKET_DETAIL.tracking_number, { exact: true })
      const title = page.getByRole('heading', { level: 1 })
      const buttons = [page.getByRole('button', { name: 'Reopen' }), page.getByRole('button', { name: 'Create follow-up' })]
      await expect(number).toBeVisible()
      for (const b of buttons) await expect(b).toBeVisible()

      const n = await number.boundingBox()
      const t = await title.boundingBox()
      if (!n || !t) throw new Error('header not rendered')
      const boxes = []
      for (const b of buttons) {
        const box = await b.boundingBox()
        if (!box) throw new Error('button not rendered')
        boxes.push(box)
      }

      // Full width: main has p-4 either side and the sidebar is a drawer.
      expect(t.width, 'the title does not get the full width').toBeGreaterThanOrEqual(width - 32 - 1)
      expect(await lineCount(number), 'the tracking number wraps').toBe(1)
      for (const box of boxes) {
        // Own row: entirely below both the number and the title.
        expect(box.y, 'a button is not on a row below the number').toBeGreaterThanOrEqual(n.y + n.height)
        expect(box.y, 'a button is not on a row below the title').toBeGreaterThanOrEqual(t.y + t.height)
        // And the belt to that braces: no box intersects the number's.
        const overlaps = n.x < box.x + box.width && box.x < n.x + n.width && n.y < box.y + box.height && box.y < n.y + n.height
        expect(overlaps, 'the tracking number runs under a button').toBe(false)
      }
      await noHorizontalScroll(page)
    })
  }

  // Unchanged from md up: the group still sits beside the title, at the right.
  test('from md up the actions still sit beside the title (unchanged): 1024px', async ({ page }) => {
    await mockApi(page)
    await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}`, (route) =>
      route.fulfill({ json: { ...TICKET_DETAIL, status_id: 'st-closed', can_reopen: true } }),
    )
    await page.setViewportSize({ width: 1024, height: 800 })
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    const t = await page.getByRole('heading', { level: 1 }).boundingBox()
    const r = await page.getByRole('button', { name: 'Reopen' }).boundingBox()
    if (!t || !r) throw new Error('header not rendered')
    expect(r.x, 'actions no longer beside the title at desktop width').toBeGreaterThanOrEqual(t.x + t.width)
    expect(r.y).toBeLessThan(t.y + t.height)
  })
})

test.describe('#414: "Opened …" moves below the number instead of being crushed beside it', () => {
  // Staff on a New ticket: "Mark Resolved" sits in the header, so the meta
  // row has the number, "·" and "Opened <date>" to fit into what is left.
  // A row that does not wrap squeezes "Opened <date>" into a column of one
  // word per line beside the number. Two lines is the most it should take: at
  // 390 the column can be a few pixels short of the whole phrase.
  for (const width of [390, 320]) {
    test(`${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await mockApi(page)
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      const number = page.getByText(TICKET_DETAIL.tracking_number, { exact: true })
      const opened = page.getByText(/^Opened \d/)
      await expect(opened).toBeVisible()
      await expect(page.getByRole('button', { name: 'Mark Resolved' })).toBeVisible()

      const lines = await lineCount(opened)
      expect(lines, 'no line boxes measured').toBeGreaterThan(0)
      expect(lines, '"Opened …" crushed into a narrow column beside the number').toBeLessThanOrEqual(2)
      // When it wraps onto the next row it follows straight on, with no
      // extra row gap (gap-x-2, not gap-2).
      const n = await number.boundingBox()
      const o = await opened.boundingBox()
      if (!n || !o) throw new Error('meta row not rendered')
      expect(o.y - (n.y + n.height), 'a row gap opened up between the wrapped lines').toBeLessThan(4)
      await noHorizontalScroll(page)
    })
  }
})

test.describe('#426: prose wraps at word boundaries; an unbroken URL still wraps', () => {
  // Prose and URL are located separately, not as one <p>: today each body is
  // a single whitespace-pre-wrap paragraph, but #443 (Markdown) will render
  // them as several elements, and this should keep holding when it does.
  const cases = [
    { name: 'description', prose: DESCRIPTION_PROSE, url: DESCRIPTION_URL },
    { name: 'reply', prose: REPLY_PROSE, url: REPLY_URL },
  ]
  for (const c of cases) {
    test(c.name, async ({ page }) => {
      await mockApi(page)
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      const prose = page.getByText(c.prose.slice(0, 40))
      const url = page.getByText(c.url)
      await expect(prose).toBeVisible()
      await expect(url).toBeVisible()

      const broken = await tokensBrokenAcrossLines(prose)
      expect(broken.filter((t) => !t.includes(c.url)), 'ordinary words split mid-word').toEqual([])
      // Not a vacuous pass: the URL is ~2000px of unbroken text in a ~300px
      // column, so if it is not drawn across several lines it overflowed or
      // was clipped.
      // Measured on the URL token itself: the element holding it may also
      // hold prose that wraps anyway.
      const urlBroken = (await tokensBrokenAcrossLines(url)).some((t) => t.includes(c.url))
      expect(urlBroken, 'the 300-character URL was not wrapped').toBe(true)
      await noHorizontalScroll(page)
    })
  }
})

// #479: a long unbroken token (a URL) in user text must wrap at the box edge
// on a phone rather than widen the page. The text itself stays whole.
//
// The URL has no `?` or `=`: Chromium offers a break after those even with no
// wrap rule at all, which would let a broken page pass.
const PHONE_WIDTHS = [320, 375, 414]
const NOTE_URL = longUrlFor('internal')

function longUrlFor(path: string) {
  return `https://example.com/${path}/`.padEnd(300, '0123456789abcdef')
}

// Unlike noHorizontalScroll this reads the document: the guest pages have no
// <main>, and for them the document is exactly what scrolls sideways.
async function documentFits(page: Page, width: number) {
  const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth)
  expect(scrollWidth, `the page scrolls sideways at ${width}px`).toBeLessThanOrEqual(width)
}

// The whole URL is still in the DOM, unmodified and unshortened, so it can be
// selected and copied.
async function expectTextIntact(page: Page, url: string) {
  await expect(page.getByText(url).first()).toHaveText(new RegExp(url))
}

test.describe('#479: long URLs wrap on a phone', () => {
  const guestTicket = {
    tracking_number: 'GHD-2026-000009',
    subject: `Link ${longUrlFor('subject')}`,
    description: `Please see ${longUrlFor('description')}`,
    status: 'Open',
    created_at: '2026-09-20T10:00:00Z',
    replies: [
      { id: 'g1', body: `Courier tracking ${longUrlFor('reply')}`, created_at: '2026-09-20T11:00:00Z', from_you: false },
      { id: 'g2', body: `Mine ${longUrlFor('mine')}`, created_at: '2026-09-20T12:00:00Z', from_you: true },
    ],
  }

  for (const width of PHONE_WIDTHS) {
    test(`guest ticket page: ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await page.route('**/api/v1/guest/ticket', (route) => route.fulfill({ json: guestTicket }))
      await page.goto('/g#guest-token')
      await expect(page.getByText(guestTicket.tracking_number)).toBeVisible()
      await documentFits(page, width)
      await expectTextIntact(page, longUrlFor('reply'))
    })

    test(`staff ticket page, every place user text renders: ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await mockApi(page)
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}`, (route) =>
        route.fulfill({
          json: { ...TICKET_DETAIL, subject: `Link ${longUrlFor('subject')}`, description: `See ${longUrlFor('description')}` },
        }),
      )
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/replies`, (route) =>
        route.fulfill({
          json: [
            ...REPLIES,
            { ...REPLIES[0], id: 'rep-2', internal: true, body: `Internal ${NOTE_URL}` },
          ],
        }),
      )
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      await expect(page.getByText(NOTE_URL)).toBeVisible()
      await noHorizontalScroll(page)
      await documentFits(page, width)
      await expectTextIntact(page, NOTE_URL)
    })

    test(`linked tickets panel: ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await mockApi(page)
      await page.route(`**/api/v1/tickets/${LINKED_TICKET.id}`, (route) =>
        route.fulfill({ json: { ...LINKED_TICKET, subject: `Dup ${longUrlFor('linked')}` } }),
      )
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      await expect(page.getByText(longUrlFor('linked'))).toBeVisible()
      await noHorizontalScroll(page)
      await documentFits(page, width)
    })

    // Free text a person typed that is not the description or a reply: a tag,
    // a custom field value, an attachment's filename, a reply author's name.
    test(`tags, custom fields, attachment names, author names: ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await mockApi(page)
      const tag = longUrlFor('tag')
      const value = longUrlFor('value')
      const file = `${longUrlFor('file')}.pdf`
      const author = longUrlFor('author')
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/tags`, (route) =>
        route.fulfill({ json: [{ ...TAGS[0], name: tag }] }),
      )
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/custom-fields`, (route) =>
        route.fulfill({ json: [{ ...CUSTOM_FIELDS[0], value }] }),
      )
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/attachments`, (route) =>
        route.fulfill({ json: [{ ...ATTACHMENTS[0], filename: file }] }),
      )
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/replies`, (route) =>
        route.fulfill({ json: [{ ...REPLIES[0], author_name: author }] }),
      )
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      await expect(page.getByText(value)).toBeVisible()
      await noHorizontalScroll(page)
      await documentFits(page, width)
    })

    test(`ticket list rows (staff): ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 844 })
      await mockApi(page)
      await page.route('**/api/v1/tickets?*', (route) =>
        route.fulfill({
          json: TICKETS.map((t, i) => (i === 2 ? { ...t, subject: `Deploy ${longUrlFor('list')}` } : t)),
        }),
      )
      await page.route('**/api/v1/tickets', (route) =>
        route.fulfill({
          json: TICKETS.map((t, i) => (i === 2 ? { ...t, subject: `Deploy ${longUrlFor('list')}` } : t)),
        }),
      )
      await page.goto('/tickets')
      await expect(page.locator('li').filter({ hasText: longUrlFor('list') })).toBeVisible()
      await noHorizontalScroll(page)
      await documentFits(page, width)
    })
  }
})
