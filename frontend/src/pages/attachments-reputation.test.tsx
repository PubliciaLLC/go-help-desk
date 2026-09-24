import { describe, expect, it, vi, beforeEach } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Attachment, AttachmentReputation } from '@/api/types'

// The reputation verdict staff read on a quarantined attachment.
//
// Driven through TicketDetailPage rather than a component, like the other two
// attachment suites, because the requirement is about what a person looking at
// a ticket sees. How the page is split up is the implementer's business.
//
// The whole subject is one distinction, made four times:
//
//   detected    N of M engines flagged it. Both numbers, and the provider's
//               name for it when there is one.
//   clean       the provider analysed it and nothing flagged it. Only means
//               anything WITH the denominator — "0 of 78 engines" is a
//               statement, "0 of 0" is a missing lookup in a clean disguise.
//   unseen      the provider has never encountered this file. NOT clean. A
//               file nobody in the world has submitted is more interesting
//               than one with no detections, not less.
//   unscanned   the provider knows the hash and holds no verdict. Not clean.
//   null        nobody asked: no key, no budget, provider down, request
//               failed. "Not checked" — never clean, never a finding.
//
// So most of what is pinned below is negative: which of these must not borrow
// the others' words. A test suite that only checked the happy string would go
// green against an implementation that renders every state as "clean".

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
const DETECTION = 'Eicar-Test-Signature'
// The provider's own consensus name, which is a different string from the
// local scanner's and is the one an analyst searches for.
const THREAT_NAME = 'Win32.Trojan.Agent.ABCD'
const ANALYSED_AT = '2026-09-18T07:30:00Z'

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

function verdict(over: Partial<AttachmentReputation> = {}): AttachmentReputation {
  return {
    state: 'unseen',
    detected: null,
    total: null,
    threat_name: '',
    analysed_at: null,
    ...over,
  }
}

/**
 * A quarantined attachment carrying whichever verdict the test is about.
 *
 * Quarantined because that is the only tier the server ever looks a hash up
 * for: `addReputation` returns early unless VirusName is set.
 */
function quarantined(reputation: AttachmentReputation | null): Attachment {
  return attachment({
    id: 'att-bad',
    filename: 'invoice.exe.zip',
    mime_type: 'application/zip',
    detected_mime: 'application/vnd.microsoft.portable-executable',
    sha256: EICAR_SHA,
    virus_name: DETECTION,
    mismatch: false,
    reputation_url: `https://www.virustotal.com/gui/file/${EICAR_SHA}`,
    reputation,
  })
}

// The second attachment in every render. Its only job is to give rowFor a
// boundary to stop at, so nothing below can be satisfied by text belonging to
// a different attachment or to the page.
const OTHER = attachment({
  id: 'att-ok',
  filename: 'screenshot.png',
  mime_type: 'image/png',
  size_bytes: 20480,
  detected_mime: 'image/png',
  sha256: 'a'.repeat(64),
  mismatch: false,
})

// Inspected, nothing found, and nobody asked a reputation service about it —
// which is every ordinary attachment on every ticket.
const CLEAN = OTHER

// Uploaded before any of this existed. Every verdict field null.
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
    expect(document.body.textContent ?? '').toContain(attachments[0].filename)
  })
  return result
}

/**
 * The part of the page that belongs to one attachment and no other.
 *
 * Copied from attachments-quarantine-warnings.test.tsx on purpose: importing
 * from another test file would run that file's tests a second time. Walks up
 * from the innermost element holding `filename` while the parent stays clear
 * of `otherFilename`, so every assertion below is about this row and cannot be
 * satisfied by a banner elsewhere on the page.
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

/** The text of the quarantined row, for a render of [quarantined, OTHER]. */
function quarantinedRowText(): string {
  return rowFor('invoice.exe.zip', OTHER.filename).textContent ?? ''
}

