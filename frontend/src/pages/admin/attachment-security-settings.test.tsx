import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The admin surface for the attachment-security settings (#165, #167, #168).
//
// All of them shipped with no way to reach them except a raw PATCH: an
// operator could not turn quarantine on, could not change what this instance
// accepts, and could not configure a reputation lookup. The features existed
// and were invisible.
//
// Driven through SettingsPage rather than through a component, for the same
// reason the ticket-side suites drive TicketDetailPage: the requirement is
// about what an administrator can see and change, and how the page is split up
// is the implementer's business. Almost every assertion below is phrased so
// that any arrangement of panels and components can satisfy it. The one
// exception is deliberate and is a requirement rather than a test hook: each
// provider's toggle, key and warning are in a named group, so that a reader
// arriving at a password box called "API key" — by tab, by screen reader, or
// by eye on a page carrying three of them — can tell which service it belongs
// to.
//
// Four things here are not ordinary form plumbing and are the reason most of
// these tests exist:
//
//   the warnings  #168 specifies them with four rules: not dismissible, shown
//                 whether or not a key is configured, quoting the provider and
//                 attributing the quote to them, and one per provider because
//                 their licences differ. CIRCL's is different again: it
//                 publishes what it is asked about, where the others only log.
//   the API keys  write-only. The server never returns one, so an input can
//                 never be populated; it returns a "<key>_set" flag instead,
//                 which is the only thing that can say whether a key is
//                 stored. A field that quietly PATCHes "" would wipe a working
//                 key on any unrelated save.
//   a refusal     the server names the offending entry — `"exe" is not an
//                 attachment extension`, `VirusTotal cannot be enabled without
//                 an API key` — and a generic "invalid input" in its place
//                 throws away the only part worth reading. A refusal must also
//                 not cost the operator their other edits.
//   all four off  a supported configuration, not a broken one. It means this
//                 instance judges attachments by its own scanner alone, and
//                 the page says so without nagging.

vi.mock('@tanstack/react-router', () => ({
  useRouterState: () => ({ location: { pathname: '/admin/settings' } }),
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Link: ({ to, children, ...rest }: any) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

import { SettingsPage } from './SettingsPage'

// ── Fixtures ──────────────────────────────────────────────────────────────────

// Deliberately not one default among them, and deliberately not all four
// providers in the same state. A panel that renders the defaults it was written
// with, or that drives four rows off one value, passes a uniform fixture and
// fails this one.
const SETTINGS: Record<string, unknown> = {
  site_name: 'Acme Support',
  attachment_allowed_types: ['.pdf', '.png'],
  attachment_scan_policy: 'permissive',
  attachment_infected_handling: 'quarantine',
  // Deliberately the non-default value, and deliberately not the same
  // word as the setting above it: a page that drives both selects off one
  // key, or that renders the default it was written with, passes a fixture
  // where they agree and fails this one.
  attachment_mismatch_handling: 'wrap',
  attachment_reputation_virustotal_enabled: true,
  attachment_reputation_metadefender_enabled: false,
  attachment_reputation_polyswarm_enabled: false,
  attachment_reputation_circl_enabled: true,
  attachment_reputation_refresh: 'monthly',
  // The keys themselves are never here — they are in secretSettingKeys next to
  // the OIDC client secret, and the settings dump replaces each with a flag
  // saying only whether something is stored. That flag is the whole of what
  // this page can know about them.
  attachment_reputation_virustotal_key_set: true,
  attachment_reputation_metadefender_key_set: false,
  attachment_reputation_polyswarm_key_set: false,
}

/** All four off, which is what every upgraded instance starts as. */
const NOTHING_ENABLED: Record<string, unknown> = {
  ...SETTINGS,
  attachment_reputation_virustotal_enabled: false,
  attachment_reputation_circl_enabled: false,
  attachment_reputation_virustotal_key_set: false,
}

// What the Go side accepts when the operator has never said otherwise, from
// admin.DefaultAllowedTypes(). Duplicated here because the API does not send
// it; the test pins that the page shows this rather than an empty box, since
// an empty box says "this instance accepts nothing", which is false.
const DEFAULT_TYPES = ['.pdf', '.docx', '.xlsx', '.txt', '.log', '.jpg', '.jpeg', '.png', '.bmp']

/** An API refusal in the shape axios hands to a mutation's onError. */
function refusal(status: number, code: string, message: string): AxiosError {
  const err = new AxiosError(`Request failed with status code ${status}`, String(status))
  err.response = {
    status,
    statusText: '',
    headers: {},
    config: {},
    data: { error: { code, message } },
  } as unknown as AxiosResponse
  return err
}

// The server's own words for a bad extension, from handler_admin_settings.go.
const ALLOWED_TYPES_MESSAGE =
  '"exe" is not an attachment extension: each entry is a leading dot ' +
  'followed by 1-16 lowercase letters or digits, e.g. ".pdf"'

// And for a provider enabled with no key, from validateReputationConfig. The
// provider is named, which is the part that must reach the operator.
const NO_KEY_MESSAGE = 'VirusTotal cannot be enabled without an API key'

// And for a mismatch-handling value the server does not accept, from
// handler_admin_settings.go. It names both values, which is the part an
// operator who typed the other setting's word needs to read.
const MISMATCH_HANDLING_MESSAGE =
  'mismatched attachment handling must be one of: refuse, wrap'

function omit(settings: Record<string, unknown>, key: string): Record<string, unknown> {
  const copy = { ...settings }
  delete copy[key]
  return copy
}

// ── Harness ───────────────────────────────────────────────────────────────────

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'admin@example.com',
      display_name: 'Ada Admin',
      role: 'admin',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
})

function mockApi(settings: Record<string, unknown>) {
  vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings') return Promise.resolve({ data: settings })
    // Empty, so the insecure-config banner renders nothing: it is also a
    // role="alert" and would otherwise be picked up by the warning queries.
    if (url === '/admin/security-warnings') return Promise.resolve({ data: { insecure_secrets: [] } })
    if (url === '/site') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
  }) as any)
}

