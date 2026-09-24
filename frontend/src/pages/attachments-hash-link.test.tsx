import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Attachment } from '@/api/types'

// The hash link, and the line of copy under it, on the rows that get one.
//
// The server used to send `reputation_url` on every attachment carrying a
// hash, which since content detection landed is all of them — so a
// holiday-request PDF on a printer ticket carried a link to VirusTotal and a
// grey sentence explaining it. It now sends the link only where this instance
// itself found something worth a second opinion:
//
//   the scanner named the file            → quarantined
//   the content contradicts the name      → wrapped, or merely flagged
//
// Everything else gets `reputation_url: null`. The HASH is still sent and must
// still be shown — it is a fact about the file rather than a claim by anybody,
// and an analyst with their own account pastes it where they like.
//
// Two rules, and each fails in its own direction:
//
//   the note follows the link   A sentence explaining a link that is not there
//                               is worse than no sentence, and a grey line
//                               under every clean attachment is exactly the
//                               noise this change removes. It exists to stop an
//                               operator who switched VirusTotal off concluding
//                               the setting is broken — which is a thing to say
//                               beside a link and nothing at all beside a row
//                               without one.
//   the server decides          The frontend renders the field it was sent and
//                               does not re-derive the rule from virus_name and
//                               mismatch. A second copy of that rule here is a
//                               copy that drifts, and the drift is silent.
//
// Driven through TicketDetailPage rather than the component, like the sibling
// attachment suites: the requirement is about what a person looking at a ticket
// sees, and how the page is split up is the implementer's business.

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

const EICAR_SHA = '275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f'
const CLEAN_SHA = 'a'.repeat(64)
const WRAPPED_SHA = 'b'.repeat(64)
const FLAGGED_SHA = 'c'.repeat(64)
const DETECTION = 'Eicar-Test-Signature'

function link(sha: string): string {
  return `https://www.virustotal.com/gui/file/${sha}`
}

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
    reputation_url: null,
    reputation: null,
    ...over,
  }
}

// The scanner named it. Wrapped with the password `infected`, and the server
// sends a link.
const QUARANTINED = attachment({
  id: 'att-bad',
  filename: 'invoice.exe.zip',
  mime_type: 'application/zip',
  detected_mime: 'application/x-dosexec',
  sha256: EICAR_SHA,
  virus_name: DETECTION,
  mismatch: false,
  reputation_url: link(EICAR_SHA),
})

// The content contradicted the name and the type it turned out to be is not one
// this instance accepts, so it was wrapped under a name we generated. The
// scanner found nothing. The server sends a link: nothing looked this file up,
// which makes the link the only outside opinion available on it.
const WRAPPED_MISMATCH = attachment({
  id: 'att-sus',
  filename: 'suspicious-1a2b3c4d.zip',
  mime_type: 'application/zip',
  detected_mime: 'application/x-dosexec',
  sha256: WRAPPED_SHA,
  virus_name: null,
  mismatch: true,
  reputation_url: link(WRAPPED_SHA),
})

// The content contradicted the name and the type it turned out to be IS
// accepted here — HTML in a .log on an instance that allows .html — so it was
// stored under its own name and only flagged. A weaker signal, and still the
// row where a curious person would check.
const FLAGGED_MISMATCH = attachment({
  id: 'att-log',
  filename: 'capture.log',
  mime_type: 'text/plain',
  size_bytes: 9000,
  detected_mime: 'text/html',
  sha256: FLAGGED_SHA,
  virus_name: null,
  mismatch: true,
  reputation_url: link(FLAGGED_SHA),
})

// Content matching its name, scanner passed. Every ticket is mostly these.
const ORDINARY = attachment({
  id: 'att-ok',
  filename: 'screenshot.png',
  mime_type: 'image/png',
  size_bytes: 20480,
  detected_mime: 'image/png',
  sha256: CLEAN_SHA,
  mismatch: false,
})

