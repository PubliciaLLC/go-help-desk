import { describe, expect, it, vi, beforeEach } from 'vitest'
import { waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Attachment, AttachmentProviderVerdict, AttachmentReputation } from '@/api/types'

// Four services, one row.
//
// #168, "Per-provider toggles, every enabled provider queried, worst verdict
// shown": every enabled provider is asked, the row shows the single most
// serious answer, and one click shows all of them separately.
//
// The three sibling reputation suites pin one verdict rendered one way. This
// one is about what having four of them costs and buys, and almost all of it
// is negatives, because the wrong implementations here all look plausible:
//
//   the worst answer   `unavailable` is a failed lookup and not a verdict, so
//                      it must never displace one. A dead VirusTotal next to a
//                      CIRCL `known` reads `known` — and the dead VirusTotal
//                      still has to be visible somewhere, or an operator never
//                      learns their key stopped working.
//   attribution        every expanded line names the service that said it. Four
//                      statements flattened into one unattributed summary is
//                      exactly the information that made the second, third and
//                      fourth lookup worth making.
//   no collapsing      two providers that happen to agree are still two
//                      providers. An implementation that de-duplicates by state
//                      passes every happy-path assertion and loses half the
//                      payload.
//   the VirusTotal     goes to VirusTotal whatever is enabled — a link is not
//   link               a lookup — which is a surprise that has to be explained
//                      on the page or it reads as a broken setting. WHICH rows
//                      carry one is a separate rule and a separate suite:
//                      every fixture here is quarantined, which is one of them.
//   nothing enabled    no block at all. Not "not checked": nothing was
//                      attempted, and saying otherwise invents a failure.
//
// Driven through TicketDetailPage like the sibling suites: the requirement is
// about what a person looking at a ticket sees, and how the page is split up is
// the implementer's business.

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
const THREAT_NAME = 'Win32.Trojan.Agent.ABCD'

// The hash link. It goes to VirusTotal whatever the toggles say, because a link
// sends nothing from this server — and the server puts one on this row because
// the scanner named the file. See attachments-hash-link.test.tsx for which rows
// get one at all.
const VT_HASH_LINK = `https://www.virustotal.com/gui/file/${EICAR_SHA}`

// A provider's own page for the hash, which is a different thing: it sits next
// to that provider's own verdict in the expanded view.
const VT_OWN_LINK = `https://www.virustotal.com/gui/file/${EICAR_SHA}?ref=own`
const MD_OWN_LINK = `https://metadefender.opswat.com/results/file/${EICAR_SHA}/hash/overview`
const PS_OWN_LINK = `https://polyswarm.network/scan/results/file/${EICAR_SHA}`

const DAY = 24 * 60 * 60 * 1000
const FLOOR_DAYS = 7

function daysAgo(n: number): string {
  return new Date(Date.now() - n * DAY).toISOString()
}

/** The day a verdict fetched at `iso` may next be asked about. */
function unlockAfter(iso: string): string {
  return new Date(Date.parse(iso) + FLOOR_DAYS * DAY).toISOString()
}

// The date as this frontend already renders one, so an assertion can tell
// WHICH date was shown without re-implementing the formatter.
function shownDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' })
}

const DISPLAY_NAMES: Record<string, string> = {
  virustotal: 'VirusTotal',
  metadefender: 'MetaDefender',
  polyswarm: 'PolySwarm',
  circl: 'CIRCL',
}

/**
 * One provider's line, as the server sends it.
 *
 * Defaults to a failed lookup with nothing in it, because that is the entry
 * every test here has to reason about and the one an implementation is most
 * likely to drop.
 */
function line(
  over: Partial<AttachmentProviderVerdict> & { provider_key: string },
): AttachmentProviderVerdict {
  return {
    provider: DISPLAY_NAMES[over.provider_key] ?? over.provider_key,
    state: 'unavailable',
    detected: null,
    total: null,
    threat_name: '',
    known_feeds: [],
    analysed_at: null,
    fetched_at: daysAgo(30),
    link_url: null,
    recheckable: false,
    inline: false,
    ...over,
  }
}

/**
 * The payload, built the way the server builds it: the summary is a COPY of
 * whichever line carries `inline`, never an aggregate.
 *
 * Built here rather than written out per test so that a fixture cannot
 * accidentally pin a summary that disagrees with its own provider list — which
 * is a state the server cannot produce, so a test that relied on it would be
 * testing nothing.
 */