/**
 * Wording that would tell a reader the provider found nothing wrong.
 *
 * Every state except `clean` has to avoid all of it. Phrased as the claims a
 * wrong implementation actually makes — "0 of", "no engines flagged it",
 * "clean" as a verdict — rather than banning the word `clean` outright, which
 * would forbid an implementation from saying "this is not a clean result".
 */
const REASSURANCE = [
  /\b\d+\s+of\s+\d+/i,
  /\bno engines?\b/i,
  /\bzero engines?\b/i,
  /\b(is|was|looks|found|reported|came back|verdict:)\s+clean\b/i,
  /\bharmless\b/i,
  /\bnothing (was )?(found|flagged|detected)\b/i,
]

function expectNoReassurance(text: string, context: string) {
  for (const re of REASSURANCE) {
    expect(text, `${context}: ${re} matched — this state is not a clean result`).not.toMatch(re)
  }
}

/** Anything that would be a reputation verdict block if it rendered. */
const VERDICT_WORDING =
  /\bengines?\b|never seen|no record|no verdict|not checked|reputation service|not been analysed/i

// ── The provider flagged it ───────────────────────────────────────────────────

describe('a quarantined attachment the reputation service flagged', () => {
  // Both numbers. "3 engines flagged it" without the denominator is missing
  // the half that says how much weight to give it, and "detected" on its own
  // says nothing an analyst can act on.
  it('shows how many engines flagged it, out of how many, and what they call it', async () => {
    await renderTicket([
      quarantined(
        verdict({ state: 'detected', detected: 3, total: 71, threat_name: THREAT_NAME }),
      ),
      OTHER,
    ])
    const text = quarantinedRowText()

    expect(text, 'the row does not say how many engines flagged the file').toMatch(/\b3\b/)
    expect(text, 'the row does not say how many engines ran — 3 alone has no weight').toMatch(
      /\b71\b/,
    )
    expect(text, 'the row never mentions engines, so the numbers mean nothing').toMatch(/engines?/i)
    expect(text, "the row does not carry the provider's name for the threat").toContain(THREAT_NAME)
    // The claim is the service's, not ours. The row above it already
    // attributes the local scanner's verdict the same way.
    expect(
      text,
      'the verdict is stated as fact rather than attributed to whoever said it',
    ).toMatch(/reputation service|provider|looked up|lookup/i)
  })

  // Same rule as virus_name: a malware author picks the filename the engines
  // name the sample after, so the threat name is attacker-influenced text
  // arriving on a page full of staff sessions.
  it('renders the threat name as text, never as markup', async () => {
    const injected = '<b>Trojan</b><img src=x onerror="alert(1)">'
    await renderTicket([
      quarantined(verdict({ state: 'detected', detected: 5, total: 70, threat_name: injected })),
      OTHER,
    ])
    const row = rowFor('invoice.exe.zip', OTHER.filename)

    expect(row.textContent, 'the threat name is not rendered at all').toContain(injected)
    expect(
      row.querySelectorAll('b, img, script').length,
      'the threat name was parsed as markup — this is stored XSS against staff sessions',
    ).toBe(0)
  })

  // When the provider analysed it, not when we asked. A reader deciding
  // whether a verdict predates the sample appearing in the wild needs the
  // provider's date and nothing else.
  it('says when the provider analysed it', async () => {
    await renderTicket([
      quarantined(
        verdict({
          state: 'detected',
          detected: 3,
          total: 71,
          threat_name: THREAT_NAME,
          analysed_at: ANALYSED_AT,
        }),
      ),
      OTHER,
    ])
    const text = quarantinedRowText()

    expect(text, 'the row does not say when the file was analysed').toMatch(/analys/i)
    expect(text, 'the analysis date is not rendered').toMatch(/2026/)
  })

  // threat_name is empty far more often than not: many providers report
  // counts with no consensus name. The counts still have to render, and no
  // placeholder may leak where the name would have been.
  it('still shows the counts when the provider has no name for the threat', async () => {
    await renderTicket([
      quarantined(verdict({ state: 'detected', detected: 2, total: 68, threat_name: '' })),
      OTHER,
    ])
    const text = quarantinedRowText()

    expect(text, 'a verdict with no threat name dropped the counts').toMatch(/\b2\b/)
    expect(text, 'a verdict with no threat name dropped the denominator').toMatch(/\b68\b/)
    expect(text, 'an empty threat name leaked a placeholder into the row').not.toMatch(
      /undefined|\bnull\b|NaN/i,
    )
  })
})