/**
 * Renders the settings page and opens the panel holding these settings.
 *
 * Returns the PATCH spy, because what reaches the wire is most of what these
 * tests are about.
 */
async function renderSettings(settings: Record<string, unknown> = SETTINGS) {
  mockApi(settings)
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: undefined } as any)

  renderWithQuery(<SettingsPage />)
  await screen.findByRole('heading', { name: 'Settings' })

  // Asserted rather than queried straight through getByRole, so that a page
  // with nowhere to put these settings fails saying so.
  const tab = screen
    .queryAllByRole('button')
    .find((b) => /attachment/i.test(b.textContent ?? ''))
  expect(tab, 'the Settings page offers no way to reach the attachment settings').toBeTruthy()
  await userEvent.click(tab!)

  return patch
}

function bodyText(): string {
  return document.body.textContent ?? ''
}

function text(el: HTMLElement): string {
  return el.textContent ?? ''
}

/** The allowed-types field, however it is spelled. */
function typesField(): HTMLTextAreaElement | HTMLInputElement {
  return screen.getByLabelText(/allowed file types/i) as HTMLTextAreaElement
}

function typesEntries(): string[] {
  return typesField()
    .value.split(/[\s,]+/)
    .filter(Boolean)
}

function control(label: RegExp): HTMLSelectElement {
  return screen.getByLabelText(label) as HTMLSelectElement
}

async function save() {
  await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
}

// ── Per-provider helpers ──────────────────────────────────────────────────────

/**
 * The settings for one provider: its toggle, its key field if it has one, and
 * its warning.
 *
 * Asserted rather than thrown from getByRole so a page that has not grouped
 * them fails saying which provider it could not find.
 */
function providerGroup(name: RegExp): HTMLElement {
  const found = screen.queryAllByRole('group', { name })
  expect(found.length, `expected exactly one settings group for ${name}`).toBe(1)
  return found[0]
}

function toggleFor(name: RegExp): HTMLElement {
  const switches = within(providerGroup(name)).queryAllByRole('switch')
  expect(switches.length, `expected exactly one toggle for ${name}`).toBe(1)
  return switches[0]
}

function isOn(name: RegExp): boolean {
  return toggleFor(name).getAttribute('aria-checked') === 'true'
}

async function setToggle(name: RegExp, on: boolean) {
  if (isOn(name) !== on) await userEvent.click(toggleFor(name))
}

/** A provider's API key field, or null where the provider has none. */
function keyFieldFor(name: RegExp): HTMLInputElement | null {
  const inputs = within(providerGroup(name))
    .queryAllByLabelText(/api key/i)
    .filter((el): el is HTMLInputElement => el instanceof HTMLInputElement)
  expect(inputs.length, `expected at most one API key field for ${name}`).toBeLessThan(2)
  return inputs[0] ?? null
}

function requireKeyField(name: RegExp): HTMLInputElement {
  const field = keyFieldFor(name)
  expect(field, `no API key field for ${name}`).not.toBeNull()
  return field!
}

/** A provider's warning callout. */
function warningFor(name: RegExp): HTMLElement {
  const found = within(providerGroup(name)).queryAllByRole('alert')
  expect(found.length, `expected exactly one warning for ${name}`).toBe(1)
  return found[0]
}

/** Every warning callout on the page that talks about a reputation provider. */
function warnings(): HTMLElement[] {
  return screen
    .queryAllByRole('alert')
    .filter((el) => /free api|virustotal|metadefender|opswat|polyswarm|circl/i.test(el.textContent ?? ''))
}

/** What the last PATCH actually sent. */
function lastPatch(patch: ReturnType<typeof vi.spyOn>): Record<string, unknown> {
  expect(patch.mock.calls.length, 'nothing was sent to the server').toBeGreaterThan(0)
  const call = patch.mock.calls[patch.mock.calls.length - 1]
  expect(call[0]).toBe('/admin/settings')
  return call[1] as Record<string, unknown>
}