function payload(providers: AttachmentProviderVerdict[]): AttachmentReputation {
  const w = providers.find((p) => p.inline)
  if (!w) {
    // Nothing answered. The summary belongs to nobody, because nobody said it.
    return {
      state: 'unavailable',
      detected: null,
      total: null,
      threat_name: '',
      analysed_at: null,
      provider: '',
      provider_key: '',
      known_feeds: [],
      fetched_at: null,
      providers,
    }
  }
  return {
    state: w.state,
    detected: w.detected,
    total: w.total,
    threat_name: w.threat_name,
    analysed_at: w.analysed_at,
    provider: w.provider,
    provider_key: w.provider_key,
    known_feeds: w.known_feeds,
    fetched_at: w.fetched_at,
    providers,
  }
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

/** A quarantined attachment carrying whichever payload the test is about. */
function quarantined(reputation: AttachmentReputation | null): Attachment {
  return attachment({
    id: 'att-bad',
    filename: 'invoice.exe.zip',
    mime_type: 'application/zip',
    detected_mime: 'application/vnd.microsoft.portable-executable',
    sha256: EICAR_SHA,
    virus_name: DETECTION,
    mismatch: false,
    // The scanner named this file, so the server sends a link — and it goes
    // to VirusTotal whatever the toggles say.
    reputation_url: VT_HASH_LINK,
    reputation,
  })
}

// The second attachment in every render, so rowFor has a boundary to stop at.
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

/**
 * The disclosure control that shows every provider's verdict.
 *
 * Found by `aria-expanded` rather than by its words, because the words are
 * copy and the disclosure is the contract: a reader on a screen reader has to
 * be told the row has more behind it and whether it is open. Nothing else on a
 * quarantined row is a disclosure, so this cannot pick up the download trigger
 * or a Check again.
 */
function expandControl(): HTMLElement | null {
  return quarantinedRow().querySelector<HTMLElement>('[aria-expanded]')
}

async function expand(): Promise<void> {
  const user = userEvent.setup()
  const control = expandControl()
  expect(control, 'the row offers no way to see each service separately').not.toBeNull()
  await user.click(control!)
  await waitFor(() => {
    expect(expandControl()?.getAttribute('aria-expanded')).toBe('true')
  })
}

/**
 * One element per provider in the expanded view.
 *
 * The provider list is a real list, which is what makes "each on its own line"
 * something a test — and a screen reader — can see rather than something the
 * pixels merely suggest.
 */
function providerLines(): HTMLElement[] {
  return within(quarantinedRow()).queryAllByRole('listitem')
}

/** The single expanded line that names `name`, failing if it is not exactly one. */
function lineFor(name: string): HTMLElement {
  const matching = providerLines().filter((el) => (el.textContent ?? '').includes(name))
  expect(
    matching.length,
    `expected exactly one expanded line for ${name}, found ${matching.length}`,
  ).toBe(1)
  return matching[0]
}

/**
 * The re-check controls on the row, however they are labelled.
 *
 * Same matcher as the sibling recheck suite, so an implementation cannot satisfy
 * one and not the other by renaming the button.
 */
function recheckControls(root: HTMLElement): HTMLElement[] {
  const wanted = /check again|re-?check|ask again|refresh|look up again|check now/i
  return [...within(root).queryAllByRole('button'), ...within(root).queryAllByRole('link')].filter(
    (el) => {
      const name = `${el.textContent ?? ''} ${el.getAttribute('aria-label') ?? ''}`
      return wanted.test(name) && !/download/i.test(name)
    },
  )
}

/** Wording that would tell a reader a service found nothing wrong. */
const REASSURANCE = [
  /\bno engines?\b/i,
  /\b(is|was|looks|found|reported|came back|verdict:)\s+clean\b/i,
  /\bharmless\b/i,
  /\bnothing (was )?(found|flagged|detected)\b/i,
]

const PLACEHOLDER = /undefined|\bnull\b|NaN|Invalid Date|\[object Object\]/i

// ── The worst answer wins, and a failure is not an answer ─────────────────────

describe('a failed lookup next to a real verdict', () => {
  // The headline rule. `unavailable` is not in the severity ordering at all —
  // it is a failed lookup, not a verdict — so it must never take the row from
  // a service that did answer.
  const providers = [
    line({ provider_key: 'virustotal', state: 'unavailable', link_url: VT_OWN_LINK }),
    line({
      provider_key: 'circl',
      state: 'known',
      known_feeds: ['nsrl'],
      fetched_at: daysAgo(3),
      inline: true,
    }),
  ]

  it('shows the answer inline, not the failure', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    const text = quarantinedRowText()

    expect(text, 'the row does not show the verdict the one working service gave').toMatch(
      /\bknown\b|catalogue/i,
    )
    expect(text, 'the row does not name the feed behind the known verdict').toMatch(/NSRL/i)
    expect(text, 'a failed VirusTotal lookup displaced an answer CIRCL actually gave').not.toMatch(
      /could not be completed|no answer|lookup failed|unavailable/i,
    )
    expect(text, 'the inline verdict rendered a placeholder').not.toMatch(PLACEHOLDER)
  })

  // Attribution, and specifically to the right one of the two. A row that says
  // "VirusTotal: known file" is worse than one that says nothing, because it is
  // false about both services.
  it('attributes the inline verdict to the service that gave it', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    const text = quarantinedRowText()

    expect(text, 'the inline verdict is not attributed to CIRCL, which is who said it').toContain(
      'CIRCL',
    )
    expect(
      text,
      'the inline verdict names the generic service as well as the one that gave it',
    ).not.toMatch(/the reputation service/i)
  })

  // The other half, and the reason `unavailable` stays on the wire at all: an
  // operator whose VirusTotal key has been rejected has to be able to see that,
  // just not at the cost of hiding CIRCL's answer.
  it('still shows the failure against its own provider when expanded', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    const vt = lineFor('VirusTotal')
    expect(
      vt.textContent ?? '',
      'the expanded VirusTotal line does not say the lookup failed, so a dead key is invisible',
    ).toMatch(/could not|no answer|did not complete|unavailable|failed/i)
    expect(vt.textContent ?? '', 'the failed line rendered a placeholder').not.toMatch(PLACEHOLDER)

    // And it must not have quietly become a verdict on the way.
    for (const re of REASSURANCE) {
      expect(vt.textContent ?? '', `a failed lookup reads as a clean result: ${re}`).not.toMatch(re)
    }
  })

  // A failed lookup has nothing cached behind it, so the server refuses a
  // re-check of one — a control here would be a button that always fails.
  it('offers no Check again on the failed line', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    expect(
      recheckControls(lineFor('VirusTotal')).length,
      'a failed lookup offers a re-check, which the server refuses as nothing to re-check',
    ).toBe(0)
  })
})

