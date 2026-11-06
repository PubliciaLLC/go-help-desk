import { describe, expect, it, vi, beforeEach } from 'vitest'
import { cleanup, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import type { Attachment, AttachmentReputation } from '@/api/types'

// Asking the reputation service again, and saying who it was.
//
// #168: "Verdicts go stale, so they expire — and staff can always ask again."
// A stored verdict used to be kept forever, which is right for `detected` —
// engines do not un-flag a file — and wrong for the other three, in the
// dangerous direction: a sample nobody had submitted when we asked is
// precisely the sample somebody submits a week later.
//
// Two things are pinned here, and most of it is negatives, because the
// negatives are the half that is easy to get right by accident and easy to
// regress:
//
//   the control   present and armed only on a non-detected verdict more than
//                 seven days old; present and DISABLED when the verdict is
//                 newer, because a control that vanishes teaches nobody
//                 anything; absent altogether on a detection and on a file
//                 with no verdict at all.
//   the provider  named, because "VirusTotal has never seen this file" is a
//                 claim with a source and "the reputation service has never
//                 seen this file" is a claim from nowhere. An identifier this
//                 build does not know falls back to the generic wording
//                 rather than printing a raw setting value at a reader.
//
// Driven through TicketDetailPage like the other three attachment suites: the
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
const THREAT_NAME = 'Win32.Trojan.Agent.ABCD'

const DAY = 24 * 60 * 60 * 1000
// The manual floor, which is a property of the feature and not of the
// configured refresh interval: staff may ask again once every seven days
// whatever the setting says, including when it says `never`.
const FLOOR_DAYS = 7

/** An ISO timestamp n days before the moment the test runs. */
function daysAgo(n: number): string {
  return new Date(Date.now() - n * DAY).toISOString()
}

/** The day a verdict fetched at `iso` may next be asked about. */
function unlockAfter(iso: string): string {
  return new Date(Date.parse(iso) + FLOOR_DAYS * DAY).toISOString()
}

// The date as this frontend already renders one (see the analysed-at stamp on
// the verdict line). Coupled to that choice on purpose: the assertion that
// matters below is WHICH date is shown — the day it clears, not the day it was
// fetched — and comparing formatted strings is the only way to tell those two
// apart without re-implementing the formatter in the test.
function shownDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { dateStyle: 'medium' })
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
    provider: 'virustotal',
    fetched_at: daysAgo(FLOOR_DAYS + 1),
    ...over,
  }
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
    reputation_url: `https://www.virustotal.com/gui/file/${EICAR_SHA}`,
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

/**
 * The re-check control on the quarantined row, however it is labelled.
 *
 * Matched on what it does rather than on one exact string, because the issue
 * fixes the behaviour and not the copy. Links count too: an implementation
 * that made this a link rather than a button would still have to satisfy
 * everything below.
 */
function recheckControl(): HTMLElement | null {
  const row = quarantinedRow()
  const wanted = /check again|re-?check|ask again|refresh|look up again|check now/i
  const candidates = [
    ...within(row).queryAllByRole('button'),
    ...within(row).queryAllByRole('link'),
  ].filter((el) => {
    const name = `${el.textContent ?? ''} ${el.getAttribute('aria-label') ?? ''}`
    // The download confirmation trigger also lives on this row and must not
    // be mistaken for this one.
    return wanted.test(name) && !/download/i.test(name)
  })
  return candidates[0] ?? null
}

/** An API refusal in the shape axios hands to a mutation's onError. */
function refusal(status: number, code?: string, message?: string): AxiosError {
  const err = new AxiosError(`Request failed with status code ${status}`, String(status))
  err.response = {
    status,
    statusText: '',
    headers: {},
    config: {},
    data: code ? { error: { code, message } } : {},
  } as unknown as AxiosResponse
  return err
}

const RECHECK_URL = `/tickets/${TICKET_ID}/attachments/att-bad/reputation`

// ── The control is not always there, and that is most of the requirement ──────

