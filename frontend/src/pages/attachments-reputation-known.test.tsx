import { describe, expect, it, vi, beforeEach } from 'vitest'
import { cleanup, waitFor, within } from '@testing-library/react'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Attachment, AttachmentReputation } from '@/api/types'

// `known`: a named catalogue has this exact hash on file.
//
// It is the sixth state and the only one in this whole feature where
// reassurance is the correct rendering — everywhere else an absence must never
// read as safety. It earns that because a named feed made a positive claim,
// which is exactly what `clean`, `unseen` and `unscanned` do not have behind
// them.
//
// And the name of the feed is not decoration. The state deliberately does not
// say how strong the claim is, because that varies entirely by feed:
//
//   microsoft_windows   an Authenticode signature assertion — the file is
//                       signed and trusted.
//   nsrl                the hash appears in the US National Software
//                       Reference Library, which catalogues files found in
//                       known software distributions INCLUDING hacking tools.
//                       Catalogued is not safe.
//
// So a renderer that prints the state without naming the feed is making a
// claim the data does not support, and most of what is pinned below is that:
// the feed is named, two different feeds do not render as one sentence, a feed
// this build has no label for is still shown rather than dropped, and `known`
// never borrows `clean`'s vocabulary of engines and counts.
//
// The other half is the re-check control. `known` is final — a file does not
// stop being a signed Microsoft binary — so the server refuses a re-check of
// one with 409, and a control that appeared here would be a button that always
// fails. Absent, exactly as on `detected`.
//
// Driven through TicketDetailPage like the sibling attachment suites: the
// requirement is about what a person looking at a ticket sees, and how the
// page is split up is the implementer's business.

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

const DAY = 24 * 60 * 60 * 1000