// ── The provider found nothing ────────────────────────────────────────────────

describe('a quarantined attachment the reputation service found clean', () => {
  // The denominator is the requirement. "No detections" is a sentence that
  // reads identically for a file 78 engines examined and for a lookup that
  // returned nothing at all.
  it('states the denominator rather than a bare clean verdict', async () => {
    await renderTicket([
      quarantined(
        verdict({ state: 'clean', detected: 0, total: 78, analysed_at: ANALYSED_AT }),
      ),
      OTHER,
    ])
    const text = quarantinedRowText()

    expect(text, 'the row does not say how many engines ran — "clean" alone is not a result').toMatch(
      /\b78\b/,
    )
    expect(text, 'the row does not say that nothing flagged it').toMatch(/\b0\b/)
    expect(text, 'the row never mentions engines, so 0 and 78 are bare numbers').toMatch(/engines?/i)
    expect(text, 'the row does not say when the provider analysed it').toMatch(/2026/)
  })

  // The tier does not move. The local scanner identified this file; a
  // reputation service with no detections is a second opinion, not an
  // acquittal, and staff must not lose the password, the warning or the
  // confirmation because a third party shrugged.
  it('does not soften the scanner verdict the file was quarantined for', async () => {
    await renderTicket([quarantined(verdict({ state: 'clean', detected: 0, total: 78 })), OTHER])
    const row = rowFor('invoice.exe.zip', OTHER.filename)
    const text = row.textContent ?? ''

    expect(text, 'a clean reputation verdict dropped the scanner detection').toContain(DETECTION)
    expect(text, 'a clean reputation verdict stopped the row calling the file malicious').toMatch(
      /malicious|malware/i,
    )
    expect(text, 'a clean reputation verdict dropped the archive password').toMatch(
      /password[^a-z0-9]{0,20}infected/i,
    )
    expect(
      within(row).queryByRole('button', { name: /download/i }),
      'a clean reputation verdict removed the download confirmation',
    ).not.toBeNull()
  })
})

// ── The provider has never seen it ────────────────────────────────────────────

describe('a quarantined attachment the reputation service has never seen', () => {
  // The one most likely to be got wrong, because "no detections" is what an
  // unseen file superficially looks like. Nobody in the world has submitted
  // this sample — for a file the local scanner already flagged, that is a
  // finding of its own, and it must not read as reassurance.
  it('says the provider has no record of it, and never reads as clean', async () => {
    await renderTicket([quarantined(verdict({ state: 'unseen' })), OTHER])
    const text = quarantinedRowText()

    expect(
      text,
      'the row does not say the provider has never encountered this file',
    ).toMatch(/never seen|no record|has not seen|never encountered/i)
    expect(text, 'the claim is not attributed to whoever made it').toMatch(
      /reputation service|provider|looked up|lookup/i,
    )
    expectNoReassurance(text, 'a file the provider has never seen')
    expect(text, 'an unseen file invented engine counts it does not have').not.toMatch(
      /undefined|\bnull\b|NaN/i,
    )
  })
})

// ── The provider holds no verdict ─────────────────────────────────────────────

