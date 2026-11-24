import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

/**
 * The four reputation services, in the order the page shows them.
 *
 * Four independent toggles rather than one selected provider: they answer
 * different questions — VirusTotal counts engines, CIRCL says whether a
 * catalogue holds the file — and every enabled one is asked. Each carries its
 * own key, so configuring a second service does not destroy the key you pasted
 * for the first.
 *
 * `keySetting` is null for CIRCL alone, and the absence is the decision:
 * hashlookup authenticates nobody, so there is no key an operator could
 * supply, and an empty box beside the other three is a box somebody feels
 * obliged to fill and then goes looking for a fault when the feature works
 * without it.
 *
 * Every key here is write-only over the API — they are in secretSettingKeys
 * beside the OIDC client secret — so the dump returns `<setting>_set`, a
 * boolean saying only whether something is stored, and never the key itself.
 */
const REPUTATION_PROVIDERS: {
  id: string
  name: string
  description: string
  enabledSetting: string
  keySetting: string | null
  Terms: () => React.JSX.Element
}[] = [
  {
    id: 'virustotal',
    name: 'VirusTotal',
    description:
      'Seventy-odd engines on one file. The broadest answer of the four, and the page every analyst already knows.',
    enabledSetting: 'attachment_reputation_virustotal_enabled',
    keySetting: 'attachment_reputation_virustotal_key',
    Terms: VirusTotalTerms,
  },
  {
    id: 'metadefender',
    name: 'MetaDefender',
    description: "OPSWAT's cloud, with a smaller engine set and a larger daily allowance.",
    enabledSetting: 'attachment_reputation_metadefender_enabled',
    keySetting: 'attachment_reputation_metadefender_key',
    Terms: MetaDefenderTerms,
  },
  {
    id: 'polyswarm',
    name: 'PolySwarm',
    description:
      'The only one of the four that can say a file is a known legitimate binary, vouched for by a named vendor feed.',
    enabledSetting: 'attachment_reputation_polyswarm_enabled',
    keySetting: 'attachment_reputation_polyswarm_key',
    Terms: PolySwarmTerms,
  },
  {
    id: 'circl',
    name: 'CIRCL',
    description:
      'A catalogue rather than a scanner: it answers whether a hash set it re-publishes holds this file, never whether an engine flagged it. No key, and no account.',
    enabledSetting: 'attachment_reputation_circl_enabled',
    keySetting: null,
    Terms: CIRCLTerms,
  },
]

/**
 * What one provider says about their own free tier, in their words.
 *
 * Four rules from #168, all deliberate:
 *
 *   not dismissible      "a warning with an × becomes a warning nobody sees
 *                        twice". There is no control in here at all.
 *   always shown         including when the provider is switched off and when
 *                        no key is configured. Someone reading this page to
 *                        decide whether to switch it on is exactly the person
 *                        who needs it.
 *   quoted, attributed   we are not the licensing authority and must not read
 *                        as one. Quote them; let the operator judge their own
 *                        deployment. Anything we cannot find on the vendor's
 *                        own page does not go in quotation marks — the last
 *                        time this feature quoted a vendor's terms from
 *                        memory, the quote turned out not to exist and was
 *                        wrong in the permissive direction.
 *   one per provider     and no second warning elsewhere, and no confirmation
 *                        dialog on a toggle. That is reserved for downloading
 *                        an infected file, where the risk is immediate.
 *
 * Both halves are in each because they answer different questions: the limit
 * decides whether the feature works for them, the licence decides whether they
 * should switch it on at all.
 *
 * role="alert" follows InsecureConfigBanner, the one existing banner in this
 * codebase — consistency with the precedent, and the content does warrant
 * interrupting somebody who is about to paste a key in.
 */
function ReputationWarning({ children }: { children: React.ReactNode }) {
  return (
    <div
      role="alert"
      className="space-y-2 rounded border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900"
    >
      {children}
    </div>
  )
}

/** A link out of a warning. Never a navigation of ours. */
function TermsLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <a className="font-medium underline" href={href} target="_blank" rel="noreferrer noopener">
      {children}
    </a>
  )
}

/* The VirusTotal copy is specified word for word in #168 and is quoted rather
   than summarised. Softening it is the failure mode it exists to prevent: the
   penalty VirusTotal state is a ban on the operator's whole organisation, not
   on the key they pasted in here. */
function VirusTotalTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ VirusTotal's free API has limits, and is not licensed for commercial use
      </p>
      {/* #168 specifies this paragraph, and the first clause of it was written
          before verdicts expired: "looks up each infected attachment once and
          stores the result" stopped being true when the re-check interval
          below arrived. Reworded rather than left standing, because the
          difference is how much of the operator's allowance this spends. */}
      <p>
        The free public API allows <strong>4 requests per minute and 500 per day</strong>. Go Help Desk asks
        once for each quarantined attachment, stores the answer and asks again only when the interval below
        has passed, but on a busy instance some lookups will be skipped until the quota resets at 00:00 UTC.
        Skipped lookups show as <em>not checked</em> — never as clean.
      </p>
      <p>
        VirusTotal state that the public API “must not be used in commercial products or services”, and
        “must not be used in business workflows that do not contribute new files”. This feature only looks
        hashes up; it never uploads a file. If you are running Go Help Desk commercially, you need a paid
        VirusTotal API key. VirusTotal state the penalty for non-compliance as a permanent ban of the
        individual or organization.
      </p>
      <p>
        <TermsLink href="https://docs.virustotal.com/reference/public-vs-premium-api">
          VirusTotal: public vs premium API ↗
        </TermsLink>
      </p>
    </>
  )
}

/* MetaDefender's terms are not VirusTotal's, so the copy is not VirusTotal's
   either — the same shape, their numbers, their words.

   The quoted clause is from OPSWAT's current Terms of Service, section 6,
   read live rather than from #168: the issue quotes "personal, non-commercial
   use" with a carve-out for a "non-commercial personal or organizational
   capacity", and neither phrase appears in the published terms today. What
   they do say about free access is quoted here verbatim, and it is narrower
   than the issue's wording rather than broader, so the operator is not being
   told they have more room than they have. */
function MetaDefenderTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ MetaDefender's free API has limits, and is licensed for personal use
      </p>
      {/* The 4,000 is ours and not theirs, and the sentence has to say so.
          OPSWAT state only "a limited number of API calls per day"; the number
          is in budget.go, where we chose it. Printing it as their published
          limit attributes a figure to them they have never given, which is the
          same error as the invented quote above in a quieter form. */}
      <p>
        OPSWAT give no figure for the free tier — their API documentation says only “a limited number of API
        calls per day” — so <strong>this instance caps itself at 4,000 lookups a day</strong> and does not
        throttle them by the minute, because OPSWAT do not throttle single hash lookups. Go Help Desk asks
        once for each quarantined attachment, stores the answer and asks again only when the interval below
        has passed. Lookups beyond the cap show as <em>not checked</em> — never as clean.
      </p>
      <p>
        OPSWAT state that free access to their services is licensed “solely for Your personal use”. There is
        no carve-out for an organization. This feature only looks hashes up; it never uploads a file. A help
        desk is not a personal use of the service, so read their terms against your own deployment before
        switching this on; OPSWAT sell paid MetaDefender Cloud plans for organizational use.
      </p>
      <p className="flex flex-wrap gap-x-4">
        <TermsLink href="https://www.opswat.com/legal/terms-of-service">
          OPSWAT Terms of Service ↗
        </TermsLink>
        <TermsLink href="https://docs.opswat.com/mdcloud">
          MetaDefender Cloud documentation ↗
        </TermsLink>
      </p>
    </>
  )
}

/* PolySwarm is the one an operator reaches for when their lawyer has rejected
   the other two, so the licensing half is the half that matters — and what
   makes their position good is an ABSENCE: no field-of-use clause anywhere in
   the grant.

   The grant itself is quoted, read live from https://polyswarm.io/terms rather
   than taken from #168, which asserts the absence without ever quoting the
   clause that would carry a restriction. Same rule as the OPSWAT paragraph
   above and for the same reason: an operator told they may use this
   commercially should be able to read the sentence that says so, and a quote
   nobody checked is how this feature already shipped one that did not exist.

   What is NOT in quotation marks is the absence, because an absence has no
   words. It is stated as our reading of their document, with its date beside
   it. */
function PolySwarmTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ PolySwarm's free tier is 60 lookups an hour, and its published terms are from 2018
      </p>
      <p>
        The free tier allows <strong>60 lookups an hour</strong>, with no daily figure published. It is an
        hourly bucket rather than a pace: a ticket carrying ten quarantined attachments spends ten of the
        sixty at once, and the hour has to pass before they come back. Go Help Desk asks once for each
        quarantined attachment, stores the answer and asks again only when the interval below has passed. Lookups beyond the allowance show as <em>not checked</em> —
        never as clean.
      </p>
      <p>
        PolySwarm grant “a non-exclusive, non-sublicenseable, non-transferable, revocable license to access
        and use the PolySwarm Products strictly in accordance with these Terms”, and attach no commercial-use
        restriction to it — no non-commercial, personal-use or internal-use clause appears anywhere in their
        terms. That makes them the only one of these four an organization can use without a licensing
        argument. The caveat is the date: their terms are dated 30 April 2018 and predate the API this
        instance calls, so the absence may be their policy or may be staleness. Read them before relying on
        it.
      </p>
      <p>
        <TermsLink href="https://polyswarm.io/terms">PolySwarm Terms of Service ↗</TermsLink>
      </p>
    </>
  )
}