// ── The ordering is the server's ──────────────────────────────────────────────

describe('which answer the row picks', () => {
  // `unseen` outranks `clean`, which is the one ordering rule nobody guesses:
  // only quarantined files are looked up, so every hash here is one the local
  // scanner already called malicious, and a file NO service has ever seen is a
  // novel sample — more concerning than one 78 engines examined and passed,
  // not less.
  //
  // The frontend does not get to have an opinion about this. The ordering
  // lives in one place on the server and a second copy here would drift; the
  // row shows the entry the server marked, whatever this build would have
  // picked for itself.
  const providers = [
    line({
      provider_key: 'metadefender',
      state: 'clean',
      detected: 0,
      total: 78,
      analysed_at: '2026-09-18T07:30:00Z',
    }),
    line({ provider_key: 'virustotal', state: 'unseen', inline: true }),
  ]

  it('shows the entry the server marked, not the one this build would rank worst', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    const text = quarantinedRowText()

    expect(text, 'the row does not show the verdict the server picked').toMatch(
      /never seen|no record|has not seen|never encountered/i,
    )
    expect(text, 'the row is attributed to the wrong service').toContain('VirusTotal')
    expect(
      text,
      'the row ranked a clean result above a file nobody has ever seen, which is the ordering this feature exists to get right',
    ).not.toMatch(/\b78\b/)
  })
})

// ── Every provider failed ─────────────────────────────────────────────────────