describe('a quarantined attachment the reputation service holds no verdict for', () => {
  // Known hash, no analysis. Distinct from unseen — the provider has met this
  // file — and just as distinct from clean.
  it('says there is no verdict, and never reads as clean', async () => {
    await renderTicket([quarantined(verdict({ state: 'unscanned' })), OTHER])
    const text = quarantinedRowText()

    expect(text, 'the row does not say the provider holds no verdict for this file').toMatch(
      /no verdict|not analys|never analys|has not been (scanned|analysed)|no result/i,
    )
    expect(text, 'the claim is not attributed to whoever made it').toMatch(
      /reputation service|provider|looked up|lookup/i,
    )
    expectNoReassurance(text, 'a file the provider holds no verdict for')
    expect(text, 'an unscanned file rendered a placeholder').not.toMatch(/undefined|\bnull\b|NaN/i)
  })

  // unseen and unscanned are two different facts about the world. An
  // implementation that renders one string for "the provider had nothing
  // useful" passes both tests above and loses the distinction, so pin it
  // directly.
  it('does not render identically to a file the provider has never seen', async () => {
    await renderTicket([quarantined(verdict({ state: 'unscanned' })), OTHER])
    const unscanned = quarantinedRowText()
    // The second render would otherwise mount alongside the first, and rowFor
    // would read the same row twice and compare it with itself.
    cleanup()

    await renderTicket([quarantined(verdict({ state: 'unseen' })), OTHER])
    const unseen = quarantinedRowText()

    expect(
      unscanned,
      '"the provider has never seen this file" and "the provider holds no verdict for it" render the same words',
    ).not.toBe(unseen)
  })
})

// ── Nobody asked, and nobody answered ─────────────────────────────────────────
//
// These were one case and are now two, and #168 splits them deliberately:
//
//   null           nothing was attempted. Every provider toggle is off, which
//                  is a supported configuration and a complete answer — the
//                  local scanner decided this file's fate on its own.
//   unavailable    a lookup WAS attempted and did not finish: no key, a spent
//                  budget, a provider that was down, a request that failed.
//
// The wording this suite used to require of the first — "not checked" — is
// what the second says, and saying it of the first invents a failure on every
// instance that never wanted the feature. So the requirement did not soften;
// it moved.

describe('a quarantined attachment nobody asked about', () => {
  // The whole reputation block is absent, the same as it is for an attachment
  // with no verdict today. No warning, no banner, no "not configured" notice:
  // an operator who wants no third-party involvement, or who cannot send
  // customer file hashes anywhere, has made a legitimate choice and the
  // product should not nag them about it.
  it('carries no reputation wording at all', async () => {
    await renderTicket([quarantined(null), OTHER])
    const text = quarantinedRowText()

    expect(
      text,
      'a row nothing was attempted for claims a lookup was made and did not finish',
    ).not.toMatch(VERDICT_WORDING)
    expectNoReassurance(text, 'an attachment nobody looked up')
    expect(text, 'a missing lookup is being reported as a finding').not.toMatch(
      /flagged|detected by|never seen|no record/i,
    )
    expect(text, 'a missing lookup rendered a date or a placeholder').not.toMatch(
      /undefined|\bnull\b|NaN|Invalid Date/i,
    )
  })

  // And the block being absent must not have taken anything of the local
  // scanner's with it, because that is the entire argument for the
  // configuration being supported.
  it('still shows everything the local scanner said', async () => {
    await renderTicket([quarantined(null), OTHER])
    const text = quarantinedRowText()

    expect(text, 'the scanner detection went with the reputation block').toContain(DETECTION)
    expect(text, 'the archive password went with the reputation block').toMatch(
      /password[^a-z0-9]{0,20}infected/i,
    )
  })
})