describe('the re-check control', () => {
  // There is nothing to learn by re-confirming a detection — engines do not
  // un-flag a file — and the server refuses it with 409 anyway. Absent, not
  // disabled: a disabled control here would invite staff to keep trying.
  it('is absent on a detected verdict, however old the verdict is', async () => {
    await renderTicket([
      quarantined(
        verdict({
          state: 'detected',
          detected: 3,
          total: 71,
          threat_name: THREAT_NAME,
          fetched_at: daysAgo(400),
        }),
      ),
      OTHER,
    ])

    expect(
      recheckControl()?.outerHTML,
      'a detection offers a re-check, which cannot tell anybody anything and which the server refuses',
    ).toBeUndefined()
  })

  // Nothing has been looked up, so there is nothing to refresh; the ordinary
  // lazy lookup covers this file on the next page render.
  it('is absent when nothing has been looked up for the file', async () => {
    await renderTicket([quarantined(null), OTHER])

    expect(
      recheckControl()?.outerHTML,
      'a file with no verdict at all offers a re-check, which would be a second way to spend the daily allowance',
    ).toBeUndefined()
  })

  // The armed case: seven days have passed, so a person can ask.
  it('is offered and usable once a non-detected verdict is older than seven days', async () => {
    const post = vi.spyOn(api, 'post')
    await renderTicket([
      quarantined(verdict({ state: 'clean', detected: 0, total: 78, fetched_at: daysAgo(9) })),
      OTHER,
    ])

    const control = recheckControl()
    expect(control, 'a verdict older than seven days offers no way to ask again').not.toBeNull()
    expect(
      (control as HTMLButtonElement).disabled,
      'the control is disabled on a verdict that is old enough to re-check',
    ).toBe(false)
    expect(post, 'the control asked the server before anyone clicked it').not.toHaveBeenCalled()
  })

  // Disabled, not hidden. A control that vanishes leaves the reader wondering
  // whether the feature exists at all, and the refusal has to be actionable:
  // the date it clears, which is seven days after we last asked and NOT the
  // day we last asked.
  it('is disabled but still shown while the verdict is recent, and says when it clears', async () => {
    const post = vi.spyOn(api, 'post')
    const user = userEvent.setup()
    const fetched = daysAgo(2)
    await renderTicket([quarantined(verdict({ state: 'unseen', fetched_at: fetched })), OTHER])

    const control = recheckControl()
    expect(control, 'a recent verdict hides the control instead of disabling it').not.toBeNull()
    expect(
      (control as HTMLButtonElement).disabled,
      'the control is live on a verdict checked two days ago — the server would refuse this with 429',
    ).toBe(true)

    const text = quarantinedRow().textContent ?? ''
    expect(text, 'the disabled control does not say when it can next be asked').toContain(
      shownDate(unlockAfter(fetched)),
    )
    expect(
      text,
      'the control shows the date of the last check rather than the date it clears',
    ).not.toContain(shownDate(fetched))
    expect(text, 'the disabled control rendered a placeholder in place of a date').not.toMatch(
      /undefined|\bnull\b|NaN|Invalid Date/i,
    )

    await user.click(control!)
    expect(post, 'a disabled control still sent the request').not.toHaveBeenCalled()
  })
})

// ── Asking again ──────────────────────────────────────────────────────────────