// ── The settings themselves ───────────────────────────────────────────────────

describe('the attachment security settings', () => {
  it('shows what this instance has stored, not what the defaults are', async () => {
    await renderSettings()

    expect(typesEntries()).toEqual(['.pdf', '.png'])
    expect(control(/scan attachments for malware/i).value).toBe('permissive')
    expect(control(/when a scan finds malware/i).value).toBe('quarantine')
    expect(control(/re-check stored verdicts/i).value).toBe('monthly')

    // Four independent toggles, so four independent answers. A row driven off
    // one shared value passes a fixture where they agree.
    expect(isOn(/virustotal/i)).toBe(true)
    expect(isOn(/metadefender/i)).toBe(false)
    expect(isOn(/polyswarm/i)).toBe(false)
    expect(isOn(/circl/i)).toBe(true)

    // Never populated from the server, because the server never sends a key.
    expect(requireKeyField(/virustotal/i).value).toBe('')
    expect(requireKeyField(/metadefender/i).value).toBe('')
    expect(requireKeyField(/polyswarm/i).value).toBe('')
  })

  // The setting these four replaced, and its single shared key. Both are
  // deprecated and read by nothing on the Go side, so a control still offering
  // them writes a row that changes no behaviour at all — the worst kind of
  // leftover, because it looks like it works.
  it('no longer offers the single-provider setting it replaced', async () => {
    const patch = await renderSettings()

    expect(screen.queryAllByLabelText(/reputation service/i)).toEqual([])

    await setToggle(/polyswarm/i, true)
    await save()

    await waitFor(() => {
      const sent = Object.keys(lastPatch(patch))
      expect(sent).not.toContain('attachment_reputation_provider')
      expect(sent).not.toContain('attachment_reputation_api_key')
    })
  })

  it('sends each toggle under its own key', async () => {
    const patch = await renderSettings()

    await setToggle(/virustotal/i, false)
    await setToggle(/metadefender/i, true)
    await setToggle(/polyswarm/i, true)
    await setToggle(/circl/i, false)
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({
        attachment_reputation_virustotal_enabled: false,
        attachment_reputation_metadefender_enabled: true,
        attachment_reputation_polyswarm_enabled: true,
        attachment_reputation_circl_enabled: false,
      })
    })
  })

  it('sends each edited value under its own key', async () => {
    const patch = await renderSettings()

    await userEvent.clear(typesField())
    await userEvent.type(typesField(), '.pdf\n.exe')
    await userEvent.selectOptions(control(/scan attachments for malware/i), 'required')
    await userEvent.selectOptions(control(/when a scan finds malware/i), 'refuse')
    await userEvent.selectOptions(control(/re-check stored verdicts/i), 'weekly')
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({
        attachment_allowed_types: ['.pdf', '.exe'],
        attachment_scan_policy: 'required',
        attachment_infected_handling: 'refuse',
        attachment_reputation_refresh: 'weekly',
      })
    })
  })

  // An instance that has never set the list is not an instance that accepts
  // nothing: the Go side falls back to DefaultAllowedTypes(). An empty box
  // would tell the operator the opposite, and the first thing they would do
  // about it is type a shorter list than they already have.
  it('shows the default type list when the instance has never set one', async () => {
    const patch = await renderSettings(omit(SETTINGS, 'attachment_allowed_types'))

    expect(typesEntries()).toEqual(DEFAULT_TYPES)

    // And showing them is not the same as choosing them. Saving an unrelated
    // change must not freeze today's defaults into the settings table, where
    // the next reader cannot tell an operator's decision from a default that
    // has since moved.
    await userEvent.selectOptions(control(/re-check stored verdicts/i), 'never')
    await save()
    await waitFor(() => {
      expect(Object.keys(lastPatch(patch))).not.toContain('attachment_allowed_types')
    })
  })

  // #168: an operator with scan_policy off and infected_handling quarantine
  // "has configured nothing at all", because no Infected verdict is ever
  // produced for the setting to act on. The combination reads like it does
  // something, which is exactly why it has to be said out loud.
  it('says that quarantine does nothing while scanning is off', async () => {
    await renderSettings()

    await userEvent.selectOptions(control(/scan attachments for malware/i), 'off')
    expect(bodyText()).toMatch(/nothing is scanned/i)

    await userEvent.selectOptions(control(/scan attachments for malware/i), 'required')
    expect(bodyText()).not.toMatch(/nothing is scanned/i)
  })
})

// ── A file whose content contradicts its name ─────────────────────────────────