describe('a file every enabled service failed to answer for', () => {
  const providers = [
    line({ provider_key: 'virustotal', state: 'unavailable', link_url: VT_OWN_LINK }),
    line({ provider_key: 'metadefender', state: 'unavailable', link_url: MD_OWN_LINK }),
  ]

  // Nothing answered, so `unavailable` IS the honest summary — which is the one
  // case it may appear inline, and it is attributed to nobody because nobody
  // said it.
  it('says the lookup did not complete, and reads as neither clean nor a finding', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    const text = quarantinedRowText()

    expect(text, 'a row whose every service failed says nothing about it').toMatch(
      /could not|no answer|did not complete|not checked/i,
    )
    for (const re of REASSURANCE) {
      expect(text, `every lookup failed and the row reads as a clean result: ${re}`).not.toMatch(re)
    }
    expect(text, 'a failed lookup is being reported as a finding').not.toMatch(
      /flagged|never seen|no record/i,
    )
    expect(text, 'the failed summary rendered a placeholder').not.toMatch(PLACEHOLDER)
    expect(
      recheckControls(quarantinedRow()).length,
      'a row with nothing cached offers a re-check',
    ).toBe(0)
  })

  // Two dead providers are two facts. An operator reading this is trying to
  // work out which of their keys to go and fix.
  it('names each service that failed when expanded', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    expect(providerLines().length, 'two failed services did not produce two lines').toBe(2)
    lineFor('VirusTotal')
    lineFor('MetaDefender')
  })
})

// ── The expanded view attributes every line ───────────────────────────────────

