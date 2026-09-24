import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import { useAuthStore } from '@/store/auth'

// The admin surface for the six attachment-security settings (#165, #167,
// #168).
//
// All six shipped with no way to reach them except a raw PATCH: an operator
// could not turn quarantine on, could not change what this instance accepts,
// and could not configure a reputation lookup. The features existed and were
// invisible.
//
// Driven through SettingsPage rather than through a component, for the same
// reason the ticket-side suites drive TicketDetailPage: the requirement is
// about what an administrator can see and change, and how the page is split up
// is the implementer's business. Every assertion below is phrased so that any
// arrangement of panels and components can satisfy it.
//
// Three things here are not ordinary form plumbing and are the reason most of
// these tests exist:
//
//   the warning   #168 specifies it word for word, with four rules: not
//                 dismissible, shown whether or not a key is configured,
//                 quoting the provider and attributing the quote to them,
//                 and no second warning elsewhere. Each provider carries its
//                 own, because their licences differ.
//   the API key   write-only. The server never returns it, so the input can
//                 never be populated, and a field that quietly PATCHes ""
//                 would wipe a working key on any unrelated save.
//   a refusal     the server names the offending entry — `"exe" is not an
//                 attachment extension` — and a generic "invalid input" in
//                 its place throws away the only part worth reading. A
//                 refusal must also not cost the operator their other edits.

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

// Deliberately not one default among them. A panel that renders the defaults it
// was written with, and ignores what the instance actually stores, passes a
// fixture built out of defaults and fails this one.
const SETTINGS: Record<string, unknown> = {
  site_name: 'Acme Support',
  attachment_allowed_types: ['.pdf', '.png'],
  attachment_scan_policy: 'permissive',
  attachment_infected_handling: 'quarantine',
  attachment_reputation_provider: 'metadefender',
  attachment_reputation_refresh: 'monthly',
  // Note what is NOT here: attachment_reputation_api_key. The settings dump
  // never returns it — it is in secretSettingKeys next to the OIDC client
  // secret — which is the whole difficulty of that field.
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

function keyField(): HTMLInputElement {
  return screen.getByLabelText(/api key/i) as HTMLInputElement
}

async function save() {
  await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
}

/** Every warning callout on the page that talks about a reputation provider. */
function warnings(): HTMLElement[] {
  return screen
    .queryAllByRole('alert')
    .filter((el) => /free api|virustotal|metadefender|opswat/i.test(el.textContent ?? ''))
}

function warning(): HTMLElement {
  const found = warnings()
  expect(found.length, 'expected exactly one reputation warning on the page').toBe(1)
  return found[0]
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
    expect(control(/reputation service/i).value).toBe('metadefender')
    expect(control(/re-check stored verdicts/i).value).toBe('monthly')
    // Never populated from the server, because the server never sends it.
    expect(keyField().value).toBe('')
  })

  it('sends each edited value under its own key', async () => {
    const patch = await renderSettings()

    await userEvent.clear(typesField())
    await userEvent.type(typesField(), '.pdf\n.exe')
    await userEvent.selectOptions(control(/scan attachments for malware/i), 'required')
    await userEvent.selectOptions(control(/when a scan finds malware/i), 'refuse')
    await userEvent.selectOptions(control(/reputation service/i), 'virustotal')
    await userEvent.selectOptions(control(/re-check stored verdicts/i), 'weekly')
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({
        attachment_allowed_types: ['.pdf', '.exe'],
        attachment_scan_policy: 'required',
        attachment_infected_handling: 'refuse',
        attachment_reputation_provider: 'virustotal',
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
    await userEvent.type(keyField(), 'k3y-typed-once')
    await save()

    await screen.findByText(new RegExp('"exe" is not an attachment extension'))

    expect(typesEntries()).toEqual(['exe'])
    expect(control(/when a scan finds malware/i).value).toBe('refuse')
    expect(control(/re-check stored verdicts/i).value).toBe('never')
    expect(keyField().value).toBe('k3y-typed-once')
  })
})

// ── The warning ───────────────────────────────────────────────────────────────