// attachment_mismatch_handling (#165) shipped with no control at all: reachable
// only by a raw PATCH, so the triage teams `wrap` exists for could not switch
// it on and everyone else could not see that their instance refuses these
// uploads.
//
// It is deliberately the mirror of attachment_infected_handling — the same
// decision about a different question: does this instance store a file it has
// reason to distrust, or turn it away? So most of what is pinned here is that
// the two are a PAIR and not one control. A select copied from the one above it
// and left wired to the old key looks completely correct on screen, changes the
// wrong setting, and is exactly the mistake the mirror invites.

const MISMATCH = /content contradicts its name/i
const INFECTED = /when a scan finds malware/i

/** The options a select offers, by value and by the words on them. */
function optionValues(select: HTMLSelectElement): string[] {
  return Array.from(select.options).map((o) => o.value)
}

/**
 * The row one control lives on: the nearest ancestor that also carries the
 * control's own label, so an assertion about this setting's wording cannot be
 * satisfied — or broken — by the setting next to it.
 */
function rowFor(select: HTMLSelectElement, label: RegExp): HTMLElement {
  let el: HTMLElement = select
  while (el.parentElement && !label.test(el.textContent ?? '')) {
    el = el.parentElement
  }
  expect(label.test(el.textContent ?? ''), 'the control is not labelled in the page').toBe(true)
  return el
}

describe('what happens to a file whose content contradicts its name', () => {
  it('shows what this instance has stored, not what the default is', async () => {
    await renderSettings()

    expect(control(MISMATCH).value).toBe('wrap')
    // And the setting it mirrors still shows its own stored value, which is a
    // different one.
    expect(control(INFECTED).value).toBe('quarantine')
  })

  // Two values, and `quarantine` is not one of them. It is the word most likely
  // to be typed here by mistake, because it belongs to the setting this one
  // mirrors, and the server refuses it rather than reading it charitably as
  // `wrap` — so a control that offers it hands the operator a save that cannot
  // succeed.
  it('offers refusing and wrapping, and nothing else', async () => {
    await renderSettings()

    expect(optionValues(control(MISMATCH)).sort()).toEqual(['refuse', 'wrap'])
    expect(
      optionValues(control(MISMATCH)),
      'the control offers the other setting’s word, which the server refuses',
    ).not.toContain('quarantine')
  })

  // Refusing is the default, and showing it is not the same as choosing it. An
  // instance that has never set this one must not have "refuse" written into
  // its settings table by an unrelated save, where the next reader cannot tell
  // an operator's decision from a default that has since moved.
  it('shows refusing when the instance has never set it, and does not write it', async () => {
    const patch = await renderSettings(omit(SETTINGS, 'attachment_mismatch_handling'))

    expect(control(MISMATCH).value).toBe('refuse')

    await userEvent.selectOptions(control(/re-check stored verdicts/i), 'never')
    await save()
    await waitFor(() => {
      expect(Object.keys(lastPatch(patch))).not.toContain('attachment_mismatch_handling')
    })
  })

  it('sends it under its own key, leaving the malware setting alone', async () => {
    const patch = await renderSettings()

    await userEvent.selectOptions(control(MISMATCH), 'refuse')

    expect(control(INFECTED).value, 'changing one select moved the other').toBe('quarantine')
    await save()
    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({
        attachment_mismatch_handling: 'refuse',
        attachment_infected_handling: 'quarantine',
      })
    })
  })

  // The same thing from the other side: a select wired to the wrong key passes
  // the test above if the mistake runs the other way.
  it('is not the malware setting wearing a second label', async () => {
    const patch = await renderSettings()

    await userEvent.selectOptions(control(INFECTED), 'refuse')

    expect(control(MISMATCH).value, 'changing the malware select moved this one').toBe('wrap')
    await save()
    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({
        attachment_infected_handling: 'refuse',
        attachment_mismatch_handling: 'wrap',
      })
    })
  })

  // The mirror is in the shape of the decision, not in the words. A wrapped
  // mismatch has no password — there is nothing to contain, only something to
  // say — and nothing found anything wrong with the file, so borrowing the
  // infected tier's vocabulary here makes both tiers mean less and tells the
  // operator something false about what is stored.
  it('does not borrow the malware setting’s words', async () => {
    await renderSettings()
    const row = rowFor(control(MISMATCH), MISMATCH)

    expect(text(row), 'this row promises a password the wrapped file does not have').not.toMatch(
      /password/i,
    )
    expect(text(row), 'a mislabelled file is described as malware').not.toMatch(
      /malware|malicious|infected|virus/i,
    )

    // And it says what the two values actually do, because "refuse" and "wrap"
    // on their own are the setting's words, not an answer to what happens.
    const options = Array.from(control(MISMATCH).options).map((o) => o.textContent ?? '')
    expect(options.join(' | '), 'nothing says the upload is turned away').toMatch(/refus|reject/i)
    expect(options.join(' | '), 'nothing says the file is stored wrapped').toMatch(
      /archive|zip|wrap/i,
    )
  })

  // The server names both accepted values. A generic "invalid input" in its
  // place throws away the only part worth reading, and this is the setting
  // whose likeliest bad value is a word that looks right.
  it('shows the server’s own refusal rather than wording of its own', async () => {
    const patch = await renderSettings()
    patch.mockRejectedValueOnce(
      refusal(400, 'invalid_mismatch_handling', MISMATCH_HANDLING_MESSAGE),
    )

    await userEvent.selectOptions(control(MISMATCH), 'refuse')
    await save()

    await screen.findByText(new RegExp(MISMATCH_HANDLING_MESSAGE))
    expect(bodyText()).not.toMatch(/invalid input|something went wrong|unknown error/i)
    // Nothing was written, so nothing on screen may change.
    expect(control(MISMATCH).value).toBe('refuse')
  })
})