describe('four services on one row', () => {
  const ANALYSED = '2026-09-18T07:30:00Z'
  const VT_FETCHED = daysAgo(30)
  const MD_FETCHED = daysAgo(2)

  const providers = [
    line({
      provider_key: 'virustotal',
      state: 'detected',
      detected: 62,
      total: 81,
      threat_name: THREAT_NAME,
      analysed_at: ANALYSED,
      fetched_at: VT_FETCHED,
      link_url: VT_OWN_LINK,
      recheckable: false,
      inline: true,
    }),
    line({
      provider_key: 'metadefender',
      state: 'clean',
      detected: 0,
      total: 37,
      analysed_at: '2026-09-11T09:00:00Z',
      fetched_at: MD_FETCHED,
      link_url: MD_OWN_LINK,
      recheckable: false,
    }),
    line({
      provider_key: 'polyswarm',
      state: 'unseen',
      fetched_at: daysAgo(30),
      link_url: PS_OWN_LINK,
      recheckable: true,
    }),
    line({
      provider_key: 'circl',
      state: 'known',
      known_feeds: ['microsoft_windows'],
      fetched_at: daysAgo(30),
      link_url: null,
      recheckable: false,
    }),
  ]

  const REP = payload(providers)

  // Collapsed is the default. A row that shows four verdicts at once is four
  // times the noise on a ticket list nobody opened for this.
  it('shows one verdict until somebody asks for the rest', async () => {
    await renderTicket([quarantined(REP), OTHER])
    const text = quarantinedRowText()

    expect(expandControl()?.getAttribute('aria-expanded'), 'the row starts expanded').toBe('false')
    expect(text, 'the worst verdict is missing from the collapsed row').toMatch(/\b62\b/)
    expect(text, "another service's counts leaked into the collapsed row").not.toMatch(/\b37\b/)
    expect(text, "another service's name leaked into the collapsed row").not.toContain('CIRCL')
    expect(providerLines().length, 'the expanded lines render before anybody expanded them').toBe(0)
  })

  // The whole point of the feature. Four statements, four lines, each one
  // traceable to the service that made it.
  it('gives every service its own line, named', async () => {
    await renderTicket([quarantined(REP), OTHER])
    await expand()

    expect(providerLines().length, 'four services did not produce four lines').toBe(4)
    for (const name of ['VirusTotal', 'MetaDefender', 'PolySwarm', 'CIRCL']) {
      lineFor(name)
    }
  })

  // The ordering is the backend's and the frontend does not get to have an
  // opinion about it — the severity ordering already lives there, and two
  // copies of it would drift.
  it('keeps the order the server sent', async () => {
    await renderTicket([quarantined(REP), OTHER])
    await expand()

    const lines = providerLines()
    const order = providers.map((p) =>
      lines.findIndex((el) => (el.textContent ?? '').includes(p.provider)),
    )
    expect(order, 'the expanded lines were reordered away from the order the server sent').toEqual([
      0, 1, 2, 3,
    ])
  })

  // Each line carries its own numbers, its own feeds, its own dates. A line
  // that showed the summary's numbers under another service's name would be
  // attributing a claim to somebody who did not make it.
  it("carries each service's own counts, feeds and dates", async () => {
    await renderTicket([quarantined(REP), OTHER])
    await expand()

    const vt = lineFor('VirusTotal').textContent ?? ''
    expect(vt, "VirusTotal's own counts are missing from its line").toMatch(/\b62\b/)
    expect(vt, "VirusTotal's denominator is missing from its line").toMatch(/\b81\b/)
    expect(vt, "VirusTotal's threat name is missing from its line").toContain(THREAT_NAME)
    expect(vt, "VirusTotal's analysis date is missing from its line").toContain(shownDate(ANALYSED))

    const md = lineFor('MetaDefender').textContent ?? ''
    expect(md, "MetaDefender's own counts are missing from its line").toMatch(/\b37\b/)
    expect(md, "the summary's counts were repeated under MetaDefender's name").not.toMatch(/\b62\b/)

    const circl = lineFor('CIRCL').textContent ?? ''
    expect(circl, "CIRCL's feed is missing, so its line claims more than the data supports").toMatch(
      /Microsoft Windows/i,
    )
    expect(circl, 'a catalogue hit invented engine counts').not.toMatch(/\bengines?\b/i)

    const ps = lineFor('PolySwarm').textContent ?? ''
    expect(ps, 'PolySwarm never-seen line lost its verdict').toMatch(
      /never seen|no record|has not seen|never encountered/i,
    )
    for (const el of providerLines()) {
      expect(el.textContent ?? '', 'an expanded line rendered a placeholder').not.toMatch(
        PLACEHOLDER,
      )
    }
  })

  // Each verdict has its own expiry clock, because the cache is keyed by hash
  // AND provider. A shared "last checked" across four services would be wrong
  // for at least three of them.
  it('says when each service was last asked, separately', async () => {
    await renderTicket([quarantined(REP), OTHER])
    await expand()

    expect(
      lineFor('MetaDefender').textContent ?? '',
      'MetaDefender line does not say when we last asked it',
    ).toContain(shownDate(MD_FETCHED))
    expect(
      lineFor('VirusTotal').textContent ?? '',
      "VirusTotal line shows MetaDefender's fetch date",
    ).not.toContain(shownDate(MD_FETCHED))
  })

  // A service's own page for the hash belongs next to its own verdict. CIRCL
  // has no per-hash web UI, so it gets none — a link to a page that cannot
  // answer the question the reader clicked it with is worse than no link.
  it('links each service that has a page, and not the one that has none', async () => {
    await renderTicket([quarantined(REP), OTHER])
    await expand()

    expect(
      within(lineFor('MetaDefender')).queryAllByRole('link').map((a) => a.getAttribute('href')),
      "MetaDefender's own page is missing from its line",
    ).toContain(MD_OWN_LINK)
    expect(
      within(lineFor('CIRCL')).queryAllByRole('link').length,
      'CIRCL has no per-hash page and was linked anyway',
    ).toBe(0)
  })
})

// ── Two services that agree are still two services ────────────────────────────

describe('two services that give the same verdict', () => {
  // The de-duplicating implementation. It passes every assertion about naming
  // and about counts as long as no two providers ever agree — and the moment
  // they do, half the payload vanishes with nothing to show it went.
  const providers = [
    line({
      provider_key: 'virustotal',
      state: 'detected',
      detected: 62,
      total: 81,
      threat_name: THREAT_NAME,
      inline: true,
    }),
    line({
      provider_key: 'metadefender',
      state: 'detected',
      detected: 19,
      total: 37,
      threat_name: 'Trojan.GenericKD',
    }),
  ]

  it('does not collapse into one line', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    expect(providerLines().length, 'two agreeing services were merged into one line').toBe(2)

    const vt = lineFor('VirusTotal').textContent ?? ''
    const md = lineFor('MetaDefender').textContent ?? ''
    expect(vt, "VirusTotal's counts are missing").toMatch(/\b62\b/)
    expect(md, "MetaDefender's counts are missing — its line repeats VirusTotal's").toMatch(/\b19\b/)
    expect(md, "MetaDefender's line shows VirusTotal's numbers").not.toMatch(/\b81\b/)
    expect(md, "MetaDefender's own name for the threat is missing").toContain('Trojan.GenericKD')
  })
})