describe('asking the reputation service again', () => {
  // The whole reason the control exists: the answer changed. A verdict that
  // read "0 of 78 engines" must not survive on the row once the service says
  // otherwise, and the control must go with it, because the new verdict is a
  // detection.
  it('shows the new verdict when the answer has changed from clean to detected', async () => {
    const user = userEvent.setup()
    const updated = quarantined(
      verdict({
        state: 'detected',
        detected: 4,
        total: 71,
        threat_name: THREAT_NAME,
        fetched_at: new Date().toISOString(),
      }),
    )
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: updated } as unknown as AxiosResponse)

    await renderTicket([
      quarantined(verdict({ state: 'clean', detected: 0, total: 78, fetched_at: daysAgo(30) })),
      OTHER,
    ])
    await user.click(recheckControl()!)

    await waitFor(() => {
      expect(quarantinedRow().textContent ?? '').toContain(THREAT_NAME)
    })
    expect(
      post.mock.calls[0]?.[0],
      'the re-check went somewhere other than this attachment',
    ).toBe(RECHECK_URL)

    const text = quarantinedRow().textContent ?? ''
    expect(text, 'the row still shows the old engine count').not.toMatch(/\b78\b/)
    expect(text, 'the new count is missing').toMatch(/\b4\b/)
    expect(text, 'the new denominator is missing').toMatch(/\b71\b/)
    expect(
      recheckControl()?.outerHTML,
      'the row still offers a re-check after the answer became a detection',
    ).toBeUndefined()
  })

  // Same answer, newer fetch. The row must accept the update rather than keep
  // showing a verdict it has been told is stale, and the control must re-lock
  // — otherwise the next reader asks again for nothing.
  it('re-locks the control when the answer comes back the same', async () => {
    const user = userEvent.setup()
    const fetched = new Date().toISOString()
    const updated = quarantined(verdict({ state: 'unseen', fetched_at: fetched }))
    vi.spyOn(api, 'post').mockResolvedValue({ data: updated } as unknown as AxiosResponse)

    await renderTicket([quarantined(verdict({ state: 'unseen', fetched_at: daysAgo(30) })), OTHER])
    await user.click(recheckControl()!)

    await waitFor(() => {
      expect((recheckControl() as HTMLButtonElement | null)?.disabled).toBe(true)
    })
    expect(
      quarantinedRow().textContent ?? '',
      'the re-locked control does not say when it can next be asked',
    ).toContain(shownDate(unlockAfter(fetched)))
  })
})

// ── Refusals ──────────────────────────────────────────────────────────────────

describe('a refusal from the server', () => {
  async function clickAndRead(err: AxiosError): Promise<string> {
    const user = userEvent.setup()
    vi.spyOn(api, 'post').mockRejectedValue(err)
    await renderTicket([quarantined(verdict({ state: 'unseen', fetched_at: daysAgo(30) })), OTHER])
    const before = quarantinedRow().textContent ?? ''
    await user.click(recheckControl()!)
    await waitFor(() => {
      expect(quarantinedRow().textContent ?? '').not.toBe(before)
    })
    return quarantinedRow().textContent ?? ''
  }

  // The server's message names the date it clears. Reporting it is the whole
  // job: "something went wrong" tells a reader nothing they can act on.
  it('says when a too-soon re-check can be asked again', async () => {
    const text = await clickAndRead(
      refusal(
        429,
        'checked_recently',
        'this file was checked less than seven days ago; it can be checked again on 28 September 2026',
      ),
    )

    expect(text, 'the refusal does not say when the file can be checked again').toContain(
      '28 September 2026',
    )
    expect(text, 'a refusal rendered a placeholder').not.toMatch(
      /undefined|\bnull\b|NaN|\[object Object\]/i,
    )
  })

  // A lookup that is switched off or out of allowance is a different fact
  // from one that was asked too recently, and staff act on them differently:
  // one clears by waiting, the other needs an operator.
  it('does not report an unavailable lookup in the same words as a too-soon one', async () => {
    const tooSoon = await clickAndRead(
      refusal(429, 'checked_recently', 'this file was checked less than seven days ago'),
    )
    // The next render mounts alongside this one otherwise, and rowFor would
    // read the same row twice and compare it with itself.
    cleanup()

    const unavailable = await clickAndRead(
      refusal(
        503,
        'reputation_unavailable',
        'the reputation lookup is not configured on this instance',
      ),
    )

    expect(
      unavailable,
      'a rate-limited re-check and an unconfigured lookup render the same words',
    ).not.toBe(tooSoon)
    expect(unavailable, 'the row does not say the lookup could not be made').toMatch(
      /not configured|unavailable|could not|cannot/i,
    )
  })

  // A 503 in particular does not always come from our handler — a proxy in
  // front of it answers with no JSON at all — and "Request failed with status
  // code 503" is not a sentence to put in front of a reader.
  it('still distinguishes the two when the refusal carries no message', async () => {
    const tooSoon = await clickAndRead(refusal(429))
    cleanup()
    const unavailable = await clickAndRead(refusal(503))

    for (const [name, text] of [
      ['429', tooSoon],
      ['503', unavailable],
    ] as const) {
      expect(text, `a bare ${name} was reported as a bare status code`).not.toMatch(
        /status code|\bError\b|something went wrong/i,
      )
    }
    expect(
      unavailable,
      'a bare 429 and a bare 503 collapse into one message',
    ).not.toBe(tooSoon)
    expect(tooSoon, 'a bare 429 does not say the file was checked too recently').toMatch(
      /seven days|recently|too soon/i,
    )
  })
})