// ── CIRCL has no key ──────────────────────────────────────────────────────────

// "CIRCL has no key field at all — it needs none, and an empty box an operator
// feels obliged to fill is worse than no box." The failure this catches is a
// fourth row built by copying the third: a disabled or empty key box beside
// CIRCL sends somebody looking for a key that does not exist.
describe('CIRCL', () => {
  it('can be enabled with no key field anywhere near it', async () => {
    const patch = await renderSettings(NOTHING_ENABLED)

    expect(keyFieldFor(/circl/i), 'CIRCL has no API key to configure').toBeNull()
    // Not a hidden or disabled one either: nothing in its group takes a value.
    expect(within(providerGroup(/circl/i)).queryAllByRole('textbox')).toEqual([])
    expect(providerGroup(/circl/i).querySelectorAll('input').length).toBe(0)

    await setToggle(/circl/i, true)
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ attachment_reputation_circl_enabled: true })
    })
    expect(Object.keys(lastPatch(patch))).not.toContain('attachment_reputation_circl_key')
  })

  // The other three have one each. A page that renders four key fields, or
  // two, is not showing the configuration the server actually holds.
  it('leaves exactly three key fields on the page', async () => {
    await renderSettings()
    expect(screen.queryAllByLabelText(/api key/i).length).toBe(3)
  })
})

// ── All four off ──────────────────────────────────────────────────────────────

// "All four disabled is a supported configuration, not a broken one." It is
// the state every instance upgrades into, and "nothing configured" is the
// easiest thing to render as though something were wrong.
describe('with no provider enabled', () => {
  it('states what that means, plainly, and does not complain about it', async () => {
    await renderSettings(NOTHING_ENABLED)

    expect(isOn(/virustotal/i)).toBe(false)
    expect(isOn(/metadefender/i)).toBe(false)
    expect(isOn(/polyswarm/i)).toBe(false)
    expect(isOn(/circl/i)).toBe(false)

    // The sentence #168 asks for: a statement about what happens, not a
    // prompt to switch something on.
    expect(bodyText()).toMatch(/judged by this instance's own scanner alone/i)

    // And it is a statement in the page, not an alarm. The only callouts here
    // are the four licence warnings, which belong to the providers.
    const scannerAlone = screen
      .queryAllByRole('alert')
      .filter((el) => /own scanner alone/i.test(el.textContent ?? ''))
    expect(scannerAlone, 'the all-off sentence is a note, not a warning').toEqual([])

    // Nothing anywhere treats this as a fault to be corrected.
    expect(bodyText()).not.toMatch(/not configured|no reputation (service|provider|lookup) is|enable at least/i)
  })

  it('saves without the server or the page objecting', async () => {
    const patch = await renderSettings(SETTINGS)

    await setToggle(/virustotal/i, false)
    await setToggle(/circl/i, false)
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({
        attachment_reputation_virustotal_enabled: false,
        attachment_reputation_circl_enabled: false,
      })
    })
    expect(screen.queryByText(/cannot be enabled|must be enabled|is required/i)).toBeNull()
  })
})

// ── A refusal ─────────────────────────────────────────────────────────────────