describe('a quarantined attachment whose lookup did not finish', () => {
  // Absence of evidence, and the failure mode runs in both directions — a
  // failed lookup shown as clean is false comfort, and shown as a finding it
  // is a false alarm on every instance whose key has expired.
  it('reads as not checked — neither a clean result nor a finding', async () => {
    await renderTicket([quarantined(verdict({ state: 'unavailable' })), OTHER])
    const text = quarantinedRowText()

    expect(text, 'a lookup that failed does not say so').toMatch(
      /not checked|no lookup|did not complete|no verdict came back|could not/i,
    )
    expectNoReassurance(text, 'an attachment whose lookup failed')
    expect(
      text,
      'a failed lookup is being reported as a reputation-service finding',
    ).not.toMatch(/flagged|detected by|never seen|no record/i)
    expect(text, 'a failed lookup rendered a date or a placeholder').not.toMatch(
      /undefined|\bnull\b|NaN|Invalid Date/i,
    )
  })
})

// ── Outside the quarantine tier ───────────────────────────────────────────────

describe('attachments outside the quarantine tier', () => {
  // The negative that keeps the block meaningful. Reputation is looked up for
  // quarantined files only, so on any other row there is nothing to say — and
  // "not checked" printed against every holiday-request PDF is noise that
  // teaches staff to skip the line on the row where it matters.
  it('show no verdict block at all', async () => {
    await renderTicket([CLEAN, UNINSPECTED])

    expect(
      rowFor(CLEAN.filename, UNINSPECTED.filename).textContent ?? '',
      'a clean attachment carries reputation wording',
    ).not.toMatch(VERDICT_WORDING)
    expect(
      rowFor(UNINSPECTED.filename, CLEAN.filename).textContent ?? '',
      'an attachment nobody inspected carries reputation wording',
    ).not.toMatch(VERDICT_WORDING)
  })

  // And it stays that way even if a verdict turns up on one — the verdict is
  // additive to the infected tier, not a fourth thing rows can grow. Without
  // this the test above passes against an implementation that renders the
  // block on every row, purely because the field is null there today.
  it('show none even when a verdict arrives on one', async () => {
    const smuggled = attachment({
      ...CLEAN,
      reputation: verdict({
        state: 'detected',
        detected: 9,
        total: 70,
        threat_name: THREAT_NAME,
      }),
    })
    await renderTicket([smuggled, UNINSPECTED])
    const text = rowFor(smuggled.filename, UNINSPECTED.filename).textContent ?? ''

    expect(text, 'a verdict rendered on a row outside the quarantine tier').not.toMatch(
      VERDICT_WORDING,
    )
    expect(text, 'a threat name rendered on a row outside the quarantine tier').not.toContain(
      THREAT_NAME,
    )
  })
})

// ── The page as a whole ───────────────────────────────────────────────────────

describe('the reputation verdict', () => {
  // Scoped assertions can all pass while the block sits somewhere nobody
  // reads. It belongs with the hash and its lookup link, which is where an
  // analyst is already looking.
  it('sits with the hash and its lookup link', async () => {
    await renderTicket([
      quarantined(verdict({ state: 'detected', detected: 3, total: 71, threat_name: THREAT_NAME })),
      OTHER,
    ])
    const row = rowFor('invoice.exe.zip', OTHER.filename)

    const link = Array.from(row.querySelectorAll('a')).find((a) => a.href.includes(EICAR_SHA))
    expect(link, 'the quarantined row lost its reputation lookup link').toBeDefined()

    // The nearest common ancestor of the hash line and the verdict must be
    // inside the row rather than the row itself — i.e. they sit together, not
    // at opposite ends of it.
    const hashLine = link!.closest('p')
    expect(hashLine, 'the lookup link is not on a line of its own').not.toBeNull()

    const verdictText = screen.queryAllByText(new RegExp(THREAT_NAME.replace(/\./g, '\\.')))
    expect(verdictText.length, 'the threat name is not rendered as text').toBeGreaterThan(0)

    const siblings = Array.from(hashLine!.parentElement?.children ?? [])
    expect(
      siblings.some((el) => (el.textContent ?? '').includes(THREAT_NAME)),
      'the verdict is not next to the hash and its lookup link',
    ).toBe(true)
  })
})