/** An ISO timestamp n days before the moment the test runs. */
function daysAgo(n: number): string {
  return new Date(Date.now() - n * DAY).toISOString()
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

function verdict(over: Partial<AttachmentReputation> = {}): AttachmentReputation {
  return {
    state: 'unseen',
    detected: null,
    total: null,
    threat_name: '',
    analysed_at: null,
    // PolySwarm is the only provider in this build that answers `known` — the
    // answer comes out of its KNOWN_GOOD catalogue lookup — so that is the
    // attribution a real `known` verdict arrives with.
    provider: 'polyswarm',
    // Deliberately ancient in the default. Every assertion below about the
    // re-check control has to be about the STATE, not about the seven-day
    // floor happening to hold the control shut.
    fetched_at: daysAgo(400),
    known_feeds: [],
    ...over,
  }
}

/** A `known` verdict vouched for by the given feeds. */
function known(feeds: string[], over: Partial<AttachmentReputation> = {}): AttachmentReputation {
  return verdict({ state: 'known', known_feeds: feeds, ...over })
}

/** A quarantined attachment carrying whichever verdict the test is about. */
function quarantined(reputation: AttachmentReputation | null): Attachment {
  return attachment({
    id: 'att-bad',
    filename: 'invoice.exe.zip',
    mime_type: 'application/zip',
    detected_mime: 'application/vnd.microsoft.portable-executable',
    sha256: EICAR_SHA,
    virus_name: DETECTION,
    mismatch: false,
    reputation_url: `https://polyswarm.network/scan/results/file/${EICAR_SHA}`,
    reputation,
  })
}

// The second attachment in every render, so rowFor has a boundary to stop at
// and nothing below can be satisfied by text belonging elsewhere on the page.
const OTHER = attachment({
  id: 'att-ok',
  filename: 'screenshot.png',
  mime_type: 'image/png',
  size_bytes: 20480,
  detected_mime: 'image/png',
  sha256: 'a'.repeat(64),
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
 * Copied from the sibling suites on purpose: importing from another test file
 * would run that file's tests a second time.
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

function quarantinedRow(): HTMLElement {
  return rowFor('invoice.exe.zip', OTHER.filename)
}

function quarantinedRowText(): string {
  return quarantinedRow().textContent ?? ''
}

/** Render the quarantined row for one verdict and hand back its text. */
async function textFor(reputation: AttachmentReputation): Promise<string> {
  await renderTicket([quarantined(reputation), OTHER])
  return quarantinedRowText()
}

/**
 * The re-check control on the quarantined row, however it is labelled.
 *
 * Matched on what it does rather than on one exact string, and links count
 * too — an implementation that made this a link would still have to satisfy
 * everything below. The download confirmation trigger lives on this row as
 * well and must not be mistaken for it.
 */
function recheckControl(): HTMLElement | null {
  const row = quarantinedRow()
  const wanted = /check again|re-?check|ask again|refresh|look up again|check now/i
  const candidates = [
    ...within(row).queryAllByRole('button'),
    ...within(row).queryAllByRole('link'),
  ].filter((el) => {
    const name = `${el.textContent ?? ''} ${el.getAttribute('aria-label') ?? ''}`
    return wanted.test(name) && !/download/i.test(name)
  })
  return candidates[0] ?? null
}

/**
 * Wording that claims engines ran on this file.
 *
 * `known` is answered straight out of a hash match — nothing is scanned at all
 * — so any of this on a `known` row is a fabricated analysis attached to the
 * one verdict staff are entitled to find reassuring. It is also the exact
 * vocabulary `clean` owns, which is the collapse this state exists to prevent.
 */
const ENGINE_WORDING = [/\b\d+\s+of\s+\d+\b/, /\bengines?\b/i, /\bflagged this file\b/i]

function expectNoEngineClaim(text: string, context: string) {
  for (const re of ENGINE_WORDING) {
    expect(text, `${context}: ${re} matched — nothing was scanned, the hash was matched`).not.toMatch(
      re,
    )
  }
}

/**
 * Placeholders that mean a field leaked instead of rendering.
 *
 * `NaN` is word-bounded and case-sensitive, unlike everything else here, and
 * both are deliberate. Case-insensitive and unanchored it matches "finance"
 * and "maintenance" — ordinary words in copy about files and services — so the
 * first person to write a sentence containing one would get a failure claiming
 * a number had leaked. A trap in a helper is worse than no helper: it fails on
 * correct work, and the fix a reader reaches for is to delete the check.
 */
const PLACEHOLDER = /undefined|\bnull\b|\bNaN\b|Invalid Date|\[object Object\]/

// ── The feed is the claim ─────────────────────────────────────────────────────

describe('a quarantined attachment a named catalogue vouches for', () => {
  // The whole requirement in one test. "Known file" on its own is a claim
  // from nowhere; the feed is what gives it a source and a weight.
  it('names the feed that has the hash on file, and attributes it', async () => {
    const text = await textFor(known(['microsoft_windows']))

    expect(text, 'the row does not say the file is known to a catalogue').toMatch(
      /\bknown\b|\bcatalogue/i,
    )
    expect(
      text,
      'the row names no feed, so it claims more than the data supports',
    ).toMatch(/Microsoft Windows/i)
    expect(text, 'the claim is not attributed to whoever made it').toMatch(
      /PolySwarm|reputation service|provider/i,
    )
    expect(text, 'the verdict rendered a placeholder').not.toMatch(PLACEHOLDER)
  })

  // NSRL catalogues files found in known software distributions, hacking
  // tools included. Rendering it with the same sentence as a signature
  // assertion tells a reader the file is trusted when the data says only that
  // somebody has seen it before.
  it('does not describe an NSRL catalogue entry as a signature', async () => {
    const text = await textFor(known(['nsrl']))

    expect(text, 'the row does not name NSRL').toMatch(/NSRL/i)
    expect(
      text,
      'an NSRL catalogue entry is described as a signature — NSRL catalogues hacking tools',
    ).not.toMatch(/\bsigned\b|\bsignature\b/i)
  })

  // The two feeds are different claims. An implementation that maps every
  // feed to one sentence passes both tests above and loses the distinction
  // that is the entire reason the feeds are on the wire.
  it('does not render a Microsoft signature and an NSRL entry as the same sentence', async () => {
    const signed = await textFor(known(['microsoft_windows']))
    // The next render would otherwise mount alongside this one, and rowFor
    // would read the same row twice and compare it with itself.
    cleanup()
    const catalogued = await textFor(known(['nsrl']))

    expect(
      signed,
      'a signed Microsoft binary and an NSRL catalogue entry render the same words',
    ).not.toBe(catalogued)
  })

  // Feeds this build has no label for keep arriving as the provider adds
  // them. A feed we cannot name prettily is still evidence, and dropping it
  // leaves a bare "known file" — the claim from nowhere this state is
  // designed not to make.
  it('shows a feed it has no label for rather than dropping it', async () => {
    const text = await textFor(known(['acme_golden_image']))

    expect(text, 'an unrecognised feed was dropped, leaving a claim with no source').toMatch(
      /acme/i,
    )
    expect(text, 'the unrecognised feed did not render readably').toMatch(/golden[ _-]?image/i)
    expect(text, 'an unrecognised feed rendered a placeholder').not.toMatch(PLACEHOLDER)
  })

  // The provider sorts and de-duplicates them and sends all of them, because
  // two catalogues vouching is a different fact from one.
  it('names every feed when more than one vouches for the file', async () => {
    const text = await textFor(known(['microsoft_windows', 'nsrl']))

    expect(text, 'the first feed is missing').toMatch(/Microsoft Windows/i)
    expect(text, 'the second feed is missing — only one of two catalogues is named').toMatch(/NSRL/i)
  })

  // Feed names reach the page from the provider's API. They are a far smaller
  // surface than a threat name, but this row escapes everything else it
  // renders and this must not be the exception.
  it('renders a feed name as text, never as markup', async () => {
    await renderTicket([quarantined(known(['<img src=x onerror="alert(1)">'])), OTHER])
    const row = quarantinedRow()

    expect(
      row.querySelectorAll('img, script, b').length,
      'a feed name was parsed as markup — this is stored XSS against staff sessions',
    ).toBe(0)
    expect(row.textContent ?? '', 'the feed name was dropped rather than escaped').toContain(
      'onerror',
    )
  })
})

// ── The feed name arrives in more than one spelling ───────────────────────────

describe('the spelling of a feed name', () => {
  // We do not know with certainty which spelling the wire carries. PolySwarm's
  // own API documentation gives the example list as `['Microsoft Windows']` —
  // spaces and capitals — while the research this build was written against
  // recorded `microsoft_windows`. Neither has been seen on a live response, so
  // both are pinned: a renderer that handles only the one it happened to be
  // written against is a coin flip.
  //
  // And it misses in the damaging direction. The fallback arm says "catalogued
  // by Microsoft Windows", which is deliberately the weaker verb — right for a
  // feed nobody recognises, and wrong here, because an Authenticode signature
  // assertion silently downgraded to a catalogue entry is exactly the
  // information loss the feed names exist to prevent. It is invisible, too:
  // the sentence still names the feed and still reads plausibly.
  const spellings = ['microsoft_windows', 'Microsoft Windows', 'microsoft-windows']

  for (const feed of spellings) {
    it(`reads ${feed} as a signature, not a catalogue entry`, async () => {
      const text = await textFor(known([feed]))

      expect(text, `${feed} was not recognised, so the claim lost its verb`).toMatch(/\bsigned\b/i)
      expect(text, `${feed} fell through to the weaker fallback wording`).not.toMatch(
        /catalogued by Microsoft/i,
      )
      expect(text, 'the feed name is missing from the sentence').toMatch(/Microsoft Windows/i)
    })
  }

  // The normalisation must not swallow genuinely unknown feeds into a
  // recognised one, and the fallback for those is right as it stands.
  it('still falls back for a feed that is not one of the known ones', async () => {
    const text = await textFor(known(['Acme Golden Image']))

    expect(text, 'an unrecognised feed borrowed a signature claim it has not earned').not.toMatch(
      /\bsigned\b|\bsignature\b/i,
    )
    expect(text, 'an unrecognised feed was dropped, leaving a claim with no source').toMatch(
      /Acme Golden Image/i,
    )
  })
})

// ── It is not `clean`, and must never read like it ────────────────────────────

describe('a known file against a clean one', () => {
  // "Engines ran and found nothing" and "this is a catalogued Microsoft
  // binary" are different claims, and the second is far stronger. Collapsing
  // them loses the answer an IT team triaging a reported .exe actually wants.
  it('does not render a known file in the words of a clean verdict', async () => {
    const text = await textFor(known(['microsoft_windows']))
    expectNoEngineClaim(text, 'a file answered out of a catalogue')
  })

  it('does not render identically to a clean verdict', async () => {
    const catalogued = await textFor(known(['microsoft_windows']))
    cleanup()
    const clean = await textFor(
      verdict({ state: 'clean', detected: 0, total: 78, analysed_at: '2026-09-18T07:30:00Z' }),
    )

    expect(
      catalogued,
      'a catalogued file and a file 78 engines found nothing in render the same words',
    ).not.toBe(clean)
  })

  // The state carries no counts on the wire — the server sends null for both,
  // on purpose — so an implementation that reaches for them has to invent
  // them, and "0 of 0 engines" is a missing lookup wearing a clean verdict's
  // clothes.
  it('invents no engine counts for a file that was never scanned', async () => {
    const text = await textFor(known(['nsrl']))

    expect(text, 'a known verdict rendered a placeholder where a count would be').not.toMatch(
      PLACEHOLDER,
    )
    expect(text, 'a known verdict fabricated a zero-of-zero analysis').not.toMatch(/\b0\s+of\s+0\b/)
  })
})

// ── A claim with no source is not reassurance ─────────────────────────────────

describe('a known verdict with no feed named', () => {
  // Reachable: PolySwarm answers KNOWN_GOOD and the catalogue entries carry
  // no tool name, so the server drops them and sends an empty list. The state
  // then says somebody has the hash on file and cannot say who — which is the
  // claim from nowhere this feature refuses everywhere else, so it must not
  // get the reassuring rendering that a named feed earns.
  it('does not read as reassurance when nothing is named', async () => {
    const text = await textFor(known([]))

    expect(
      text,
      'a known verdict with no feed named still asserts the file is signed or trusted',
    ).not.toMatch(/\bsigned\b|\bsignature\b|\btrusted\b|\bknown good\b/i)
    expect(text, 'an empty feed list rendered a placeholder').not.toMatch(PLACEHOLDER)
    expectNoEngineClaim(text, 'a known verdict with no feed named')
  })

  // And it is still a different fact from a file nobody looked up.
  it('does not render as a file with no lookup at all', async () => {
    const nameless = await textFor(known([]))
    cleanup()
    const nothing = await textFor(verdict({ state: 'unseen' }))

    expect(
      nameless,
      'a nameless known verdict renders as a file the provider has never seen',
    ).not.toBe(nothing)
  })
})

// ── The verdict is final, so there is nothing to ask again ────────────────────

describe('the re-check control on a known file', () => {
  // The anchor for the two absences below. They are negatives, and a negative
  // that passes because the helper cannot find ANY control is not a test at
  // all — so prove first that a control on this exact row, under this exact
  // harness, is findable when the state is one that decays.
  it('is found by this suite when the verdict is one that decays', async () => {
    await renderTicket([
      quarantined(verdict({ state: 'clean', detected: 0, total: 78, fetched_at: daysAgo(400) })),
      OTHER,
    ])

    expect(
      recheckControl(),
      'this suite cannot find a re-check control at all, so the absences below prove nothing',
    ).not.toBeNull()
  })

  // A file does not stop being a signed Microsoft binary. `known` is exempt
  // from automatic expiry and from the manual re-check alike, and the server
  // refuses one with 409 — so a control here would be a button that always
  // fails. Absent, exactly as on a detection.
  it('is absent, however old the verdict is', async () => {
    await renderTicket([quarantined(known(['microsoft_windows'], { fetched_at: daysAgo(900) })), OTHER])

    expect(
      recheckControl()?.outerHTML,
      'a known verdict offers a re-check, which the server refuses with 409',
    ).toBeUndefined()
  })

  // The absence must be the state's doing and not the seven-day floor's. A
  // verdict fetched a year ago clears the floor comfortably, so an
  // implementation that only ever disables the control would show an armed
  // one here.
  it('is absent even on a verdict fetched long after the seven-day floor cleared', async () => {
    await renderTicket([quarantined(known(['nsrl'], { fetched_at: daysAgo(400) })), OTHER])

    expect(
      recheckControl()?.outerHTML,
      'the control is held shut by the floor rather than absent because the verdict is final',
    ).toBeUndefined()
    expect(
      quarantinedRowText(),
      'the row offers a date on which a final verdict can be asked about again',
    ).not.toMatch(/can be checked again|check(ed)? again on/i)
  })
})

// ── The tier does not move ────────────────────────────────────────────────────

describe('a known verdict on a quarantined file', () => {
  // The reassuring reading is about the reputation service, not about the
  // local scanner. Our own scanner identified this file; a catalogue entry is
  // a strong second opinion and still not an acquittal, and staff must not
  // lose the password, the warning or the confirmation because of it.
  it('does not soften the scanner verdict the file was quarantined for', async () => {
    await renderTicket([quarantined(known(['microsoft_windows'])), OTHER])
    const row = quarantinedRow()
    const text = row.textContent ?? ''

    expect(text, 'a known verdict dropped the scanner detection').toContain(DETECTION)
    expect(text, 'a known verdict stopped the row calling the file malicious').toMatch(
      /malicious|malware/i,
    )
    expect(text, 'a known verdict dropped the archive password').toMatch(
      /password[^a-z0-9]{0,20}infected/i,
    )
    expect(
      within(row).queryByRole('button', { name: /download/i }),
      'a known verdict removed the download confirmation',
    ).not.toBeNull()
  })
})

// ── A local detection outranks any catalogue ──────────────────────────────────

/**
 * Colours this page uses to say "this is fine".
 *
 * A family rather than one Tailwind shade, because the requirement is about
 * what the line communicates: a rewrite that swaps emerald for green has
 * changed nothing.
 */
const REASSURING = ['emerald', 'green', 'teal', 'lime']

/**
 * The colour family of the line carrying a given piece of wording.
 *
 * The treatment is half the finding, not only the words — a sentence that
 * reads as context is still reassurance if it is painted in the colour this
 * page reserves for good news.
 */
function verdictColour(row: HTMLElement, wording: RegExp): string {
  const lines = Array.from(row.querySelectorAll<HTMLElement>('p')).filter((el) =>
    wording.test(el.textContent ?? ''),
  )
  expect(lines.length, `expected exactly one line on the row matching ${wording}`).toBe(1)

  const colour = (lines[0].getAttribute('class') ?? '').match(/\btext-([a-z]+)-\d{3}\b/)
  expect(colour, 'the verdict line carries no text colour at all').not.toBeNull()
  return colour![1]
}

describe('a catalogue entry on a file our own scanner named', () => {
  // EICAR is in NSRL. So on an instance with CIRCL enabled, the file ClamAV
  // quarantined — wrapped, password-protected, with the whole loud treatment
  // above it — carried an emerald line reading "CIRCL: known file —
  // catalogued by NSRL". That is the product reassuring staff about a file it
  // has itself identified as malicious.
  //
  // NSRL cataloguing means the file appeared in a known software
  // distribution; NSRL catalogues hacking tools and test files. It was never a
  // statement that a file is safe. The rule: a local scanner detection
  // outranks any external catalogue, so the entry is still shown — "NSRL has
  // this on file" is real information about a sample an analyst is triaging —
  // and shown as context rather than as a second opinion that overrules the
  // scanner.
  const nsrl = () => known(['nsrl'], { provider: 'circl' })

  it('does not render the catalogue hit as reassurance', async () => {
    await renderTicket([quarantined(nsrl()), OTHER])
    const colour = verdictColour(quarantinedRow(), /\bknown\b/i)

    expect(
      REASSURING,
      'a file the scanner quarantined carries a reassuring catalogue line — EICAR itself ' +
        'is in NSRL, so this is the product vouching for its own detection',
    ).not.toContain(colour)
  })

  // The row already has a treatment for a `known` verdict that has not earned
  // reassurance: the one with no feed named. This is the same situation from
  // the other direction — the claim is sourced, and the scanner outranks it —
  // so it is the same treatment and not a third one.
  it('renders it in the treatment the row already uses for a known verdict that is not reassurance', async () => {
    await renderTicket([quarantined(nsrl()), OTHER])
    const named = verdictColour(quarantinedRow(), /\bknown\b/i)
    cleanup()

    await renderTicket([quarantined(known([], { provider: 'circl' })), OTHER])
    const nameless = verdictColour(quarantinedRow(), /\bknown\b/i)

    expect(
      named,
      'the two known verdicts a quarantined row can carry are painted differently',
    ).toBe(nameless)
  })

  // Demoted, not deleted. An analyst triaging a sample wants to know a
  // catalogue has the hash on file, and which one — a fix that drops the line
  // throws away the information along with the reassurance.
  it('still names the catalogue and who reported it', async () => {
    const text = await textFor(nsrl())

    expect(text, 'the catalogue entry was dropped rather than demoted').toMatch(/NSRL/i)
    expect(text, 'the catalogue entry lost the service that reported it').toContain('CIRCL')
    expect(text, 'the demoted line rendered a placeholder').not.toMatch(PLACEHOLDER)
  })

  // Context, not a verdict on the file. Nothing here may say the file is all
  // right: our own scanner said the opposite and it is the one that ran on
  // these bytes.
  it('does not word the catalogue entry as an acquittal', async () => {
    const text = await textFor(nsrl())

    expect(
      text,
      'the line reads as a verdict on the file rather than as context on the sample',
    ).not.toMatch(/\bsafe\b|\bharmless\b|\bbenign\b|\btrusted\b|\bknown good\b|not malicious/i)
  })
})