describe('when the server refuses a value', () => {
  it("shows the server's own message, which names the offending entry", async () => {
    const patch = await renderSettings()
    patch.mockRejectedValueOnce(refusal(400, 'invalid_allowed_types', ALLOWED_TYPES_MESSAGE))

    await userEvent.clear(typesField())
    await userEvent.type(typesField(), 'exe')
    await save()

    await screen.findByText(new RegExp('"exe" is not an attachment extension'))
  })

  // The failure that costs an operator twenty minutes: one bad extension, and
  // the form resets to what the server holds, taking four unrelated edits with
  // it. Nothing was written, so nothing on screen may change.
  it('keeps every other edit, including a typed API key', async () => {
    const patch = await renderSettings()
    patch.mockRejectedValueOnce(refusal(400, 'invalid_allowed_types', ALLOWED_TYPES_MESSAGE))

    await userEvent.clear(typesField())
    await userEvent.type(typesField(), 'exe')
    await userEvent.selectOptions(control(/when a scan finds malware/i), 'refuse')
    await userEvent.selectOptions(control(/re-check stored verdicts/i), 'never')
    await setToggle(/polyswarm/i, true)
    await userEvent.type(requireKeyField(/polyswarm/i), 'k3y-typed-once')
    await save()

    await screen.findByText(new RegExp('"exe" is not an attachment extension'))

    expect(typesEntries()).toEqual(['exe'])
    expect(control(/when a scan finds malware/i).value).toBe('refuse')
    expect(control(/re-check stored verdicts/i).value).toBe('never')
    expect(isOn(/polyswarm/i)).toBe(true)
    expect(requireKeyField(/polyswarm/i).value).toBe('k3y-typed-once')
  })

  // Enabling a provider with no key is refused by the server, which names the
  // provider. The page must not pre-empt that with wording of its own: only
  // the server knows whether a key is stored, and "VirusTotal cannot be
  // enabled without an API key" is the sentence that says what to do.
  it('shows which provider is missing a key, in the server\'s words', async () => {
    const patch = await renderSettings(NOTHING_ENABLED)
    patch.mockRejectedValueOnce(refusal(400, 'invalid_reputation_config', NO_KEY_MESSAGE))

    await setToggle(/virustotal/i, true)
    await save()

    // The attempt is made — the page does not quietly refuse to send it.
    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ attachment_reputation_virustotal_enabled: true })
    })
    await screen.findByText(new RegExp(NO_KEY_MESSAGE))
    // And the toggle the operator flipped is still flipped, so the fix is to
    // paste a key rather than to start again.
    expect(isOn(/virustotal/i)).toBe(true)
  })

  // The same rule from the other side: an enabled provider whose stored key is
  // removed is the same invalid state, and the server refuses it the same way.
  // The page has to let the request happen — a client-side guard here would be
  // a second copy of a rule that lives on the server.
  it('lets the server refuse a key removed from an enabled provider', async () => {
    const patch = await renderSettings()
    patch.mockRejectedValueOnce(refusal(400, 'invalid_reputation_config', NO_KEY_MESSAGE))

    expect(isOn(/virustotal/i)).toBe(true)
    await removeStoredKey(/virustotal/i)
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ attachment_reputation_virustotal_key: '' })
    })
    await screen.findByText(new RegExp(NO_KEY_MESSAGE))
  })
})

/** Asks for a provider's stored key to be removed, and confirms it. */
async function removeStoredKey(name: RegExp) {
  const group = providerGroup(name)
  await userEvent.click(within(group).getByRole('button', { name: /remove the stored key/i }))
  const dialog = await screen.findByRole('dialog')
  await userEvent.click(within(dialog).getByRole('button', { name: /remove/i }))
}

// ── The warnings ──────────────────────────────────────────────────────────────