/* CIRCL's warning is not a variation on the other three, and it must not read
   like one. Two facts are theirs alone:

     it publishes    /stats/top serves the most-queried hashes with filenames.
                     VirusTotal and OPSWAT log what they are asked; this one
                     puts it on a public page. An operator weighing whether
                     customers' file hashes may go there cannot get that from
                     shared copy.
     no terms        there is no terms-of-service page for hashlookup at all.
                     The licence is CC-BY, which permits commercial use, but
                     CIRCL nowhere say so in their own words — so that is
                     stated as the position it is rather than smoothed into a
                     permission. CC-BY asks for attribution, and the line it
                     asks for is here. */
function CIRCLTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ CIRCL publishes what it is asked about, and has no terms of service to read
      </p>
      <p>
        hashlookup needs no key and no account, and it is the only one of the four that makes its traffic
        public: <span className="font-mono">/stats/top</span> serves the hundred most-queried hashes with
        their filenames and query counts, alongside the hashes it is most often asked about and has no
        record of. Each lookup is recorded against the caller's IP address and User-Agent. The other three
        log what they are asked; this one publishes it, so a customer's attachment hash — and whatever
        filename CIRCL holds for it — can appear on a public page.
      </p>
      <p>
        CIRCL state no rate limit, describing the service as “free and served as a best-effort basis”, so
        the <strong>300 lookups an hour</strong> this instance allows itself is a courtesy of ours rather
        than a limit of theirs.
      </p>
      <p>
        There is no terms-of-service page for hashlookup. The service declares its licence as CC-BY and the
        dataset is released as CC-BY-4.0, which permits commercial use — but CIRCL nowhere say so in their
        own words, which is why none of that is in quotation marks. CC-BY asks for attribution, and this
        instance gives it: Hash reputation data from CIRCL hashlookup, Computer Incident Response Center
        Luxembourg, CC-BY-4.0.
      </p>
      <p>
        <TermsLink href="https://hashlookup.circl.lu">CIRCL hashlookup ↗</TermsLink>
      </p>
    </>
  )
}

/**
 * One provider: its toggle, its key if it has one, and its warning.
 *
 * Grouped and named rather than laid out as three unrelated rows, because the
 * page now carries three password boxes all labelled "API key" and a reader
 * arriving at one — by tab, by screen reader, or by eye — has to be able to
 * tell whose account they are about to paste a key into.
 *
 * Nothing here pre-empts the server's rule that an enabled provider needs a
 * key. The server refuses that write with invalid_reputation_config and names
 * the provider; a second copy of the rule in here would be one more thing to
 * drift, and it would have to guess at a stored key it cannot read.
 */
