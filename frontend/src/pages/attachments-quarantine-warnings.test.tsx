import { describe, expect, it, vi, beforeEach } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { attachmentDownloadUrl } from '@/api/tickets'
import { useAuthStore } from '@/store/auth'
import type { Attachment } from '@/api/types'

// The staff-facing warning surfaces for #168 (quarantined attachments).
//
// These tests drive TicketDetailPage rather than any component, deliberately.
// The requirement is about what a person looking at a ticket sees and can do,
// and it says nothing about how the page is split up. Whether the banner, the
// row and the dialog end up as one component or five is the implementer's
// call; every assertion here is phrased so that any of those arrangements can
// satisfy it.
//
// Three states the backend can describe, which must not look alike:
//
//   virus_name non-null        the scanner identified it. Loud treatment:
//                              ticket banner, warning on the row, password,
//                              confirmation before download.
//   virus_name null,           the content contradicted the filename and the
//   mismatch true              scanner did NOT flag it. Wrapped without a
//                              password. Quieter, and never called malicious.
//   everything null            nobody inspected it — it predates the feature.
//                              Not a clean bill of health.
//
// The issue is emphatic that the loud treatment stays rare: "a warning that
// fires often is a warning people click past. The loud treatment applies only
// to a stored Infected verdict — never to a type mismatch, never to an
// executable extension, never to 'unscanned'." Half the tests below are that
// rule, from the other side.

const TICKET_ID = 'tkt-1'