describe('the licensing and rate-limit warning', () => {
  it("states VirusTotal's limits and licence, quoted and attributed", async () => {
    await renderSettings({ ...SETTINGS, attachment_reputation_provider: 'virustotal' })

    const w = text(warning())
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

    const link = within(warning()).getByRole('link', { name: /public vs premium/i })
    expect(link.getAttribute('href')).toBe(
      'https://docs.virustotal.com/reference/public-vs-premium-api',
    )
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toMatch(/noreferrer/)
    expect(link.getAttribute('rel')).toMatch(/noopener/)
  })

  it("states MetaDefender's own limits and licence, not VirusTotal's", async () => {
    await renderSettings()

    const w = text(warning())
    expect(w).toContain('4,000')
    expect(w).toMatch(/OPSWAT/)
    expect(w).toMatch(/personal use/i)
    // VirusTotal's numbers and VirusTotal's terms describe VirusTotal. Shown
    // against the other provider they are simply false.
    expect(w).not.toContain('4 requests per minute')
    expect(w).not.toContain('500 per day')
    expect(w).not.toContain('must not be used in commercial products or services')

    const link = within(warning()).getByRole('link', { name: /opswat/i })
    expect(link.getAttribute('href')).toMatch(/^https:\/\//)
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toMatch(/noreferrer/)
    expect(link.getAttribute('rel')).toMatch(/noopener/)
  })

  it('follows the selected provider', async () => {
    await renderSettings()
    expect(text(warning())).toMatch(/OPSWAT/)

    await userEvent.selectOptions(control(/reputation service/i), 'virustotal')
    expect(text(warning())).toContain('4 requests per minute and 500 per day')
    expect(text(warning())).not.toMatch(/OPSWAT/)

    await userEvent.selectOptions(control(/reputation service/i), 'metadefender')
    expect(text(warning())).toMatch(/OPSWAT/)
  })

  // "It is not dismissible and it does not hide when the lookup is switched
  // off. Someone reading the settings page to decide whether to turn it on is
  // exactly the person who needs it."
  it('cannot be dismissed, and shows whether or not a key is being set', async () => {
    await renderSettings({ ...SETTINGS, attachment_reputation_provider: 'virustotal' })

    // Nothing inside it closes it. Links are not buttons; a × would be.
    expect(within(warning()).queryAllByRole('button')).toEqual([])

    // No key entered — the state an operator is in while deciding.
    expect(text(warning())).toContain('must not be used in commercial products or services')

    await userEvent.type(keyField(), 'a-key')
    expect(text(warning())).toContain('must not be used in commercial products or services')
  })

  // "One warning, the facts, the link. No second warning elsewhere."
  it('appears exactly once', async () => {
    await renderSettings()
    expect(warnings().length).toBe(1)
  })
})

// ── The write-only API key ────────────────────────────────────────────────────

describe('the reputation API key', () => {
  it('says that a stored key cannot be shown, rather than implying none is set', async () => {
    await renderSettings()
    expect(keyField().type).toBe('password')
    expect(bodyText()).toMatch(/never sends a stored key back/i)
    expect(bodyText()).toMatch(/leaving (this|it) blank/i)
  })

  // The bad afternoon this prevents: an operator edits the refresh interval,
  // saves, and the empty key field PATCHes "" over a working key. Nothing
  // reports it; the lookups simply stop.
  it('is not sent at all when the field is left blank', async () => {
    const patch = await renderSettings()

    await userEvent.selectOptions(control(/re-check stored verdicts/i), 'weekly')
    await save()

    await waitFor(() => {
      expect(Object.keys(lastPatch(patch))).not.toContain('attachment_reputation_api_key')
    })
  })

  it('is sent when one is typed, and is not left on screen afterwards', async () => {
    const patch = await renderSettings()

    await userEvent.type(keyField(), 's3cret-key')
    await save()

    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ attachment_reputation_api_key: 's3cret-key' })
    })
    await waitFor(() => expect(keyField().value).toBe(''))
  })

  it('is cleared only through a deliberate, confirmed request', async () => {
    const patch = await renderSettings()

    await userEvent.click(screen.getByRole('button', { name: /remove the stored key/i }))
    // Asking is not doing: nothing reaches the server until the operator
    // confirms and then saves.
    expect(patch).not.toHaveBeenCalled()

    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: /remove/i }))

    // Still nothing sent, and the page says what the next save will do.
    expect(patch).not.toHaveBeenCalled()
    expect(bodyText()).toMatch(/will be removed when you save/i)

    await save()
    await waitFor(() => {
      expect(lastPatch(patch)).toMatchObject({ attachment_reputation_api_key: '' })
    })
  })
})