// ── Who said it ───────────────────────────────────────────────────────────────

describe('the provider behind a verdict', () => {
  // Attribution is the point. The frontend cannot work the provider out for
  // itself — it is a session-gated admin setting staff cannot read — so the
  // payload carries it, and the row has to use it.
  //
  // Both spellings, because the identifier is what the setting holds and the
  // display name is what the server currently sends: an implementation that
  // handles only one of them names the provider on half the deployments and
  // silently falls back on the other half.
  const named: Array<[string, string]> = [
    ['virustotal', 'VirusTotal'],
    ['VirusTotal', 'VirusTotal'],
    ['metadefender', 'MetaDefender'],
    ['MetaDefender', 'MetaDefender'],
  ]

  for (const [sent, shown] of named) {
    it(`renders ${sent} as ${shown}`, async () => {
      await renderTicket([quarantined(verdict({ state: 'unseen', provider: sent })), OTHER])
      const text = quarantinedRow().textContent ?? ''

      expect(text, `the verdict is not attributed to ${shown}`).toContain(shown)
      expect(
        text,
        'the verdict names the provider and still hedges with the generic wording',
      ).not.toMatch(/the reputation service/i)
    })
  }

  // A provider this build does not know about is a setting value, not a
  // product name, and a reader should never be shown one. The generic wording
  // is the honest fallback.
  it('falls back to generic wording for a provider it does not recognise', async () => {
    await renderTicket([
      quarantined(verdict({ state: 'unseen', provider: 'polyswarm-v3-beta' })),
      OTHER,
    ])
    const text = quarantinedRow().textContent ?? ''

    expect(text, 'a raw setting value was printed at a reader as a provider name').not.toMatch(
      /polyswarm/i,
    )
    expect(text, 'an unrecognised provider lost the generic attribution too').toMatch(
      /reputation service/i,
    )
    expect(text, 'an unrecognised provider still has to say the file was never seen').toMatch(
      /never seen|no record|has not seen|never encountered/i,
    )
  })

  // The name belongs on every state, not only the reassuring ones. A
  // detection attributed to nobody is the claim most worth sourcing.
  it('names the provider on a detection too', async () => {
    await renderTicket([
      quarantined(
        verdict({
          state: 'detected',
          detected: 3,
          total: 71,
          threat_name: THREAT_NAME,
          provider: 'metadefender',
        }),
      ),
      OTHER,
    ])

    expect(
      quarantinedRow().textContent ?? '',
      'a detection does not say which service found it',
    ).toContain('MetaDefender')
  })

  // The provider name reaches the page from a setting an operator typed. It
  // is a far smaller surface than a threat name, but the row already escapes
  // everything else it renders and this must not be the exception.
  it('renders the provider as text, never as markup', async () => {
    await renderTicket([
      quarantined(verdict({ state: 'unseen', provider: '<img src=x onerror="alert(1)">' })),
      OTHER,
    ])
    const row = quarantinedRow()

    expect(
      row.querySelectorAll('img, script, b').length,
      'the provider name was parsed as markup',
    ).toBe(0)
    expect(row.textContent ?? '', 'an unknown provider leaked its raw value').not.toMatch(
      /onerror/i,
    )
  })
})

// ── Where it sits ─────────────────────────────────────────────────────────────

describe('the re-check control on the page', () => {
  // The download confirmation must stay the only thing on this row that
  // reaches the bytes, and the re-check must not have grown a second way to
  // get at them.
  it('offers nothing that downloads the file', async () => {
    await renderTicket([
      quarantined(verdict({ state: 'unseen', fetched_at: daysAgo(30) })),
      OTHER,
    ])

    const control = recheckControl()
    expect(control, 'no control to check').not.toBeNull()
    expect(
      control!.getAttribute('href'),
      'the re-check control is a link to something',
    ).toBeNull()
    expect(
      screen.queryByRole('dialog'),
      'a dialog was open before anybody asked for one',
    ).toBeNull()
  })
})