function ReputationProviderRow({
  provider, enabled, setEnabled, keyStored, typedKey, setTypedKey, cleared, setCleared,
}: {
  provider: (typeof REPUTATION_PROVIDERS)[number]
  enabled: boolean
  setEnabled: (v: boolean) => void
  /** From the dump's "<setting>_set" flag — the only thing it says about a key. */
  keyStored: boolean
  typedKey: string
  setTypedKey: (v: string) => void
  cleared: boolean
  setCleared: (v: boolean) => void
}) {
  const [confirmClear, setConfirmClear] = useState(false)
  const fieldId = `reputation-key-${provider.id}`

  return (
    <div role="group" aria-label={provider.name} className="space-y-3 px-5 py-4">
      <div className="flex items-center justify-between gap-8">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium text-gray-900">{provider.name}</div>
          <div className="mt-0.5 text-sm text-gray-500">{provider.description}</div>
        </div>
        <div className="shrink-0">
          <Toggle label={`Ask ${provider.name}`} checked={enabled} onChange={setEnabled} />
        </div>
      </div>

      {provider.keySetting && (
        <div className="space-y-2">
          <label htmlFor={fieldId} className="block text-sm font-medium text-gray-900">
            {provider.name} API key
          </label>
          <Input
            id={fieldId}
            type="password"
            autoComplete="off"
            className="max-w-lg font-mono text-sm"
            placeholder={cleared ? 'The stored key will be removed' : 'Paste a key to set or replace one'}
            value={typedKey}
            disabled={cleared}
            onChange={(e) => setTypedKey(e.target.value)}
          />
          <p className="text-xs text-gray-500">
            {keyStored ? 'A key is stored.' : 'No key is stored.'} Write-only: the server never sends a
            stored key back, so this field cannot be filled in for you. Leaving it blank leaves whatever is
            stored untouched — saving an empty field never removes a working key.
          </p>

          {cleared ? (
            <div className="flex items-center gap-3">
              <p className="text-sm text-amber-700">The stored key will be removed when you save.</p>
              <Button variant="outline" size="sm" onClick={() => setCleared(false)}>
                Keep the stored key
              </Button>
            </div>
          ) : (
            keyStored && (
              <Button variant="outline" size="sm" onClick={() => setConfirmClear(true)}>
                Remove the stored key
              </Button>
            )
          )}
          <ConfirmDialog
            open={confirmClear}
            onOpenChange={setConfirmClear}
            title={`Remove the stored ${provider.name} API key?`}
            description={`${provider.name} lookups stop until a new key is saved, and the save is refused outright while ${provider.name} is switched on above. Stored verdicts are kept, and nothing is removed until you save.`}
            confirmLabel="Remove the key"
            onConfirm={() => {
              setCleared(true)
              setConfirmClear(false)
            }}
          />
        </div>
      )}

      <ReputationWarning>
        <provider.Terms />
      </ReputationWarning>
    </div>
  )
}

export function ReputationSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool, str, setStr, secret, setSecret, secretCleared, setSecretCleared } = draft
  const refresh = str('attachment_reputation_refresh') || 'biweekly'
  return (
    <Section title="Reputation lookup">
      {/* What the toggles below do, and — the part that has to be said out
          loud — what all of them off means. That is the state every instance
          upgrades into, it is a legitimate choice for an operator who cannot
          send customer file hashes anywhere, and "nothing configured" is the
          easiest thing in a settings page to render as though something were
          wrong. One statement, not a prompt. */}
      <div className="space-y-2 px-5 py-4 text-sm text-gray-500">
        <p>
          A quarantined attachment's SHA-256 can be looked up at these services. They answer different
          questions — how many engines flagged the file, whether a catalogue has it on record — so every
          service switched on is asked, and staff see the most serious answer with all of them one click
          away. A hash is not the file: nothing here ever uploads one.
        </p>
        <p>
          With none enabled, attachments are judged by this instance's own scanner alone.
        </p>
        {/* A link is not a lookup, and without this sentence an operator who
            switched VirusTotal off and still sees VirusTotal links will
            reasonably conclude the setting does not work. */}
        <p>
          A flagged attachment always links to VirusTotal, whatever these toggles say. That link opens in the
          reader's own browser and this instance sends nothing there unless VirusTotal is switched on
          below; the toggles govern what this server discloses, not where staff may look.
        </p>
      </div>

      {REPUTATION_PROVIDERS.map((p) => (
        <ReputationProviderRow
          key={p.id}
          provider={p}
          enabled={bool(p.enabledSetting)}
          setEnabled={(v) => setBool(p.enabledSetting, v)}
          keyStored={p.keySetting ? bool(`${p.keySetting}_set`) : false}
          typedKey={p.keySetting ? secret(p.keySetting) : ''}
          setTypedKey={(v) => { if (p.keySetting) setSecret(p.keySetting, v) }}
          cleared={p.keySetting ? secretCleared(p.keySetting) : false}
          setCleared={(v) => { if (p.keySetting) setSecretCleared(p.keySetting, v) }}
        />
      ))}

      <SettingRow
        label="Re-check stored verdicts"
        description="How long a clean, never-seen or unscanned verdict is trusted before it is looked up again. A detection is never re-checked — engines do not un-flag a file — and staff can ask again by hand once every seven days whatever this says."
      >
        <Select
          className="w-64 max-w-full"
          aria-label="Re-check stored verdicts"
          value={refresh}
          onChange={(e) => setStr('attachment_reputation_refresh', e.target.value)}
        >
          <option value="weekly">After 7 days</option>
          <option value="biweekly">After 14 days</option>
          <option value="monthly">After 30 days</option>
          <option value="quarterly">After 90 days</option>
          <option value="never">Never re-check automatically</option>
        </Select>
      </SettingRow>
    </Section>
  )
}