// ── Asking one service again ──────────────────────────────────────────────────

describe('the Check again control in the expanded view', () => {
  const RECHECK_URL = `/tickets/${TICKET_ID}/attachments/att-bad/reputation`
  const PS_FETCHED = daysAgo(2)

  const providers = [
    line({
      provider_key: 'virustotal',
      state: 'unseen',
      fetched_at: daysAgo(30),
      recheckable: true,
      inline: true,
    }),
    line({
      provider_key: 'polyswarm',
      state: 'clean',
      detected: 0,
      total: 40,
      fetched_at: PS_FETCHED,
      recheckable: false,
    }),
    line({
      provider_key: 'circl',
      state: 'known',
      known_feeds: ['nsrl'],
      fetched_at: daysAgo(400),
      recheckable: false,
    }),
  ]

  // Each verdict expires on its own clock, so each gets its own control and
  // each names the provider it is asking. A control that asked all of them
  // would spend three allowances to answer one question.
  it('asks only the service whose control was clicked', async () => {
    const user = userEvent.setup()
    const post = vi
      .spyOn(api, 'post')
      .mockResolvedValue({ data: quarantined(payload(providers)) } as unknown as AxiosResponse)

    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    const controls = recheckControls(lineFor('VirusTotal'))
    expect(controls.length, "VirusTotal's own line offers no way to ask it again").toBe(1)
    await user.click(controls[0])

    await waitFor(() => {
      expect(post).toHaveBeenCalled()
    })
    expect(
      String(post.mock.calls[0]?.[0]),
      'the re-check did not name the service whose control was clicked',
    ).toBe(`${RECHECK_URL}?provider=virustotal`)
  })

  // Disabled rather than hidden, and it says when it clears — the same rule the
  // single-verdict control already follows, per provider.
  it('is disabled and dated on a verdict asked about inside the seven-day floor', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    const ps = lineFor('PolySwarm')
    const controls = recheckControls(ps)
    expect(controls.length, 'a recent verdict hides its control instead of disabling it').toBe(1)
    expect(
      (controls[0] as HTMLButtonElement).disabled,
      'the control is live on a verdict the server would refuse with 429',
    ).toBe(true)
    expect(
      ps.textContent ?? '',
      'the disabled control does not say when that service can next be asked',
    ).toContain(shownDate(unlockAfter(PS_FETCHED)))
  })

  // A file does not stop being the thing a catalogue has on record, so the
  // server refuses this with 409 whatever the floor says.
  it('is absent on a final verdict, however old', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    expect(
      recheckControls(lineFor('CIRCL')).length,
      'a known verdict offers a re-check, which the server refuses with 409',
    ).toBe(0)
  })

  // The inline control asks every enabled service at once, which is right for
  // the collapsed row and wrong the moment the reader can see them separately:
  // two controls for the same service, one of them unattributed.
  it('replaces the unattributed inline control rather than sitting beside it', async () => {
    await renderTicket([quarantined(payload(providers)), OTHER])

    const collapsed = recheckControls(quarantinedRow())
    expect(collapsed.length, 'the collapsed row offers no way to ask again').toBe(1)

    await expand()
    const open = recheckControls(quarantinedRow())
    expect(
      open.length,
      'the expanded row offers an unattributed control as well as the per-service ones',
    ).toBe(2)
    for (const control of open) {
      const owner = providerLines().find((el) => el.contains(control))
      expect(owner, 'a Check again in the expanded view names no service').toBeDefined()
    }
  })
})

// ── One service is still one service ──────────────────────────────────────────

