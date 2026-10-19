import { test, expect, type Locator, type Page } from '@playwright/test'
import {
  mockApi,
  mockAdminApi,
  LONG_USER,
  SAML_CERT,
  DESCRIPTION_PROSE,
  DESCRIPTION_URL,
  ME_ADMIN,
  ME_REPORTER,
  ME_STAFF,
  ASSIGNABLE_STAFF,
  ATTACHMENTS,
  STATUSES,
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
// TicketDetail, NewTicket). The admin pages are covered by the #522 blocks at
// the end of this file, at 320 and 375 rather than 390.
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
      // Pinned, not left to the room it happens to have: whitespace-nowrap is
      // what keeps the number whole if the column is ever narrower than it.
      expect(await number.evaluate((el) => getComputedStyle(el).whiteSpace), 'tracking number may break').toBe('nowrap')
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
  // 768 is md itself (Tailwind md = min-width 768px): the first width at
  // which the row layout applies. 767 above is the last stacked width, so the
  // pair pins the boundary from both sides.
  for (const width of [768, 1024]) {
  test(`from md up the actions still sit beside the title (unchanged): ${width}px`, async ({ page }) => {
    await mockApi(page)
    await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}`, (route) =>
      route.fulfill({ json: { ...TICKET_DETAIL, status_id: 'st-closed', can_reopen: true } }),
    )
    await page.setViewportSize({ width, height: 800 })
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    const t = await page.getByRole('heading', { level: 1 }).boundingBox()
    const r = await page.getByRole('button', { name: 'Reopen' }).boundingBox()
    if (!t || !r) throw new Error('header not rendered')
    expect(r.x, 'actions no longer beside the title at desktop width').toBeGreaterThanOrEqual(t.x + t.width)
    expect(r.y).toBeLessThan(t.y + t.height)
  })
  }
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
    status: 'S'.repeat(300),
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
          json: {
            ...TICKET_DETAIL,
            subject: `Link ${longUrlFor('subject')}`,
            description: `See ${longUrlFor('description')}`,
            // #514's Requester line: a long name and an address with no break point.
            requester: { name: 'R'.repeat(300), email: `${'q'.repeat(300)}@example.com` },
            type_id: 'ty-1',
            item_id: 'it-1',
          },
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
      // The rest of the page's user text: status history, the activity feed
      // (actor name and a subject change holding a URL), followers, the
      // assignee and a status name. A long word, not only a URL, for names.
      const word = 'W'.repeat(300)
      await page.route('**/api/v1/categories/cat-1/types', (route) =>
        route.fulfill({ json: [{ id: 'ty-1', category_id: 'cat-1', name: 'T'.repeat(300), sort_order: 1, active: true }] }),
      )
      await page.route('**/api/v1/categories/cat-1/types/ty-1/items', (route) =>
        route.fulfill({ json: [{ id: 'it-1', type_id: 'ty-1', name: 'I'.repeat(300), sort_order: 1, active: true }] }),
      )
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/history`, (route) =>
        route.fulfill({
          json: [
            {
              id: 'h1', ticket_id: TICKET_DETAIL.id, from_status_id: 'st-new', from_status_name: word,
              from_status_color: '#3b82f6', to_status_id: 'st-resolved', to_status_name: 'Resolved',
              to_status_color: '#22c55e', changed_by_user_id: 'u-staff', changed_by_name: word,
              created_at: '2026-09-20T12:00:00Z',
            },
          ],
        }),
      )
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/audit`, (route) =>
        route.fulfill({
          json: [
            {
              id: 'a1', action: 'updated', actor_id: 'u-staff', actor_name: word,
              created_at: '2026-09-20T12:00:00Z',
              before: { subject: longUrlFor('before') }, after: { subject: longUrlFor('after') },
            },
          ],
        }),
      )
      await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/followers`, (route) =>
        route.fulfill({ json: [{ user_id: 'u-f', display_name: word, created_at: '2026-09-20T12:00:00Z' }] }),
      )
      await page.route('**/api/v1/staff', (route) =>
        route.fulfill({ json: [{ ...ASSIGNABLE_STAFF[0], display_name: word }] }),
      )
      await page.route('**/api/v1/statuses', (route) =>
        route.fulfill({ json: STATUSES.map((st) => (st.id === 'st-new' ? { ...st, name: word } : st)) }),
      )
      await page.goto(`/tickets/${TICKET_DETAIL.id}`)
      await expect(page.getByText(NOTE_URL)).toBeVisible()
      // Each of these is present on the page, so a zero-width miss is not a pass.
      for (const text of [longUrlFor('after'), 'Followers']) {
        await expect(page.getByText(text).first()).toBeVisible()
      }
      await expect(page.getByText(word).first()).toBeVisible()
      await expect(page.getByText('T'.repeat(300))).toBeVisible()
      await expect(page.getByText('I'.repeat(300))).toBeVisible()
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

// Ellipsis is allowed only in compact rows and chips, and only where the full
// text is still reachable: a title attribute (hover, long-press) and a click
// through to where it is shown in full. Ticket content itself always wraps.
test.describe('truncated text exposes its full text in a title', () => {
  const long = longUrlFor('truncated')

  test('linked ticket line and the desktop-only truncations', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 })
    await mockApi(page, { me: { ...ME_STAFF, email: `${'e'.repeat(80)}@example.com` } })
    await page.route(`**/api/v1/tickets/${LINKED_TICKET.id}`, (route) =>
      route.fulfill({ json: { ...LINKED_TICKET, subject: long } }),
    )
    await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/canned-responses`, (route) =>
      route.fulfill({ json: [{ id: 'c1', name: 'Greeting', body: `Hello ${long}`, sort_order: 1, created_at: '2026-09-20T10:00:00Z' }] }),
    )
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    await page.getByRole('button', { name: 'Insert canned response' }).click()
    await expect(page.getByText(`Hello ${long}`)).toBeVisible()

    const truncated = await page.evaluate(() =>
      Array.from(document.querySelectorAll('.truncate')).map((el) => ({
        text: (el.textContent ?? '').trim(),
        title: el.getAttribute('title') ?? (el.closest('[title]') as HTMLElement | null)?.title ?? null,
      })),
    )
    const texts = truncated.map((t) => t.text)
    // Not vacuous: the linked line, the canned preview and the sidebar email are all there.
    expect(texts.some((t) => t.includes(`${LINKED_TICKET.tracking_number} · ${long}`))).toBe(true)
    expect(texts.some((t) => t === `Hello ${long}`), 'canned preview not found').toBe(true)
    expect(texts.some((t) => t.startsWith('eeee')), 'sidebar email not found').toBe(true)
    // Every truncated element in the document, unfiltered: the sidebar and
    // top-bar brand included.
    expect(truncated.length).toBeGreaterThanOrEqual(4)
    for (const t of truncated) expect(t.title, `"${t.text.slice(0, 40)}" is cut with no title`).toBe(t.text)
  })

  test('pending upload filename', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockApi(page)
    await page.goto('/tickets/new')
    const name = `${'f'.repeat(120)}.txt`
    await page.locator('input[type="file"]').setInputFiles({ name, mimeType: 'text/plain', buffer: Buffer.from('x') })
    const row = page.locator('li span.truncate', { hasText: name })
    await expect(row).toBeVisible()
    await expect(row).toHaveAttribute('title', name)
  })

  test('a reply author name and an attachment filename wrap in full instead of being cut', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 })
    await mockApi(page)
    const author = longUrlFor('author')
    const file = `${longUrlFor('file')}.pdf`
    await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/replies`, (route) =>
      route.fulfill({ json: [{ ...REPLIES[0], author_name: author }] }),
    )
    await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/attachments`, (route) =>
      route.fulfill({ json: [{ ...ATTACHMENTS[0], filename: file }] }),
    )
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    for (const text of [author, file]) {
      const el = page.getByText(text, { exact: true })
      await expect(el).toBeVisible()
      const m = await el.evaluate((e) => ({
        ellipsis: getComputedStyle(e).textOverflow,
        cut: e.scrollWidth > e.clientWidth,
        title: e.getAttribute('title') ?? (e.closest('[title]') as HTMLElement | null)?.title ?? null,
      }))
      expect(m.ellipsis, `${text.slice(0, 30)} is ellipsised, so its full text is nowhere in the app`).not.toBe('ellipsis')
      expect(m.cut).toBe(false)
      expect(await lineCount(el), 'a long name was not wrapped over several lines').toBeGreaterThan(1)
      expect(m.title).toBe(text)
    }
    // The download glyph is decoration.
    await expect(page.locator('a[download] [aria-hidden="true"]', { hasText: '↓' })).toHaveCount(1)
  })
})

test.describe('#517 review: more places at 320px', () => {
  test('admin audit page: a diff value with a long URL and a long word', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 })
    await mockApi(page, { me: ME_ADMIN })
    const word = 'W'.repeat(300)
    await page.route(/\/api\/v1\/admin\/audit/, (route) =>
      route.fulfill({
        json: {
          entries: [
            {
              id: 'ad1', entity_type: 'ticket', entity_id: 'tk-1', action: 'updated', actor_id: 'u-admin',
              actor_name: 'Ada Admin', created_at: '2026-09-20T12:00:00Z',
              before: { subject: longUrlFor('before'), note: word }, after: { subject: longUrlFor('after'), note: `${word}x` },
            },
          ],
          total: 1,
          total_capped: false,
        },
      }),
    )
    await page.goto('/admin/audit')
    const item = page.locator('li', { hasText: 'subject' }).first()
    await expect(item).toBeVisible()
    // The diff's own width, not the page: the table's other columns set the
    // page width here. What has to hold is that the diff itself wraps, on the
    // one page where nothing above it in the tree wraps for it.
    const widest = await page.locator('li', { hasText: 'subject' }).evaluateAll((els) => Math.max(...els.map((e) => e.getBoundingClientRect().width)))
    expect(widest, 'an audit diff value is wider than the viewport: it is not wrapping').toBeLessThanOrEqual(320)
  })

  const STATUS_NAMES = ['Waiting on third-party vendor response', 'S'.repeat(300)]
  for (const name of STATUS_NAMES) {
    test(`list card status chip: ${name.length} characters`, async ({ page }) => {
      await page.setViewportSize({ width: 320, height: 844 })
      await mockApi(page)
      await page.route('**/api/v1/statuses', (route) =>
        route.fulfill({ json: STATUSES.map((st) => (st.id === 'st-new' ? { ...st, name } : st)) }),
      )
      await page.goto('/tickets')
      const card = page.locator('li').filter({ hasText: 'Printer on fire, second floor' })
      await expect(card).toBeVisible()
      await noHorizontalScroll(page)
      await documentFits(page, 320)
      const chipRight = await card.getByText(name, { exact: true }).evaluate((e) => e.getBoundingClientRect().right)
      const cardRight = await card.evaluate((e) => e.getBoundingClientRect().right)
      expect(chipRight, 'the status chip runs outside its card').toBeLessThanOrEqual(cardRight)
    })
  }

  test('desktop table: a long status name has a title', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 })
    await mockApi(page)
    const name = 'Waiting on third-party vendor response'
    await page.route('**/api/v1/statuses', (route) =>
      route.fulfill({ json: STATUSES.map((st) => (st.id === 'st-new' ? { ...st, name } : st)) }),
    )
    await page.goto('/tickets')
    await expect(page.locator('td').getByText(name).first()).toHaveAttribute('title', name)
  })

  test('requester picker: hit list and "Filing for" line with a realistic 59-character email', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 })
    await mockApi(page)
    const email = `${'firstname.lastname.department'.padEnd(47, 'x')}@example.com`
    expect(email.length).toBe(59)
    const hit = { id: 'u-h', display_name: 'N'.repeat(300), email }
    await page.route(/\/api\/v1\/users\/lookup/, (route) => route.fulfill({ json: [hit] }))
    await page.goto('/tickets/new')
    await page.getByLabel('Another user').check()
    await page.getByLabel('Search users').fill('first')
    const result = page.getByRole('list', { name: 'Matching users' })
    await expect(result).toBeVisible()
    await noHorizontalScroll(page)
    await documentFits(page, 320)
    await result.getByRole('button').click()
    await expect(page.getByText('Filing for')).toBeVisible()
    await noHorizontalScroll(page)
    await documentFits(page, 320)
  })

  test('category name and custom-field label in the sidebar', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 })
    await mockApi(page)
    const category = 'C'.repeat(300)
    const label = 'L'.repeat(300)
    await page.route('**/api/v1/categories', (route) =>
      route.fulfill({ json: [{ id: 'cat-1', name: category, active: true }] }),
    )
    await page.route(`**/api/v1/tickets/${TICKET_DETAIL.id}/custom-fields`, (route) =>
      route.fulfill({ json: [{ ...CUSTOM_FIELDS[0], field_name: label }] }),
    )
    await page.goto(`/tickets/${TICKET_DETAIL.id}`)
    await expect(page.getByText(label)).toBeVisible()
    await expect(page.getByText(category).first()).toBeVisible()
    await noHorizontalScroll(page)
    await documentFits(page, 320)
  })
})

test.describe('truncated text, continued', () => {
  const long = longUrlFor('truncated')
  test('ticket list table subject (desktop width)', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 })
    await mockApi(page)
    const tickets = TICKETS.map((t, i) => (i === 2 ? { ...t, subject: `Deploy ${long}` } : t))
    await page.route('**/api/v1/tickets', (route) => route.fulfill({ json: tickets }))
    await page.route('**/api/v1/tickets?*', (route) => route.fulfill({ json: tickets }))
    await page.goto('/tickets')
    const cell = page.locator('td').filter({ hasText: long })
    await expect(cell).toBeVisible()
    const link = cell.getByRole('link')
    await expect(link).toHaveAttribute('title', `Deploy ${long}`)
  })
})

// #517 round 3: the remaining ellipsis places outside ticket content each
// carry a title, and two pre-existing overflows wrap.
async function truncatedWithTitle(page: Page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll('.truncate')).map((el) => ({
      text: (el.textContent ?? '').trim(),
      title: el.getAttribute('title') ?? (el.closest('[title]') as HTMLElement | null)?.title ?? null,
    })),
  )
}

test.describe('#517 round 3: titles on the remaining truncations', () => {
  test('dashboard client name and email', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 })
    await mockApi(page, { me: ME_ADMIN })
    await page.route(/\/api\/v1\/admin\/users/, (route) =>
      route.fulfill({
        json: [{ ...ME_REPORTER, id: 'u-c', display_name: 'D'.repeat(80), email: `${'m'.repeat(80)}@example.com`, disabled: false }],
      }),
    )
    await page.goto('/dashboard')
    await expect(page.getByText('D'.repeat(80))).toBeVisible()
    const t = await truncatedWithTitle(page)
    expect(t.length).toBeGreaterThanOrEqual(3)
    for (const x of t) expect(x.title, `"${x.text.slice(0, 40)}" is cut with no title`).toBe(x.text)
  })

  test('account page passkey label', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 })
    await mockApi(page)
    const name = 'K'.repeat(120)
    await page.route('**/api/v1/me/passkeys', (route) =>
      route.fulfill({
        json: [{ id: 'pk1', name, transports: ['usb'], backup_eligible: false, backup_state: false, created_at: '2026-09-20T10:00:00Z', last_used_at: null }],
      }),
    )
    await page.goto('/account')
    await expect(page.getByText(name)).toBeVisible()
    const t = await truncatedWithTitle(page)
    for (const x of t) expect(x.title, `"${x.text.slice(0, 40)}" is cut with no title`).toBe(x.text)
  })
})

test.describe('#517 round 3: two long names that ran the page sideways', () => {
  const name = 'B'.repeat(300)

  test('the drawer sidebar brand wraps', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 })
    await mockApi(page)
    await page.route('**/api/v1/site', (route) =>
      route.fulfill({ json: { name, logo_url: '', version: '1.3.0' } }),
    )
    await page.goto('/dashboard')
    await page.getByRole('button', { name: /menu|navigation/i }).click()
    const brand = page.getByRole('dialog').getByText(name)
    await expect(brand).toBeVisible()
    const right = await brand.evaluate((e) => e.getBoundingClientRect().right)
    expect(right, 'the drawer brand runs off the screen').toBeLessThanOrEqual(320)
  })

  test('the dashboard "Welcome back" line wraps', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 844 })
    await mockApi(page, { me: { ...ME_STAFF, display_name: name } })
    await page.goto('/dashboard')
    await expect(page.getByText(`Welcome back, ${name}`)).toBeVisible()
    await noHorizontalScroll(page)
  })
})

// #517 round 4: the sign-in card's title is the site name, which an operator
// sets and which can be long. /setup redirects to /login once an account
// exists, so this is the page it lands on too.
test.describe('#517 round 4: sign-in page with a long site name', () => {
  const names = { url: longUrlFor('site'), word: 'S'.repeat(300) }
  for (const [kind, name] of Object.entries(names)) {
    test(`/login at 320px: ${kind}`, async ({ page }) => {
      await page.setViewportSize({ width: 320, height: 844 })
      await mockApi(page)
      await page.route('**/api/v1/me', (route) => route.fulfill({ status: 401, json: { error: { code: 'unauthorized', message: 'no session' } } }))
      await page.route('**/api/v1/site', (route) => route.fulfill({ json: { name, logo_url: '', version: '1.3.0' } }))
      await page.goto('/login')
      await expect(page.getByText(`Sign in to ${name}`)).toBeVisible()
      await documentFits(page, 320)
    })
  }
})

// #522: the admin pages, at the narrowest phone (320) and a common one (375),
// with 300-character unbroken names, descriptions and URLs. Short data is
// tested too where a page already overflowed without long data.
//
// Measures main AND the document: #522 saw main at 2433px and the document at
// 557px on Webhooks, and either one scrolling sideways is the bug.
async function noSidewaysScroll(page: Page, width: number) {
  const m = await page.evaluate(() => {
    const main = document.querySelector('main')
    if (!main) throw new Error('no <main> element found')
    return {
      main: main.scrollWidth, mainBox: main.clientWidth,
      doc: document.documentElement.scrollWidth, docBox: document.documentElement.clientWidth,
    }
  })
  expect(m.main, `main is ${m.main}px wide in a ${m.mainBox}px box at ${width}px`).toBeLessThanOrEqual(m.mainBox)
  expect(m.doc, `the document is ${m.doc}px wide in a ${m.docBox}px viewport at ${width}px`).toBeLessThanOrEqual(m.docBox)
}

// Opens every node of the category tree, one level per pass.
async function expandTree(page: Page) {
  for (let pass = 0; pass < 4; pass++) {
    const closed = page.getByRole('button', { name: 'Expand' })
    if ((await closed.count()) === 0) return
    await closed.first().click()
    await page.waitForLoadState('networkidle')
  }
}

const ADMIN_PAGES: { name: string; path: string; ready: string | RegExp; short?: boolean; tab?: string; act?: (page: Page) => Promise<void> }[] = [
  { name: 'users', path: '/admin/users', ready: 'Users' },
  { name: 'users (short data)', path: '/admin/users', ready: 'Users', short: true },
  { name: 'user detail', path: `/admin/users/${LONG_USER.id}`, ready: 'NNNNNNNN' },
  { name: 'groups', path: '/admin/groups', ready: 'Groups' },
  { name: 'roles', path: '/admin/roles', ready: 'Roles' },
  { name: 'categories', path: '/admin/categories', ready: 'Categories' },
  { name: 'statuses', path: '/admin/statuses', ready: 'Statuses' },
  { name: 'tags', path: '/admin/tags', ready: 'Tags' },
  { name: 'canned responses', path: '/admin/canned-responses', ready: /Canned/ },
  { name: 'custom fields', path: '/admin/custom-fields', ready: /Custom/ },
  { name: 'API keys', path: '/admin/api-keys', ready: /API keys/i },
  { name: 'OAuth clients', path: '/admin/oauth-clients', ready: /OAuth/ },
  { name: 'webhooks', path: '/admin/webhooks', ready: /Webhooks/ },
  { name: 'routing', path: '/admin/routing', ready: /Routing/ },
  { name: 'audit', path: '/admin/audit', ready: /Audit/ },
  { name: 'users: add form', path: '/admin/users', ready: 'Users', act: (p) => p.getByRole('button', { name: 'Add User' }).click() },
  { name: 'groups: members open', path: '/admin/groups', ready: 'Groups', act: (p) => p.getByRole('button', { name: 'Members' }).first().click() },
  { name: 'groups: name editor open', path: '/admin/groups', ready: 'Groups', act: (p) => p.getByTitle('Edit group').first().click() },
  { name: 'categories: tree expanded', path: '/admin/categories', ready: 'Categories', act: expandTree },
  { name: 'webhooks: edit form', path: '/admin/webhooks', ready: /Webhooks/, act: (p) => p.getByRole('button', { name: 'Edit' }).first().click() },
  { name: 'webhooks: history open', path: '/admin/webhooks', ready: /Webhooks/, act: (p) => p.getByRole('button', { name: 'History' }).first().click() },
  { name: 'settings: general', path: '/admin/settings', ready: 'Settings' },
  { name: 'settings: branding', path: '/admin/settings', ready: 'Settings', tab: 'Branding' },
  { name: 'settings: authentication', path: '/admin/settings', ready: 'Settings', tab: 'Authentication' },
  { name: 'settings: features (SLA)', path: '/admin/settings', ready: 'Settings', tab: 'Features' },
  { name: 'settings: email', path: '/admin/settings', ready: 'Settings', tab: 'Email' },
  { name: 'settings: attachments', path: '/admin/settings', ready: 'Settings', tab: 'Attachments' },
]

test.describe('#522: admin pages fit a phone', () => {
  for (const width of [320, 375]) {
    for (const p of ADMIN_PAGES) {
      test(`${p.name}: ${width}px`, async ({ page }) => {
        await mockAdminApi(page, { short: p.short })
        await page.setViewportSize({ width, height: 844 })
        await page.goto(p.path)
        await expect(page.getByRole('heading', { level: 1 }).filter({ hasText: p.ready })).toBeVisible()
        if (p.tab) await page.getByRole('button', { name: p.tab, exact: true }).click()
        // Let the queries settle so the long data is on screen before measuring.
        await page.waitForLoadState('networkidle')
        if (p.act) await p.act(page)
        await noSidewaysScroll(page, width)
      })
    }
  }
})

// #522: the SAML certificate line is cut to its first line and ends in an
// ellipsis, so the full PEM has to be on hover. It is the one compact place on
// the admin pages that stays truncated.
test.describe('#522: the truncated SAML certificate line carries its full text', () => {
  test('title holds the whole PEM', async ({ page }) => {
    await mockAdminApi(page)
    await page.goto('/admin/settings')
    await page.getByRole('button', { name: 'Authentication', exact: true }).click()
    const line = page.getByText('-----BEGIN CERTIFICATE-----…', { exact: true })
    await expect(line).toBeVisible()
    await expect(line).toHaveAttribute('title', SAML_CERT)
  })
})