vi.mock('@tanstack/react-router', () => ({
  useParams: () => ({ id: TICKET_ID }),
  useRouterState: () => ({ location: { pathname: `/tickets/${TICKET_ID}` } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { TicketDetailPage } from './TicketDetailPage'

// ── Fixtures ──────────────────────────────────────────────────────────────────

// The real EICAR hash, so anyone reading a failure can recognise it.
const EICAR_SHA = '275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f'
const DETECTION = 'Eicar-Test-Signature'

function attachment(over: Partial<Attachment> & { id: string; filename: string }): Attachment {
  return {
    ticket_id: TICKET_ID,
    mime_type: 'application/octet-stream',
    size_bytes: 4096,
    created_at: '2026-09-20T10:05:00Z',
    detected_mime: null,
    sha256: null,
    virus_name: null,
    mismatch: null,
    // Built by the server from the configured provider, so the frontend never
    // learns which one it is. Defaulted to null; the fixtures that care set it.
    reputation_url: null,
    ...over,
  }
}

// Quarantined: scanned, identified, wrapped with the password `infected`.
// mime_type and size_bytes describe the archive; detected_mime describes the
// sample inside it, and the hash is of the sample, not of our wrapper.
const INFECTED = attachment({
  id: 'att-bad',
  filename: 'invoice.exe.zip',
  mime_type: 'application/zip',
  detected_mime: 'application/vnd.microsoft.portable-executable',
  sha256: EICAR_SHA,
  virus_name: DETECTION,
  mismatch: false,
  // The server builds this from the configured provider; the frontend never
  // learns which one. Present here because an inspected file always has one.
  reputation_url: `https://www.virustotal.com/gui/file/${EICAR_SHA}`,
})

const INFECTED_TWO = attachment({
  id: 'att-bad-2',
  filename: 'update.scr.zip',
  mime_type: 'application/zip',
  detected_mime: 'application/vnd.microsoft.portable-executable',
  sha256: 'f'.repeat(64),
  virus_name: 'Trojan.GenericKD.71234567',
  mismatch: false,
})

// Scanned, nothing found, content agrees with the name. The ordinary case.
const CLEAN = attachment({
  id: 'att-ok',
  filename: 'screenshot.png',
  mime_type: 'image/png',
  size_bytes: 20480,
  detected_mime: 'image/png',
  sha256: 'a'.repeat(64),
  virus_name: null,
  mismatch: false,
})

// Content contradicted the name; the scanner did not flag it. Stored wrapped
// as suspicious-<crc32>.zip, with no password.
const MISMATCH = attachment({
  id: 'att-sus',
  filename: 'suspicious-1a2b3c4d.zip',
  mime_type: 'application/zip',
  detected_mime: 'application/vnd.microsoft.portable-executable',
  sha256: 'b'.repeat(64),
  virus_name: null,
  mismatch: true,
})

// The control for MISMATCH: same file, same name, but the content matched and
// nothing was flagged. Only the two verdict fields differ, so any difference
// in what renders has to come from them.
const MISMATCH_CONTROL = attachment({
  ...MISMATCH,
  detected_mime: 'application/zip',
  mismatch: false,
})

// Uploaded before any of this existed. Every field null.
const UNINSPECTED = attachment({
  id: 'att-old',
  filename: 'notes.txt',
  mime_type: 'text/plain',
  size_bytes: 300,
})

// The control for UNINSPECTED: the same file, actually inspected and clean.
const UNINSPECTED_CONTROL = attachment({
  ...UNINSPECTED,
  detected_mime: 'text/plain',
  sha256: 'c'.repeat(64),
  mismatch: false,
})

// ── Harness ───────────────────────────────────────────────────────────────────

const TICKET = {
  id: TICKET_ID,
  tracking_number: 'TKT-0001',
  subject: 'User forwarded a suspicious file',
  description: 'Reported by finance.',
  category_id: 'cat-1',
  priority: 'high',
  status_id: 'st-new',
  created_at: '2026-09-20T10:00:00Z',
  updated_at: '2026-09-20T10:00:00Z',
}

const STATUSES = [
  { id: 'st-new', name: 'New', kind: 'system', sort_order: 1, color: '#888', active: true, ticket_count: 1 },
]

// One spy on the HTTP client covers every query the page and its panels fire.
// Anything not named here answers with an empty list, which is what the page
// needs for the sidebar panels that are not under test.
function mockApi(attachments: Attachment[]) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === `/tickets/${TICKET_ID}`) return Promise.resolve({ data: TICKET })
    if (url === `/tickets/${TICKET_ID}/attachments`) return Promise.resolve({ data: attachments })
    if (url === '/statuses') return Promise.resolve({ data: STATUSES })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

beforeEach(() => {
  // Staff, not admin: an admin also fires the security-warnings query, which
  // renders its own banner and would muddy the banner assertions below.
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'staff@example.com',
      display_name: 'Sam Staff',
      role: 'staff',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

async function renderTicket(attachments: Attachment[]) {
  mockApi(attachments)
  const result = renderWithQuery(<TicketDetailPage />)
  // The attachment list is the last thing to arrive; waiting for a filename to
  // appear anywhere means every assertion afterwards runs against a settled
  // page. Matched against the whole body rather than with findByText, which
  // wants the filename to be an element's entire text — a row that renders
  // "⚠ report.pdf" in one element is a perfectly good row.
  await waitFor(() => {
    expect(bodyText()).toContain(attachments.length > 0 ? attachments[0].filename : TICKET.subject)
  })
  return result
}

function bodyText(): string {
  return document.body.textContent ?? ''
}

/**
 * The persistent warning bar for the ticket as a whole.
 *
 * Matched by role rather than by copy: the issue fixes what the banner must
 * convey but not its wording, and a bar that staff must not miss has to be
 * announced rather than merely coloured. `alert` is what the one existing
 * banner in this codebase uses (InsecureConfigBanner); `status` is accepted
 * because it is the better role for a standing description of the page's
 * contents, and the issue does not choose between them.
 */
function ticketBanner(): HTMLElement | undefined {
  const candidates = [...screen.queryAllByRole('alert'), ...screen.queryAllByRole('status')]
  return candidates.find((el) => /quarantin|infected|malicious|malware/i.test(el.textContent ?? ''))
}

/**
 * The part of the page that belongs to one attachment and no other.
 *
 * Walks up from the innermost element holding `filename` for as long as the
 * parent stays clear of `otherFilename`. The result is the largest region
 * that is still about this attachment alone — which is what "the row carries
 * it inline" means, and is why these assertions cannot be satisfied by a
 * banner somewhere else on the page saying the same words.
 */
function rowFor(filename: string, otherFilename: string): HTMLElement {
  const all = Array.from(document.body.querySelectorAll<HTMLElement>('*')).filter((el) =>
    (el.textContent ?? '').includes(filename),
  )
  const innermost = all.filter((el) => !all.some((other) => other !== el && el.contains(other)))
  expect(innermost.length, `nothing on the page renders the filename ${filename}`).toBeGreaterThan(0)

  let el = innermost[0]
  while (el.parentElement && !(el.parentElement.textContent ?? '').includes(otherFilename)) {
    el = el.parentElement
  }
  return el
}

/** The anchor that actually fetches the bytes, if the page is offering one. */
function downloadLink(attachmentId: string): HTMLAnchorElement | null {
  const href = attachmentDownloadUrl(TICKET_ID, attachmentId)
  return document.querySelector<HTMLAnchorElement>(`a[href="${href}"]`)
}

// ── The ticket banner ─────────────────────────────────────────────────────────

describe('the ticket banner', () => {
  // "A banner on the ticket, not a subtle badge, whenever it holds a
  // quarantined attachment. Persistent — not dismissible into invisibility."
  // The count is what makes this a banner about the ticket rather than a
  // second copy of the row's warning: two quarantined files among three.
  it('warns on any ticket holding quarantined attachments, and says how many', async () => {
    await renderTicket([CLEAN, INFECTED, INFECTED_TWO])

    const banner = ticketBanner()
    expect(
      banner,
      'a ticket holding quarantined attachments renders no persistent warning banner',
    ).toBeDefined()
    expect(
      banner!.textContent,
      'the banner does not say how many quarantined attachments the ticket holds',
    ).toMatch(/\b2\b/)
  })

  // A warning with an × becomes a warning nobody sees twice. There is nothing
  // to click that makes it go away.
  it('offers no way to dismiss the banner', async () => {
    await renderTicket([INFECTED])

    const banner = ticketBanner()
    expect(banner, 'no banner to check for a dismiss control').toBeDefined()

    const dismiss = within(banner!)
      .queryAllByRole('button')
      .filter((b) => /dismiss|close|hide|ignore|✕|×|⨯/i.test(b.textContent + ' ' + (b.getAttribute('aria-label') ?? '')))
    expect(dismiss.map((b) => b.outerHTML), 'the banner can be dismissed').toEqual([])
  })

  // The rarity rule, from the other side. A ticket whose attachments are all
  // fine must be indistinguishable from one with no attachments at all.
  it('does not warn when nothing is quarantined', async () => {
    await renderTicket([CLEAN, UNINSPECTED])
    expect(ticketBanner()?.textContent, 'a clean ticket carries a malware banner').toBeUndefined()
  })

  // A type mismatch is a different, quieter signal. Escalating it to the
  // ticket banner is what turns the banner into decoration.
  it('does not warn for a type mismatch', async () => {
    await renderTicket([MISMATCH])
    expect(
      ticketBanner()?.textContent,
      'a type mismatch raised the ticket-level malware banner',
    ).toBeUndefined()
  })
})

// ── The attachment row ────────────────────────────────────────────────────────

describe('the row for a quarantined attachment', () => {
  // All four facts, inline and unprompted: identified as malicious, the
  // scanner's name for it, that it is wrapped, and the password. Scoped to the
  // row, so a banner elsewhere on the page carrying the same words does not
  // satisfy this.
  it('says it is malicious, names the detection, and states the password', async () => {
    await renderTicket([INFECTED, CLEAN])
    const text = rowFor(INFECTED.filename, CLEAN.filename).textContent ?? ''

    // "Infected" alone tells an analyst nothing; the family name is the first
    // thing they act on.
    expect(text, 'the row does not name the detection').toContain(DETECTION)
    // The word as well as the colour — colour alone fails a colourblind
    // reader and fails completely in a screenshot pasted into a ticket.
    // "infected" on its own does not count here: it is the password.
    expect(text, 'the row never says the file is malicious').toMatch(/malicious|malware/i)
    expect(text, 'the row does not say the file is in a password-protected archive').toMatch(
      /password[- ]protected/i,
    )
    expect(text, 'the row does not mention the archive').toMatch(/archive|zip/i)
    // Useless without the password, and the password is published on purpose.
    expect(text, 'the row does not state the password `infected`').toMatch(
      /password[^a-z0-9]{0,20}infected/i,
    )
  })

  // Not behind a hover and not behind a tooltip: getByText matches rendered
  // text nodes only, so a title= attribute carrying the detection name would
  // fail this even though it "shows" on hover.
  it('renders the detection as text, not as an attribute', async () => {
    await renderTicket([INFECTED, CLEAN])
    expect(
      screen.queryAllByText(new RegExp(DETECTION)).length,
      `no rendered text contains ${DETECTION} — a title= or aria-label that only appears on hover does not count`,
    ).toBeGreaterThan(0)
  })
})

// ── The download confirmation ─────────────────────────────────────────────────

/** Opens the confirmation for the quarantined attachment and returns it. */
async function openConfirmation(user: ReturnType<typeof userEvent.setup>): Promise<HTMLElement> {
  const row = rowFor(INFECTED.filename, CLEAN.filename)
  const trigger = within(row).queryByRole('button', { name: /download/i })
  expect(
    trigger,
    'the quarantined attachment has no download control that could raise a confirmation',
  ).not.toBeNull()

  await user.click(trigger!)
  await waitFor(() =>
    expect(screen.queryByRole('dialog'), 'downloading a quarantined file raised no dialog').not.toBeNull(),
  )
  return screen.getByRole('dialog')
}

describe('downloading a quarantined attachment', () => {
  // The load-bearing one. Not "a dialog is rendered" but "the bytes cannot be
  // reached without going through it" — which means the URL is not in the
  // page at all beforehand. An <a href> that calls preventDefault and opens a
  // dialog would pass a weaker test and still be bypassed by middle-click,
  // ctrl-click, "Save link as", or a keyboard context menu.
  it('does not put the file within reach until the confirmation is open', async () => {
    const user = userEvent.setup()
    await renderTicket([INFECTED, CLEAN])

    expect(
      downloadLink(INFECTED.id)?.outerHTML,
      'the quarantined file is one click away, with no confirmation in between',
    ).toBeUndefined()

    await openConfirmation(user)

    // The other half: confirming has to actually get you the file.
    const dialog = screen.getByRole('dialog')
    const confirm =
      within(dialog).queryByRole('button', { name: /download/i }) ??
      within(dialog).queryByRole('link', { name: /download/i })
    expect(confirm, 'the dialog has no control that says it downloads the file').not.toBeNull()
    await user.click(confirm!)

    expect(
      downloadLink(INFECTED.id),
      'confirming did not make the file reachable',
    ).not.toBeNull()
  })

  // "It names the file, the detection, and the password" — a generic "are you
  // sure?" is the thing this is specified against.
  it('names the file, the detection and the password', async () => {
    const user = userEvent.setup()
    await renderTicket([INFECTED, CLEAN])
    const text = (await openConfirmation(user)).textContent ?? ''

    expect(text, 'the dialog does not name the file').toContain(INFECTED.filename)
    expect(text, 'the dialog does not name the detection').toContain(DETECTION)
    expect(text, 'the dialog does not say the file is malicious').toMatch(/malicious|malware/i)
    expect(text, 'the dialog does not state the password').toMatch(/password[^a-z0-9]{0,20}infected/i)
  })

  // The button has to say what it does. "OK" is the wording the issue rules
  // out by name, because the whole purpose of the click is that the person
  // reads what they are agreeing to.
  it('confirms with a button that says what it does, and offers a way out', async () => {
    const user = userEvent.setup()
    await renderTicket([INFECTED, CLEAN])
    const dialog = await openConfirmation(user)

    const names = within(dialog)
      .queryAllByRole('button')
      .concat(within(dialog).queryAllByRole('link'))
      .map((el) => (el.textContent ?? '').trim())

    expect(
      names.some((n) => /download/i.test(n)),
      `no control in the dialog says it downloads the file: ${JSON.stringify(names)}`,
    ).toBe(true)
    expect(
      names.some((n) => /^(ok|yes)$/i.test(n)),
      `the dialog confirms with a bare ${JSON.stringify(names)} instead of naming the action`,
    ).toBe(false)
    expect(
      names.some((n) => /cancel|not now|go back/i.test(n)),
      `the dialog offers no way out: ${JSON.stringify(names)}`,
    ).toBe(true)
  })

  // Backing out has to back out. Without this, a dialog whose Cancel also
  // downloads passes every other test here.
  it('leaves the file out of reach when the confirmation is cancelled', async () => {
    const user = userEvent.setup()
    await renderTicket([INFECTED, CLEAN])
    const dialog = await openConfirmation(user)

    const cancel = within(dialog).queryByRole('button', { name: /cancel|not now|go back/i })
    expect(cancel, 'the dialog offers no way to cancel').not.toBeNull()
    await user.click(cancel!)

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(
      downloadLink(INFECTED.id)?.outerHTML,
      'cancelling the confirmation still left the file one click away',
    ).toBeUndefined()
  })

  // "No 'don't ask again': the whole point is that this cannot become muscle
  // memory." Tested as behaviour, not as the absence of a checkbox: download
  // once, then ask again and the dialog is still there. An implementation
  // that remembers the answer fails here even if it never renders a checkbox.
  it('asks again every time, and offers nothing that would stop it asking', async () => {
    const user = userEvent.setup()
    await renderTicket([INFECTED, CLEAN])

    const first = await openConfirmation(user)
    expect(
      within(first).queryAllByRole('checkbox').length,
      'the dialog carries a checkbox, which can only be a "do not ask again"',
    ).toBe(0)
    const optOut = within(first)
      .queryAllByRole('button')
      .concat(within(first).queryAllByRole('link'))
      .map((el) => (el.textContent ?? '').trim())
      .filter((n) => /again|remember|always|don'?t ask|stop asking|skip/i.test(n))
    expect(optOut, 'the dialog offers a way to stop being asked').toEqual([])

    const confirm =
      within(first).queryByRole('button', { name: /download/i }) ??
      within(first).queryByRole('link', { name: /download/i })
    await user.click(confirm!)

    // Close it if confirming left it open, then ask for the same file again.
    const cancel = screen.queryByRole('dialog')
      ? within(screen.getByRole('dialog')).queryByRole('button', { name: /cancel|not now|go back|close/i })
      : null
    if (cancel) await user.click(cancel)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await openConfirmation(user)
  })
})

// ── The SHA-256 ───────────────────────────────────────────────────────────────

describe('the SHA-256 on a quarantined attachment', () => {
  // The hash is what an analyst works from, and the next thing they do with it
  // is paste it somewhere. The displayed form may well be truncated, so what
  // is pinned is that the *whole* hash is obtainable: through the link, and
  // through the copy control.
  it('links the hash to a reputation service, with the referrer withheld', async () => {
    await renderTicket([INFECTED, CLEAN])
    const row = rowFor(INFECTED.filename, CLEAN.filename)

    const link = Array.from(row.querySelectorAll('a')).find((a) => a.href.includes(EICAR_SHA))
    expect(
      link?.outerHTML,
      'the row carries no link that looks the hash up at a reputation service',
    ).toBeDefined()

    const rel = link!.getAttribute('rel') ?? ''
    expect(rel, 'the reputation link leaks the referrer').toContain('noreferrer')
    expect(rel, 'the reputation link does not set noopener').toContain('noopener')
  })

  it('copies the whole hash, not the shortened form on screen', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })

    await renderTicket([INFECTED, CLEAN])
    const row = rowFor(INFECTED.filename, CLEAN.filename)

    const copy = within(row).queryByRole('button', { name: /copy/i })
    expect(copy, 'the row has no control for copying the hash').not.toBeNull()
    await user.click(copy!)

    expect(writeText, 'the copy control did not put the full hash on the clipboard').toHaveBeenCalledWith(
      EICAR_SHA,
    )
  })
})

// ── A type mismatch is not a detection ────────────────────────────────────────

describe('an attachment whose content contradicted its name', () => {
  // The single most likely thing to be got wrong: mismatch and infected share
  // a wrapper and share nothing else. Nobody identified this file as anything,
  // and there is no password, so saying either would be false.
  it('is never described as malicious, and claims no password', async () => {
    await renderTicket([MISMATCH])
    const text = bodyText()

    expect(text, 'a file the scanner did not flag is being called malicious').not.toMatch(
      /malicious|malware|virus|infected/i,
    )
    expect(text, 'a file wrapped without a password is said to have one').not.toMatch(/password/i)
  })

  // The quiet treatment is the point of the rarity rule. A mismatch is not
  // worth a modal, and making it raise one is how the modal stops working for
  // the case that needs it.
  it('downloads in one click, with no confirmation', async () => {
    const user = userEvent.setup()
    await renderTicket([MISMATCH])

    const link = downloadLink(MISMATCH.id)
    expect(link, 'a type mismatch has no direct download link').not.toBeNull()
    await user.click(link!)

    expect(
      screen.queryByRole('dialog')?.textContent,
      'a type mismatch raised the malware confirmation dialog',
    ).toBeUndefined()
  })

  // It still has to be visibly different from a file that was inspected and
  // found consistent — otherwise "the content contradicted the name" is a fact
  // the backend records and nobody ever sees. The two fixtures differ only in
  // detected_mime and mismatch, so any difference in the page comes from them.
  it('does not render the same as a file whose content matched', async () => {
    await renderTicket([MISMATCH])
    const flagged = bodyText()
    cleanup()

    await renderTicket([MISMATCH_CONTROL])
    const consistent = bodyText()

    expect(
      flagged,
      'a content/name mismatch renders exactly like a file whose content matched',
    ).not.toBe(consistent)
  })
})

// ── An attachment nobody inspected ────────────────────────────────────────────

describe('an attachment that predates detection', () => {
  // Null is "not recorded". Rendering it the same as a completed, negative
  // scan is the same mistake the backend scanner has now had fixed twice:
  // clean must only ever come from a check that ran.
  it('does not render the same as a file that was inspected and found clean', async () => {
    await renderTicket([UNINSPECTED])
    const unknown = bodyText()
    cleanup()

    await renderTicket([UNINSPECTED_CONTROL])
    const inspected = bodyText()

    expect(
      unknown,
      'an attachment nobody inspected renders exactly like one that was scanned and found clean',
    ).not.toBe(inspected)
  })

  // Not knowing is not the same as knowing it is bad, either. The loud
  // treatment belongs to a stored Infected verdict and nothing else.
  it('raises neither the banner nor a confirmation', async () => {
    const user = userEvent.setup()
    await renderTicket([UNINSPECTED])

    expect(ticketBanner()?.textContent, 'an uninspected file raised the malware banner').toBeUndefined()

    const link = downloadLink(UNINSPECTED.id)
    expect(link, 'an uninspected attachment has no download link').not.toBeNull()
    await user.click(link!)
    expect(
      screen.queryByRole('dialog')?.textContent,
      'an uninspected file raised the malware confirmation dialog',
    ).toBeUndefined()
  })
})

// ── The ordinary case ─────────────────────────────────────────────────────────

describe('a clean attachment', () => {
  // Easy to get right by accident and easy to regress. If any of this starts
  // failing, the warnings have leaked onto the 99% of files that are fine,
  // and every warning on this page is worth less.
  it('carries no warning wording at all', async () => {
    await renderTicket([CLEAN])
    const text = bodyText()

    expect(text, 'a clean attachment is described as malicious').not.toMatch(
      /malicious|malware|virus|infected|quarantin/i,
    )
    expect(text, 'a clean attachment claims to be in a password-protected archive').not.toMatch(
      /password/i,
    )
  })

  it('downloads in one click, with no confirmation', async () => {
    const user = userEvent.setup()
    await renderTicket([CLEAN])

    const link = downloadLink(CLEAN.id)
    expect(link, 'the clean attachment has no download link').not.toBeNull()
    await user.click(link!)

    expect(
      screen.queryByRole('dialog')?.textContent,
      'downloading a clean attachment asked for confirmation',
    ).toBeUndefined()
  })
})