describe('an instance with a single provider enabled', () => {
  // The regression that matters most, because most instances are this. Nothing
  // about the row may change: one verdict, no disclosure, no second copy of the
  // same sentence hiding behind a control.
  it('renders exactly as it did before there was more than one', async () => {
    const providers = [
      line({
        provider_key: 'virustotal',
        state: 'unseen',
        fetched_at: daysAgo(30),
        recheckable: true,
        link_url: VT_OWN_LINK,
        inline: true,
      }),
    ]
    await renderTicket([quarantined(payload(providers)), OTHER])
    const text = quarantinedRowText()

    expect(
      expandControl(),
      'a single service still offers to expand into a list of one, which shows the reader nothing',
    ).toBeNull()
    expect(text, 'the single verdict is not attributed').toContain('VirusTotal')
    expect(text, 'the single verdict is missing').toMatch(
      /never seen|no record|has not seen|never encountered/i,
    )
    expect(
      recheckControls(quarantinedRow()).length,
      'a single service lost its Check again control, or grew a second one',
    ).toBe(1)
    expect(text, 'the single-provider row rendered a placeholder').not.toMatch(PLACEHOLDER)
  })
})

// ── The link goes to VirusTotal whatever is enabled, and says so ─────────────

describe('the hash link', () => {
  // The correction in #168: a link is not a lookup. Disabling VirusTotal means
  // "do not send my customers' hashes there from my server", not "my staff may
  // never look at VirusTotal" — and the hash is on the page with a copy button
  // either way.
  const circlOnly = [
    line({
      provider_key: 'circl',
      state: 'known',
      known_feeds: ['nsrl'],
      fetched_at: daysAgo(3),
      inline: true,
    }),
  ]

  it('goes to VirusTotal even when VirusTotal is not one of the services asked', async () => {
    await renderTicket([quarantined(payload(circlOnly)), OTHER])
    const row = quarantinedRow()

    const links = Array.from(row.querySelectorAll('a')).map((a) => a.getAttribute('href'))
    expect(links, 'the hash link is missing from the row').toContain(
      VT_HASH_LINK,
    )
  })

  // Without this line, an operator who deliberately switched VirusTotal off and
  // still sees a VirusTotal link on every attachment will reasonably conclude
  // the setting does not work.
  it('says following it is the reader’s own browser and sends nothing from here', async () => {
    await renderTicket([quarantined(payload(circlOnly)), OTHER])
    const text = quarantinedRowText()

    expect(text, 'the link does not name the service it goes to').toContain('VirusTotal')
    expect(text, 'the link does not say it opens in the reader’s own browser').toMatch(
      /your (own )?browser/i,
    )
    // Not "nothing is sent". This row is quarantined and CIRCL is enabled, so
    // the hash IS sent — to CIRCL. The sentence this used to require said
    // nothing leaves the instance unless VirusTotal is on, which is false for
    // an operator running any of the other three, and the assertion encoded
    // that falsehood rather than merely tolerating it.
    //
    // What the note owes the reader is where the hash goes: only to the
    // services they switched on, which may be none of them.
    expect(
      text,
      'the note does not say where the hash actually goes',
    ).toMatch(/only to the services switched on|which may be none/i)
    expect(
      text,
      'the note claims nothing is sent, on a row where a hash is sent to an enabled provider',
    ).not.toMatch(/nothing is sent from this instance unless/i)
  })

  // Two different things that happen to share a host. The hash link is a fact
  // about the file; a provider's own link is part of that provider's
  // verdict, and putting the first inside the provider list would attribute it
  // to whichever service it landed next to.
  it('is not inside the list of per-service links', async () => {
    await renderTicket([
      quarantined(
        payload([
          line({ provider_key: 'virustotal', state: 'unseen', link_url: VT_OWN_LINK, inline: true }),
          line({ provider_key: 'circl', state: 'known', known_feeds: ['nsrl'] }),
        ]),
      ),
      OTHER,
    ])
    await expand()

    const hashLink = Array.from(quarantinedRow().querySelectorAll('a')).find(
      (a) => a.getAttribute('href') === VT_HASH_LINK,
    )
    expect(hashLink, 'the hash link is gone').toBeDefined()
    expect(
      providerLines().some((el) => el.contains(hashLink!)),
      'the hash link sits inside a provider line, where it reads as that service’s own',
    ).toBe(false)
  })
})

// ── Nothing enabled is a supported configuration ──────────────────────────────