describe('the licensing and rate-limit warnings', () => {
  it("states VirusTotal's limits and licence, quoted and attributed", async () => {
    await renderSettings()

    const w = text(warningFor(/virustotal/i))
    // The limit decides whether the feature works for them…
    expect(w).toContain('4 requests per minute and 500 per day')
    expect(w).toContain('00:00 UTC')
    expect(w).toMatch(/never as clean/i)
    // …and the licence decides whether they should switch it on at all.
    expect(w).toContain('must not be used in commercial products or services')
    expect(w).toContain('must not be used in business workflows that do not contribute new files')
    expect(w).toMatch(/permanent ban of the individual or organization/i)
    // Attributed. We are not the licensing authority and must not read as one.
    expect(w).toMatch(/VirusTotal state/)
    // And it says what we do, so "commercial" is judged against the right facts.
    expect(w).toMatch(/never uploads a file/i)

    const link = within(warningFor(/virustotal/i)).getByRole('link', { name: /public vs premium/i })
    expect(link.getAttribute('href')).toBe(
      'https://docs.virustotal.com/reference/public-vs-premium-api',
    )
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toMatch(/noreferrer/)
    expect(link.getAttribute('rel')).toMatch(/noopener/)
  })

  it("states MetaDefender's own limits and licence, not VirusTotal's", async () => {
    await renderSettings()

    const w = text(warningFor(/metadefender/i))
    expect(w).toContain('4,000')
    expect(w).toMatch(/OPSWAT/)

    // The clause OPSWAT actually publish. An earlier draft of this feature
    // quoted "personal, non-commercial use" with a carve-out for a
    // "non-commercial personal or organizational capacity"; neither phrase is
    // in their terms, and the real one is NARROWER — it tells an operator they
    // have less room, not more. Putting the invented wording back would tell
    // somebody a non-commercial organisation may use the free tier, about a
    // service whose stated penalty is a permanent ban.
    expect(w).toContain('solely for Your personal use')
    expect(w).not.toContain('personal, non-commercial use')
    expect(w).not.toContain('non-commercial personal or organizational capacity')

    // The 4,000 is ours, not theirs: OPSWAT publish no number. Stating it as
    // their published limit would attribute a figure to them that they have
    // never said, which is the same error as the quote above in a quieter form.
    expect(w).toMatch(/this instance|Go Help Desk/)
    expect(w).toContain('a limited number of API calls per day')

    // VirusTotal's numbers and VirusTotal's terms describe VirusTotal. Shown
    // against another provider they are simply false.
    expect(w).not.toContain('4 requests per minute')
    expect(w).not.toContain('500 per day')
    expect(w).not.toContain('must not be used in commercial products or services')

    const link = within(warningFor(/metadefender/i)).getByRole('link', { name: /opswat/i })
    expect(link.getAttribute('href')).toMatch(/^https:\/\//)
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toMatch(/noreferrer/)
    expect(link.getAttribute('rel')).toMatch(/noopener/)
  })

  // PolySwarm is the only one of the four with no commercial-use restriction,
  // which is the reason an operator would choose it — and the caveat is the
  // date, because terms from 2018 predate the API this instance calls. Stating
  // the first without the second sells a licensing position that may not hold.
  it("states PolySwarm's limits and the age of its terms", async () => {
    await renderSettings()

    const w = text(warningFor(/polyswarm/i))
    expect(w).toContain('60')
    expect(w).toMatch(/hour/i)
    expect(w).toMatch(/no (commercial-use|commercial use) restriction/i)
    expect(w).toContain('2018')
    expect(w).toMatch(/PolySwarm/)

    // The licence grant, from the live page rather than from #168, which
    // asserts the absence of a field-of-use clause without ever quoting the
    // clause that would carry one. An operator being told they may use this
    // commercially should be able to read the sentence that lets them.
    expect(w).toContain(
      'non-exclusive, non-sublicenseable, non-transferable, revocable license to access and use the PolySwarm Products',
    )
    const link = within(warningFor(/polyswarm/i)).getByRole('link', { name: /polyswarm/i })
    expect(link.getAttribute('href')).toBe('https://polyswarm.io/terms')
    expect(link.getAttribute('rel')).toMatch(/noreferrer/)
    expect(link.getAttribute('rel')).toMatch(/noopener/)

    // Their limits are not the others'. A row built by copying VirusTotal's
    // would say 500 a day here.
    expect(w).not.toContain('500 per day')
    expect(w).not.toContain('4,000')
  })

  // CIRCL's is different in kind, not just in wording: the other three log
  // what they are asked, this one publishes it. An operator deciding whether
  // customer file hashes may go there needs that fact and cannot get it from
  // the shared copy.
  it('says that CIRCL publishes what it is asked about', async () => {
    await renderSettings()

    const w = text(warningFor(/circl/i))
    expect(w).toMatch(/publish/i)
    expect(w).toMatch(/most-queried|leaderboard|stats\/top/i)
    expect(w).toMatch(/filename/i)
    // CC-BY, with the attribution the licence asks for, and no terms page to
    // point at — the honest position rather than a smoothed-over one.
    expect(w).toMatch(/CC-BY/)
    expect(w).toMatch(/Computer Incident Response Center Luxembourg/)

    // And the disclosure sentence belongs to CIRCL alone. Shared copy that
    // said this about every provider would be false about three of them.
    expect(text(warningFor(/virustotal/i))).not.toMatch(/publish/i)
    expect(text(warningFor(/metadefender/i))).not.toMatch(/publish/i)
  })

  // "It is not dismissible and it does not hide when the lookup is switched
  // off. Someone reading the settings page to decide whether to turn it on is
  // exactly the person who needs it."
  it('cannot be dismissed, and shows in both toggle states', async () => {
    await renderSettings(NOTHING_ENABLED)

    for (const name of [/virustotal/i, /metadefender/i, /polyswarm/i, /circl/i]) {
      // Nothing inside one closes it. Links are not buttons; a × would be.
      expect(within(warningFor(name)).queryAllByRole('button')).toEqual([])
      // Off — the state an operator is in while deciding.
      expect(isOn(name)).toBe(false)
      expect(text(warningFor(name)).length).toBeGreaterThan(0)

      await setToggle(name, true)
      expect(text(warningFor(name)).length).toBeGreaterThan(0)
    }

    // Still four, and still one each, after every toggle has moved.
    expect(warnings().length).toBe(4)
  })

  // Typing a key is not consent to the licence, and the operator has not saved
  // anything yet either.
  it('stays while a key is being typed', async () => {
    await renderSettings()

    await userEvent.type(requireKeyField(/metadefender/i), 'a-key')
    expect(text(warningFor(/metadefender/i))).toContain('solely for Your personal use')
  })

  // "One warning, the facts, the link. No second warning elsewhere."
  it('appears exactly once per provider', async () => {
    await renderSettings()
    expect(warnings().length).toBe(4)
  })
})

// ── The write-only API keys ───────────────────────────────────────────────────

describe('the reputation API keys', () => {
  // The dump returns "<key>_set" and never the key. That flag is the only way
  // this page can tell a configured provider from an unconfigured one, and
  // getting it wrong in either direction misleads: an operator told no key is
  // stored pastes a new one they did not need, and one told a key is stored
  // goes looking elsewhere for why the lookups are silent.
  it('says which providers have a key stored, without ever showing one', async () => {
    await renderSettings()

    expect(requireKeyField(/virustotal/i).type).toBe('password')
    expect(text(providerGroup(/virustotal/i))).toMatch(/a key is stored/i)
    expect(text(providerGroup(/metadefender/i))).toMatch(/no key is stored/i)
    expect(text(providerGroup(/polyswarm/i))).toMatch(/no key is stored/i)
    expect(bodyText()).toMatch(/never sends a stored key back/i)
    expect(bodyText()).toMatch(/leaving (this|it) blank/i)
  })

  it('follows the flag rather than the toggle', async () => {
    // MetaDefender off with a key stored, VirusTotal on with none: the two
    // facts are independent, and a page that reads one off the other says the
    // opposite of the truth in both rows.
    await renderSettings({
      ...SETTINGS,
      attachment_reputation_metadefender_enabled: false,
      attachment_reputation_metadefender_key_set: true,
      attachment_reputation_virustotal_enabled: true,
      attachment_reputation_virustotal_key_set: false,
    })

    expect(text(providerGroup(/metadefender/i))).toMatch(/a key is stored/i)
    expect(text(providerGroup(/virustotal/i))).toMatch(/no key is stored/i)
  })

  // The bad afternoon this prevents: an operator edits the refresh interval,
  // saves, and three empty key fields PATCH "" over three working keys.
  // Nothing reports it; the lookups simply stop.
  it('are not sent at all when the fields are left blank', async () => {
    const patch = await renderSettings()

    await userEvent.selectOptions(control(/re-check stored verdicts/i), 'weekly')
    await save()

    await waitFor(() => {
      const sent = Object.keys(lastPatch(patch))
      expect(sent).not.toContain('attachment_reputation_virustotal_key')
      expect(sent).not.toContain('attachment_reputation_metadefender_key')
      expect(sent).not.toContain('attachment_reputation_polyswarm_key')
    })
  })

  // Three fields, three settings. One shared piece of state behind them sends
  // the same key to every provider — which pastes a VirusTotal key into a
  // PolySwarm account, and is exactly the mistake separate keys exist to
  // prevent.
  it('go to their own provider and nowhere else', async () => {
    const patch = await renderSettings()

    await userEvent.type(requireKeyField(/polyswarm/i), 'ps-s3cret')
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ attachment_reputation_polyswarm_key: 'ps-s3cret' })
    })
    const sent = Object.keys(lastPatch(patch))
    expect(sent).not.toContain('attachment_reputation_virustotal_key')
    expect(sent).not.toContain('attachment_reputation_metadefender_key')

    // And a typed key does not stay on screen after it has been accepted.
    await waitFor(() => expect(requireKeyField(/polyswarm/i).value).toBe(''))
    expect(requireKeyField(/virustotal/i).value).toBe('')
  })

  it('can each be sent in the same save', async () => {
    const patch = await renderSettings()

    await userEvent.type(requireKeyField(/virustotal/i), 'vt-key')
    await userEvent.type(requireKeyField(/metadefender/i), 'md-key')
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({
        attachment_reputation_virustotal_key: 'vt-key',
        attachment_reputation_metadefender_key: 'md-key',
      })
    })
  })

  it('are cleared only through a deliberate, confirmed request', async () => {
    const patch = await renderSettings()

    await userEvent.click(
      within(providerGroup(/virustotal/i)).getByRole('button', { name: /remove the stored key/i }),
    )
    // Asking is not doing: nothing reaches the server until the operator
    // confirms and then saves.
    expect(patch).not.toHaveBeenCalled()

    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: /remove/i }))

    // Still nothing sent, and the page says what the next save will do.
    expect(patch).not.toHaveBeenCalled()
    expect(text(providerGroup(/virustotal/i))).toMatch(/will be removed when you save/i)

    await save()
    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ attachment_reputation_virustotal_key: '' })
    })
    // One provider's removal is not another's.
    const sent = Object.keys(lastPatch(patch))
    expect(sent).not.toContain('attachment_reputation_metadefender_key')
    expect(sent).not.toContain('attachment_reputation_polyswarm_key')
  })
})

// ── The hash link, which is not a lookup ──────────────────────────────────────

// "A link is not a lookup." The VirusTotal link on an attachment is there
// whatever these toggles say, because clicking it is the analyst's own act in
// their own browser. Without a line saying so, an operator who switched
// VirusTotal off and still sees the link will reasonably conclude the setting
// does not work.
describe('the always-present VirusTotal link on attachments', () => {
  it('is explained where the toggles are', async () => {
    await renderSettings(NOTHING_ENABLED)

    expect(bodyText()).toMatch(/own browser/i)
    expect(bodyText()).toMatch(/sends nothing/i)
  })
})