// Uploaded before any of this existed: no hash, so nothing to link to and
// nothing to show. Not a clean bill of health — nobody looked.
const UNINSPECTED = attachment({
  id: 'att-old',
  filename: 'notes.txt',
  mime_type: 'text/plain',
  size_bytes: 300,
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
  // Staff, not admin: an admin also fires the security-warnings query, whose
  // banner would show up in the page-wide assertions.
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
  await waitFor(() => {
    expect(bodyText()).toContain(attachments[0].filename)
  })
  return result
}

function bodyText(): string {
  return document.body.textContent ?? ''
}

/**
 * The part of the page that belongs to one attachment and no other.
 *
 * Copied from the sibling suites on purpose: importing from another test file
 * would run that file's tests a second time. Walks up from the innermost
 * element holding `filename` while the parent stays clear of `otherFilename`,
 * so every assertion below is about this row and cannot be satisfied by text
 * belonging to a different attachment or to the page.
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

/** Every link on a row that points at a hash report. */
function hashLinks(row: HTMLElement): HTMLAnchorElement[] {
  return Array.from(row.querySelectorAll('a')).filter((a) =>
    /virustotal\.com/i.test(a.getAttribute('href') ?? ''),
  )
}

/**
 * The explanatory line, in any phrasing.
 *
 * Deliberately a union rather than the exact sentence: what must not appear
 * under a linkless row is the explanation, however it is worded, and pinning
 * one spelling would let a rewrite of it slip past. Every branch is something
 * only this note says — the row's own text never mentions a browser, never
 * says anything is sent, and never names a service.
 */
const NOTE_WORDING = /opens in (your|the|their)[^.]*browser|nothing is sent|sends nothing|switched on in the admin settings/i

/** Anything that names the service the link goes to. */
const SERVICE_NAMED = /virustotal/i

function expectHashIsShown(row: HTMLElement, sha: string) {
  const text = row.textContent ?? ''
  expect(text, 'the row no longer labels the hash').toMatch(/sha-?256/i)
  expect(text, 'the hash itself is not rendered').toContain(sha.slice(0, 12))
  expect(
    within(row).queryAllByRole('button', { name: /copy/i }).length,
    'the hash lost its copy control, which is how it reaches an analyst’s notes',
  ).toBe(1)
}

function expectLinkAndNote(row: HTMLElement, sha: string) {
  const links = hashLinks(row)
  expect(links.length, 'the row the server sent a link for does not render one').toBe(1)
  expect(links[0].getAttribute('href'), 'the link does not point at this file’s hash').toContain(sha)
  // Opened in the reader's own browser, carrying nothing of this instance with
  // it — which is the claim the note beside it makes.
  expect(links[0].getAttribute('rel')).toMatch(/noreferrer/)
  expect(links[0].getAttribute('rel')).toMatch(/noopener/)

  expect(
    row.textContent ?? '',
    'the link is there and the line explaining it is not — the note exists to stop an ' +
      'operator who switched VirusTotal off reading the link as a broken setting',
  ).toMatch(NOTE_WORDING)
}

function expectNoLinkAndNoNote(row: HTMLElement) {
  const text = row.textContent ?? ''
  expect(
    hashLinks(row).map((a) => a.getAttribute('href')),
    'a row the server sent no link for rendered one anyway',
  ).toEqual([])
  expect(
    text,
    'the note explaining the link is under a row that has no link — a grey line on every ' +
      'clean attachment is the noise this change removes',
  ).not.toMatch(NOTE_WORDING)
  expect(text, 'a row with no link still names the service it would have gone to').not.toMatch(
    SERVICE_NAMED,
  )
}

// ── The rows that get a link ──────────────────────────────────────────────────

describe('an attachment this instance found something on', () => {
  it('shows the hash, the link and the line that explains it — scanner detection', async () => {
    await renderTicket([QUARANTINED, ORDINARY])
    const row = rowFor(QUARANTINED.filename, ORDINARY.filename)

    expectHashIsShown(row, EICAR_SHA)
    expectLinkAndNote(row, EICAR_SHA)
  })

  // No virus name anywhere on this row. A frontend that gated the link on the
  // scanner verdict — the obvious wrong rule, and the one the quarantined case
  // above cannot tell apart from the right one — fails here.
  it('shows the hash, the link and the line that explains it — a wrapped mismatch', async () => {
    await renderTicket([WRAPPED_MISMATCH, ORDINARY])
    const row = rowFor(WRAPPED_MISMATCH.filename, ORDINARY.filename)

    expect(row.textContent ?? '', 'the fixture is not the case this test is about').not.toContain(
      DETECTION,
    )
    expectHashIsShown(row, WRAPPED_SHA)
    expectLinkAndNote(row, WRAPPED_SHA)
  })

  // Flagged and stored under its own name: not wrapped, not quarantined, and
  // still a row the server sends a link for.
  it('shows the hash, the link and the line that explains it — a flagged mismatch', async () => {
    await renderTicket([FLAGGED_MISMATCH, ORDINARY])
    const row = rowFor(FLAGGED_MISMATCH.filename, ORDINARY.filename)

    expectHashIsShown(row, FLAGGED_SHA)
    expectLinkAndNote(row, FLAGGED_SHA)
  })
})

// ── The rows that do not ──────────────────────────────────────────────────────

describe('an ordinary attachment', () => {
  // The whole point of the change, and both halves of it. The hash stays
  // because it is a fact about the file; the link and its explanation go
  // because this instance found nothing to have a second opinion about.
  it('keeps its hash and loses the link and the note with it', async () => {
    await renderTicket([ORDINARY, UNINSPECTED])
    const row = rowFor(ORDINARY.filename, UNINSPECTED.filename)

    expectHashIsShown(row, CLEAN_SHA)
    expectNoLinkAndNoNote(row)
  })

  // Scoped assertions cannot see a note rendered once for the list rather than
  // once per row, which is the shape a "fix" that hoists it out of the row
  // would take.
  it('leaves the explanation off the page entirely when no row has a link', async () => {
    await renderTicket([ORDINARY, UNINSPECTED])

    expect(bodyText(), 'the note is on the page with no link anywhere near it').not.toMatch(
      NOTE_WORDING,
    )
    expect(bodyText()).not.toMatch(SERVICE_NAMED)
  })

  // An attachment old enough to have no hash has nothing to show and nothing
  // to link to. It must not acquire a placeholder, a link to an empty report,
  // or the note.
  it('shows nothing at all for an attachment nobody inspected', async () => {
    await renderTicket([UNINSPECTED, ORDINARY])
    const row = rowFor(UNINSPECTED.filename, ORDINARY.filename)

    expect(row.textContent ?? '', 'a row with no hash rendered a hash line').not.toMatch(/sha-?256/i)
    expect(row.textContent ?? '', 'a row with no hash rendered a placeholder').not.toMatch(
      /undefined|\bnull\b|NaN/i,
    )
    expectNoLinkAndNoNote(row)
  })
})

// ── Whose decision it is ──────────────────────────────────────────────────────

describe('which rows get a link', () => {
  // The server's finding, not the frontend's re-derivation of it. The rule is
  // three conditions on the Go side and it will grow a fourth; a copy of it
  // here would go stale silently, because both copies look right in isolation.
  //
  // So: a row with no detection and no mismatch, which the server nonetheless
  // sent a link for, renders the link. An implementation that recomputes
  // `worthLookingUp` from virus_name and mismatch drops it.
  it('is the server’s answer, not one worked out again from the row', async () => {
    const decided = attachment({ ...ORDINARY, reputation_url: link(CLEAN_SHA) })
    await renderTicket([decided, UNINSPECTED])
    const row = rowFor(decided.filename, UNINSPECTED.filename)

    expectHashIsShown(row, CLEAN_SHA)
    expectLinkAndNote(row, CLEAN_SHA)
  })

  // And the other direction, which is the one that actually ships: a
  // quarantined row the server sent no link for — every provider off is not
  // the condition, but a future one might be — shows no link and no note,
  // while keeping everything the local scanner said.
  it('drops both when the server sends no link, even on a quarantined row', async () => {
    const silent = attachment({ ...QUARANTINED, reputation_url: null })
    await renderTicket([silent, ORDINARY])
    const row = rowFor(silent.filename, ORDINARY.filename)

    expectHashIsShown(row, EICAR_SHA)
    expectNoLinkAndNoNote(row)

    const text = row.textContent ?? ''
    expect(text, 'the scanner detection went with the link').toContain(DETECTION)
    expect(text, 'the archive password went with the link').toMatch(
      /password[^a-z0-9]{0,20}infected/i,
    )
    expect(
      within(row).queryByRole('button', { name: /download/i }),
      'the download confirmation went with the link',
    ).not.toBeNull()
  })
})