describe('a quarantined attachment on an instance with no provider enabled', () => {
  // #168: "All four disabled is a supported configuration, not a broken one."
  // Nothing was attempted, so nothing is said — and specifically not "not
  // checked yet", which describes an attempt that did not finish and invents a
  // failure on every instance that never wanted the feature.
  it('carries no reputation block at all', async () => {
    await renderTicket([quarantined(null), OTHER])
    const text = quarantinedRowText()

    expect(text, 'a row nothing was asked about claims a lookup was attempted').not.toMatch(
      /not checked|no lookup|reputation service|never seen|no verdict|\bengines?\b/i,
    )
    expect(
      expandControl(),
      'a row with no verdicts at all offers to expand into nothing',
    ).toBeNull()
    expect(
      recheckControls(quarantinedRow()).length,
      'a row with nothing looked up offers a re-check, which is a second way to spend an allowance',
    ).toBe(0)
  })

  // And the local scanner's verdict stands on its own, which is the whole
  // argument for the configuration being supported.
  it('keeps everything the local scanner said', async () => {
    await renderTicket([quarantined(null), OTHER])
    const row = quarantinedRow()
    const text = row.textContent ?? ''

    expect(text, 'the scanner detection went with the reputation block').toContain(DETECTION)
    expect(text, 'the row stopped calling the file malicious').toMatch(/malicious|malware/i)
    expect(text, 'the archive password went with the reputation block').toMatch(
      /password[^a-z0-9]{0,20}infected/i,
    )
    expect(text, 'the hash went with the reputation block').toContain(EICAR_SHA.slice(0, 12))
    expect(
      within(row).queryByRole('button', { name: /download/i }),
      'the download confirmation went with the reputation block',
    ).not.toBeNull()
  })

  // The hash is a fact about the file rather than a claim by anyone, so it and
  // its link survive an instance that asks nobody anything.
  it('keeps the hash link, because a link is not a lookup', async () => {
    await renderTicket([quarantined(null), OTHER])

    const links = Array.from(quarantinedRow().querySelectorAll('a')).map((a) =>
      a.getAttribute('href'),
    )
    expect(links, 'the hash link went with the reputation block').toContain(VT_HASH_LINK)
  })
})

// ── Nothing here reaches the bytes ────────────────────────────────────────────

describe('the expanded view', () => {
  // The expanded view grew several links and several buttons. None of them may
  // be a second route to the file: the confirmation dialog stays the only one.
  it('offers nothing that downloads the file', async () => {
    const providers = [
      line({
        provider_key: 'virustotal',
        state: 'unseen',
        fetched_at: daysAgo(30),
        recheckable: true,
        link_url: VT_OWN_LINK,
        inline: true,
      }),
      line({ provider_key: 'circl', state: 'known', known_feeds: ['nsrl'] }),
    ]
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    for (const el of providerLines()) {
      for (const a of within(el).queryAllByRole('link')) {
        const href = a.getAttribute('href') ?? ''
        expect(href, 'a provider line links at the attachment itself').not.toMatch(
          /\/attachments\//,
        )
        expect(a.hasAttribute('download'), 'a provider line offers a download').toBe(false)
      }
    }
  })

  // Provider names and feed names arrive from a third party's API. This row
  // escapes everything else it renders and the expanded view must not be the
  // exception.
  it('renders provider and feed names as text, never as markup', async () => {
    const injected = '<img src=x onerror="alert(1)">'
    const providers = [
      line({
        provider_key: 'virustotal',
        state: 'known',
        known_feeds: [injected],
        inline: true,
      }),
      line({ provider_key: 'circl', state: 'unseen' }),
    ]
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()

    const row = quarantinedRow()
    expect(
      row.querySelectorAll('img, script, b').length,
      'a name from a provider API was parsed as markup — this is stored XSS against staff sessions',
    ).toBe(0)
    expect(row.textContent ?? '', 'the feed name was dropped rather than escaped').toContain(
      'onerror',
    )
  })

  // Closing it again has to work, or the control is a one-way door and the
  // "collapsed by default" decision lasts exactly one click.
  it('closes again', async () => {
    const providers = [
      line({ provider_key: 'virustotal', state: 'unseen', inline: true }),
      line({ provider_key: 'circl', state: 'known', known_feeds: ['nsrl'] }),
    ]
    await renderTicket([quarantined(payload(providers)), OTHER])
    await expand()
    expect(providerLines().length, 'nothing expanded').toBe(2)

    const user = userEvent.setup()
    await user.click(expandControl()!)
    await waitFor(() => {
      expect(expandControl()?.getAttribute('aria-expanded')).toBe('false')
    })
    expect(providerLines().length, 'the expanded lines survived the row being closed').toBe(0)
  })
})
