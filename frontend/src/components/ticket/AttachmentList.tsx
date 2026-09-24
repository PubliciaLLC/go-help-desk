import { useId, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { attachmentDownloadUrl, recheckAttachmentReputation } from '@/api/tickets'
import { apiRefusal, extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import type { Attachment, AttachmentProviderVerdict } from '@/api/types'

// Published on purpose, and stated wherever a quarantined file is shown. The
// password protects nothing: its only jobs are that the stored bytes are not
// double-clickable and that an on-access scanner does not eat the sample out
// from under the ticket. See the quarantine branch in handler_attachments.go.
const QUARANTINE_PASSWORD = 'infected'

// A stored Infected verdict, and nothing else, earns the loud treatment. A
// content/name mismatch is a quieter, different signal and an uninspected file
// is not a finding at all — #168: a warning that fires often is a warning
// people click past.
function isQuarantined(a: Attachment): boolean {
  return a.virus_name !== null
}

// Human words for the types the content detector actually returns. Unknown
// types fall through to the MIME string itself, which is ugly but true — an
// invented label would be worse than no label.
const CONTENT_LABELS: Record<string, string> = {
  'application/x-dosexec': 'a Windows executable',
  'application/zip': 'a ZIP archive',
  'application/pdf': 'a PDF',
  'application/octet-stream': 'unrecognised data',
  'text/html': 'HTML',
  'text/plain': 'plain text',
  'image/png': 'a PNG image',
  'image/jpeg': 'a JPEG image',
}

function contentLabel(mime: string): string {
  return CONTENT_LABELS[mime] ?? mime
}

// The services this build knows about, keyed on a lowercased value so both
// spellings the server may send resolve: the setting's identifier
// ("virustotal") and the display name it already maps that to ("VirusTotal").
const PROVIDER_NAMES: Record<string, string> = {
  virustotal: 'VirusTotal',
  metadefender: 'MetaDefender',
  // PolySwarm and CIRCL are the two that answer `known`, so leaving either out
  // would mean the one verdict staff may read as reassurance is the one
  // verdict that names nobody. CIRCL and not "CIRCL hashlookup", because the
  // name goes into a sentence attributing a claim and the organisation is what
  // is making it.
  polyswarm: 'PolySwarm',
  circl: 'CIRCL',
}

/**
 * The provider's name as a person reads it, or null when this build does not
 * recognise it.
 *
 * Null is the important half. The value reaches here from a setting an
 * operator typed, and a build that meets a provider it was released before
 * must fall back to the generic wording rather than print a raw setting value
 * at a reader — "polyswarm-v3 has never seen this file" names nothing.
 */
function providerName(provider: string | undefined): string | null {
  if (!provider) return null
  return PROVIDER_NAMES[provider.toLowerCase()] ?? null
}

/**
 * What a named feed actually asserts, as a phrase that completes "this file
 * is …".
 *
 * The verb is the point and it differs per feed, which is the whole reason
 * the state is "known" and not "known good". A vendor signing feed is an
 * Authenticode assertion — the file is signed and trusted. NSRL catalogues
 * files found in known software distributions, hacking tools included, so
 * "catalogued by NSRL" is the strongest thing that may honestly be said about
 * an NSRL hit and "signed" would be a fabrication.
 */
const KNOWN_FEED_PHRASES: Record<string, string> = {
  microsoft_windows: 'signed by Microsoft Windows',
  nsrl: 'catalogued by NSRL',
}

/**
 * One feed as a person reads it.
 *
 * A feed this build has no phrase for is shown rather than dropped, which is
 * the opposite of the rule for an unrecognised provider name above — and the
 * difference is where the value comes from. A provider is an operator-typed
 * setting, so printing an unknown one invents a service. A feed name comes
 * from the provider's own API, and it is the entire evidence behind the claim:
 * drop it and "known file" becomes the claim from nowhere this state exists
 * to avoid. The weaker verb is used for it, because an unrecognised feed has
 * not earned the stronger one.
 */
function feedPhrase(feed: string): string {
  return KNOWN_FEED_PHRASES[feedKey(feed)] ?? `catalogued by ${feed.replace(/_/g, ' ')}`
}

/**
 * A feed name reduced to the one spelling the table above is keyed on.
 *
 * Case AND separator, because the providers do not agree with each other or
 * with themselves: PolySwarm's API documentation gives the example list as
 * `['Microsoft Windows']` while the research this build was written against
 * recorded `microsoft_windows`, and neither has been seen on a live response.
 *
 * Matching only one of them fails in the damaging direction. The fallback verb
 * is deliberately the weaker "catalogued by", which is right for a feed nobody
 * recognises and wrong for a signing feed — an Authenticode assertion silently
 * downgraded to a catalogue entry is the exact information loss `known` plus
 * its feeds exists to prevent, and it is invisible, because the sentence still
 * names the feed and still reads plausibly.
 */
function feedKey(feed: string): string {
  return feed.toLowerCase().replace(/[\s-]+/g, '_')
}

/** Every feed, in the order the server sorted them. */
function feedPhrases(feeds: string[]): string {
  const phrases = feeds.map(feedPhrase)
  if (phrases.length === 1) return phrases[0]
  return `${phrases.slice(0, -1).join(', ')} and ${phrases[phrases.length - 1]}`
}

// Staff may ask the provider again once every seven days per hash, whatever
// the instance's automatic refresh interval says — including when it says
// `never`. A floor, not an override: the server enforces the same number, and
// the daily budget still applies on top of it.
const MANUAL_REFRESH_FLOOR_DAYS = 7

function formatDate(at: Date): string {
  return at.toLocaleDateString(undefined, { dateStyle: 'medium' })
}

/**
 * The earliest a verdict fetched at `fetchedAt` may be asked about again, or
 * null when there is no usable fetch time.
 *
 * Null means "no reason to hold it back": a verdict whose age nobody knows
 * gets a live control, and the server — which is the authority on the floor
 * and answers with the date it clears — refuses it if that turns out to be
 * wrong. Disabling it forever on an unknown date would be a control that can
 * never be used and a date that cannot be printed.
 */
function nextCheckAt(fetchedAt: string | null | undefined): Date | null {
  if (!fetchedAt) return null
  const at = new Date(fetchedAt)
  if (Number.isNaN(at.getTime())) return null
  return new Date(at.getTime() + MANUAL_REFRESH_FLOOR_DAYS * 24 * 60 * 60 * 1000)
}

interface RowProps {
  ticketId: string
  attachment: Attachment
}

/**
 * The persistent warning for a ticket holding quarantined attachments.
 *
 * `role="alert"` matches InsecureConfigBanner, the one existing banner in this
 * codebase. There is nothing to click that makes it go away: a warning with an
 * × is a warning nobody sees twice.
 */
export function QuarantineBanner({ attachments }: { attachments: Attachment[] }) {
  const count = attachments.filter(isQuarantined).length
  if (count === 0) return null

  return (
    <div role="alert" className="rounded-md border border-red-700 bg-red-600 px-4 py-3 text-white">
      <p className="text-sm font-semibold">
        ⚠ {count} {count === 1 ? 'attachment' : 'attachments'} on this ticket{' '}
        {count === 1 ? 'was' : 'were'} identified as malicious by the scanner and quarantined.
      </p>
      <p className="mt-1 text-sm text-red-50">
        Each is stored inside a password-protected archive and will not download without a
        confirmation. Treat them as live samples.
      </p>
    </div>
  )
}

export function AttachmentList({ ticketId, attachments }: { ticketId: string; attachments: Attachment[] }) {
  return (
    <ul className="space-y-2">
      {attachments.map((a) => (
        <li key={a.id}>
          {isQuarantined(a) ? (
            <QuarantinedRow ticketId={ticketId} attachment={a} />
          ) : (
            <OrdinaryRow ticketId={ticketId} attachment={a} />
          )}
        </li>
      ))}
    </ul>
  )
}

/**
 * Anything without a stored Infected verdict: clean, never inspected, or a
 * content/name mismatch. One click downloads it — a mismatch is not worth a
 * modal, and making it raise one is how the modal stops working for the case
 * that needs it.
 */
function OrdinaryRow({ ticketId, attachment }: RowProps) {
  return (
    <div className="space-y-1">
      <a
        href={attachmentDownloadUrl(ticketId, attachment.id)}
        className="flex items-center gap-2 text-sm text-blue-600 hover:underline truncate"
        download={attachment.filename}
      >
        <span className="shrink-0 text-gray-400">↓</span>
        <span className="truncate">{attachment.filename}</span>
      </a>
      <ContentLine attachment={attachment} />
      <HashLine attachment={attachment} />
    </div>
  )
}

/**
 * A file the scanner identified. The verdict is attributed rather than stated
 * as fact — we know what the scanner said, not what the file is, and the
 * operator who chose to keep it may well disagree.
 *
 * The download URL is deliberately not on this row. An <a href> that calls
 * preventDefault is still reachable by middle-click, ctrl-click, "Save link
 * as" and the keyboard context menu; a button that opens a dialog containing
 * the link is not.
 */
function QuarantinedRow({ ticketId, attachment }: RowProps) {
  const [confirming, setConfirming] = useState(false)

  return (
    <div className="rounded-md border border-red-300 bg-red-50 p-2">
      <div className="flex items-start gap-2">
        {/* The icon and the word, not the colour alone: colour fails a
            colourblind reader and fails completely in a screenshot. */}
        <span aria-hidden="true" className="shrink-0 font-semibold text-red-700">
          ⚠
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <p className="break-all text-sm font-medium text-red-900">{attachment.filename}</p>
          <p className="text-xs text-red-800">
            Identified as malicious by the scanner:{' '}
            <span className="font-mono font-semibold">{attachment.virus_name}</span>
          </p>
          <p className="text-xs text-red-800">
            Stored in a password-protected archive · password:{' '}
            <code className="font-mono font-semibold">{QUARANTINE_PASSWORD}</code>
          </p>
          <ContentLine attachment={attachment} />
          <HashLine attachment={attachment} />
          <ReputationLine ticketId={ticketId} attachment={attachment} />
          <Button size="sm" variant="destructive" onClick={() => setConfirming(true)}>
            Download…
          </Button>
        </div>
      </div>
      <QuarantineConfirmation
        ticketId={ticketId}
        attachment={attachment}
        open={confirming}
        onOpenChange={setConfirming}
      />
    </div>
  )
}

/**
 * What the content turned out to be. Rendered for anything that was inspected
 * and omitted entirely for anything that was not — an absence, not a "not
 * scanned" label that would read as a finding.
 */
function ContentLine({ attachment }: { attachment: Attachment }) {
  const detected = attachment.detected_mime
  if (detected === null) return null

  if (!attachment.mismatch) {
    return <p className="text-xs text-gray-500">Content: {contentLabel(detected)}</p>
  }

  // Deliberately none of the infected tier's vocabulary. Nothing found
  // anything wrong with this file; it is only named wrongly, and borrowing
  // those words would make both tiers mean less.
  //
  // A wrapped file carries a name we generated, so there is no claimed
  // extension left to contradict; an unwrapped one carries the uploader's
  // name, and naming the extension it claimed is the whole point.
  const wrapped = attachment.mime_type === 'application/zip'
  const dot = attachment.filename.lastIndexOf('.')
  const claimed = !wrapped && dot > 0 ? attachment.filename.slice(dot) : ''

  return (
    <p className="text-xs font-medium text-amber-700">
      Content looks like {contentLabel(detected)}
      {claimed && `, not a ${claimed} file`}
      {wrapped && ' — stored in an archive'}.
    </p>
  )
}

/**
 * The hash, shown for anything that was inspected — including clean files.
 * It costs nothing there and it is what makes the never-inspected case
 * legible: a hash on the row means somebody looked.
 *
 * Truncated on screen because the full 64 characters do not fit a sidebar;
 * the copy control puts the whole thing on the clipboard, which is what an
 * analyst is about to paste into a case note.
 */
function HashLine({ attachment }: { attachment: Attachment }) {
  const hash = attachment.sha256
  if (hash === null) return null

  return (
    <>
      <p className="flex flex-wrap items-center gap-1.5 text-xs text-gray-500">
        <span className="text-gray-400">SHA-256</span>
        <code className="font-mono" title={hash}>
          {hash.slice(0, 12)}…
        </code>
        <button
          type="button"
          aria-label="Copy SHA-256"
          onClick={() => void navigator.clipboard?.writeText(hash)}
          className="rounded border border-gray-300 px-1 py-0.5 text-[10px] font-medium text-gray-600 hover:bg-gray-100"
        >
          Copy
        </button>
        {/* Built by the server, and VirusTotal whatever the per-provider
            toggles say. Named here rather than left anonymous because it IS
            one service's page and a reader is entitled to know which before
            clicking. A provider's own link is a different thing and lives next
            to that provider's own verdict.
            Absent on a row this instance found nothing on — the server sends
            no URL there — and that condition is the server's to decide: the
            rule is three findings on the Go side and it will grow a fourth,
            and a copy of it here would go stale without either half looking
            wrong. The hash above stays either way. */}
        {attachment.reputation_url && (
          <a
            href={attachment.reputation_url}
            target="_blank"
            rel="noreferrer noopener"
            className="text-blue-600 hover:underline"
          >
            Look up on VirusTotal ↗
          </a>
        )}
      </p>
      {/* The sentence that stops the link looking like a bug.
          A link is not a lookup: a lookup is this server sending a customer's
          hash to a third party, which is what the toggles govern; this is an
          anchor the reader clicks themselves. Without saying so, an operator
          who deliberately switched VirusTotal off and still sees a VirusTotal
          link on every attachment will reasonably conclude the setting does
          not work.
          Deliberately avoids the words "lookup", "provider" and "reputation
          service": the sibling suites match on those to prove a verdict was
          attributed, and a line of boilerplate carrying them on every row
          would satisfy those assertions for free. */}
      {attachment.reputation_url && (
        <p className="text-[11px] text-gray-400">
          Opens in your browser. Nothing is sent from this instance unless VirusTotal is switched
          on in the admin settings.
        </p>
      )}
    </>
  )
}

/**
 * When the provider analysed the file — not when we asked it.
 *
 * Empty for a verdict that carries no date, which is most of them: a provider
 * that has never analysed a file has no date to give, and inventing one from
 * the time of our request would answer a different question than the one a
 * reader is asking.
 */
function analysedStamp(iso: string | null): string {
  if (!iso) return ''
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return ''
  return ` Last analysed by the service on ${formatDate(at)}.`
}

/**
 * What every enabled reputation service says about this file's hash: the worst
 * single answer for the row, and each service's own answer behind one click.
 *
 * On the quarantined tier only, because that is the only tier the server looks
 * anything up for — an allowance spent on holiday-request PDFs is an allowance
 * that is gone when a real sample arrives. It is additive: nothing a third
 * party says moves a file out of quarantine, so the detection, the password
 * and the confirmation above are unaffected by anything here.
 */
function ReputationLine({ ticketId, attachment }: RowProps) {
  const [expanded, setExpanded] = useState(false)
  const listId = useId()
  const rep = attachment.reputation

  // Absent or null means NOTHING WAS ATTEMPTED: no provider is enabled on this
  // instance. That is a supported configuration and a complete answer — the
  // local scanner decided this file's fate on its own — so there is nothing to
  // say and the block is absent entirely.
  //
  // Specifically not "not checked yet". That phrase describes a lookup that
  // was attempted and did not finish, which is the `unavailable` state below,
  // and printing it here would invent a failure on every instance that never
  // wanted the feature.
  if (!rep) return null

  // One provider — or a server old enough to send a verdict and no list — is
  // the row this feature started as. Expanding into a list of one would show
  // the reader the same sentence twice behind a control.
  const providers = rep.providers ?? []
  const separable = providers.length > 1

  // The three states that decay. `detected` and `known` are both final and for
  // opposite reasons — engines do not un-flag a file, and a file does not stop
  // being the signed Microsoft binary a catalogue has on record — so the
  // server refuses a re-check of either, and a control on one would be a
  // button that always fails. `unavailable` has nothing cached behind it to
  // refresh, and an unrecognised state is not a verdict at all.
  const refreshable = rep.state === 'clean' || rep.state === 'unseen' || rep.state === 'unscanned'

  const { who, label } = attribution(rep.provider_key, rep.provider)

  return (
    <div className="space-y-1">
      <ReputationVerdict verdict={rep} who={who} label={label} />

      {/* The inline control asks every enabled service at once, which is right
          for a row showing one merged answer and wrong the moment the reader
          can see them separately: it would sit beside a per-service control
          for the same service, unattributed. */}
      {refreshable && !(separable && expanded) && (
        <CheckAgain ticketId={ticketId} attachment={attachment} fetchedAt={rep.fetched_at} />
      )}

      {separable && (
        <>
          <button
            type="button"
            aria-expanded={expanded}
            aria-controls={listId}
            onClick={() => setExpanded((open) => !open)}
            className="text-xs font-medium text-blue-700 underline-offset-2 hover:underline"
          >
            {expanded ? 'Hide' : 'Show'} all {providers.length} services {expanded ? '▴' : '▾'}
          </button>
          {expanded && (
            <ul id={listId} className="space-y-2 border-l-2 border-red-200 pl-2">
              {providers.map((p) => (
                <ProviderLine
                  key={p.provider_key}
                  ticketId={ticketId}
                  attachment={attachment}
                  verdict={p}
                />
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  )
}

/**
 * One service's own statement, on its own line, in the expanded view.
 *
 * Every line is attributed, and that is the whole reason the list exists: a
 * verdict is one service's claim about one file at one time, and four of them
 * flattened into an unattributed summary throws away exactly what made the
 * second, third and fourth lookup worth making.
 *
 * Each also carries its own dates and its own re-check control, because each
 * is genuinely separate — the verdict cache is keyed by hash AND provider, so
 * every answer expires on its own clock.
 */
function ProviderLine({
  ticketId,
  attachment,
  verdict,
}: RowProps & { verdict: AttachmentProviderVerdict }) {
  const { named, who, label } = attribution(verdict.provider_key, verdict.provider)

  // Same rule as the summary, minus `unavailable`, which the server already
  // reports as not re-checkable — it is stated here as well so the control's
  // ABSENCE is this build's decision and not a flag it was handed.
  const decays =
    verdict.state === 'clean' || verdict.state === 'unseen' || verdict.state === 'unscanned'

  return (
    <li className="space-y-1">
      <ReputationVerdict verdict={verdict} who={who} label={label} />
      <p className="flex flex-wrap items-center gap-1.5 text-xs text-gray-400">
        {askedStamp(verdict.fetched_at)}
        {/* This service's own page for the hash, beside its own verdict.
            Absent for one with no per-hash page — CIRCL — because a link to a
            page that cannot answer the question the reader clicked it with is
            worse than no link. */}
        {verdict.link_url && (
          <a
            href={verdict.link_url}
            target="_blank"
            rel="noreferrer noopener"
            className="text-blue-600 hover:underline"
          >
            Open {named ?? 'the report'} ↗
          </a>
        )}
      </p>
      {decays && (
        <CheckAgain
          ticketId={ticketId}
          attachment={attachment}
          provider={verdict.provider_key}
          fetchedAt={verdict.fetched_at}
          armed={verdict.recheckable}
        />
      )}
    </li>
  )
}

/**
 * The two shapes of one attribution: one that opens a sentence, one that
 * labels a line of numbers.
 *
 * The key is preferred over the display name because it is the stable
 * identifier; the name is the fallback for a payload that carries only it. A
 * service this build does not recognise gets the generic wording rather than
 * having a string from the wire printed at a reader.
 */
function attribution(key: string | undefined, name: string | undefined) {
  const named = providerName(key) ?? providerName(name)
  return { named, who: named ?? 'The reputation service', label: named ?? 'Reputation service' }
}

/** When WE last asked this service, as against when it last analysed. */
function askedStamp(iso: string | null | undefined) {
  if (!iso) return null
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return null
  return <span>Asked on {formatDate(at)}.</span>
}

/**
 * The verdict a renderer of any one answer produces, summary or provider line.
 *
 * `state` is a plain string rather than the union so that the two payload
 * shapes share one renderer: the alternative is two copies of six states'
 * wording, which is two copies to drift apart — and the drift that matters
 * would be the row's sentence disagreeing with the line it was copied from.
 */
interface Verdict {
  state: string
  detected: number | null
  total: number | null
  threat_name: string
  analysed_at: string | null
  known_feeds?: string[] | null
}

/**
 * The verdict in words, attributed to whoever made the claim.
 *
 * The provider's name is the attribution: "VirusTotal has never seen this
 * file" is a claim with a source and "the reputation service has never seen
 * this file" is a claim from nowhere. The frontend does not work the name out
 * for itself — which services an instance uses is session-gated admin
 * configuration staff cannot read, which is why the server sends the name —
 * and one this build does not recognise falls back to the generic wording
 * rather than printing a value from the wire at a reader.
 *
 * The states are different facts and none is a synonym for another. "Never
 * seen" is the one that must not slip: for a file the local scanner has
 * already flagged, being unknown to the provider is a fact worth noticing, not
 * a shrug, and certainly not reassurance.
 */
function ReputationVerdict({
  verdict: rep,
  who,
  label,
}: {
  verdict: Verdict
  who: string
  label: string
}) {
  // Only the two states that represent an analysis carry the date. No provider
  // sends one for `unseen`, `unscanned` or `known` — there was no analysis to
  // date — and printing one there would have the line contradict itself in its
  // own second sentence.
  const analysed =
    rep.state === 'detected' || rep.state === 'clean' ? analysedStamp(rep.analysed_at) : ''

  switch (rep.state) {
    case 'detected':
    case 'clean': {
      // Both states are claims about engines, and a claim about engines
      // without the numbers is not one. "0 of 0 engines" in particular is a
      // lookup that returned nothing, wearing a clean verdict's clothes.
      const counted =
        typeof rep.detected === 'number' && typeof rep.total === 'number' && rep.total > 0
      if (!counted) {
        return (
          <p className="text-xs font-medium text-amber-700">
            {who} returned no engine counts, so there is no verdict to show.
            {analysed}
          </p>
        )
      }

      const counts = `${rep.detected} of ${rep.total} engines`
      if (rep.state === 'clean') {
        return (
          <p className="text-xs text-gray-600">
            {label}: {counts} flagged this file.{analysed}
          </p>
        )
      }
      return (
        <p className="text-xs font-medium text-red-800">
          {label}: {counts} flagged this file
          {/* The provider's consensus name, which a malware author has a hand
              in choosing. A text node, like virus_name above it. */}
          {rep.threat_name !== '' && (
            <>
              {' as '}
              <span className="break-all font-mono font-semibold">{rep.threat_name}</span>
            </>
          )}
          .{analysed}
        </p>
      )
    }

    case 'known': {
      // The one verdict in this feature that may read as reassurance, and the
      // feeds are what earn it: a named catalogue made a positive claim,
      // which is exactly what clean, unseen and unscanned have nothing of.
      //
      // Deliberately none of the clean tier's vocabulary. Nothing was scanned
      // — the answer came straight from the hash match — so engines and
      // counts here would be a fabricated analysis attached to the one line
      // staff are entitled to trust, and "engines ran and found nothing" is a
      // far weaker claim than "a catalogue has this exact file on record".
      const feeds = rep.known_feeds ?? []
      if (feeds.length > 0) {
        return (
          <p className="text-xs font-medium text-emerald-700">
            {label}: known file — {feedPhrases(feeds)}.
          </p>
        )
      }

      // Reachable: the provider answers with a catalogue hit whose entries
      // carry no feed name, and the server sends the empty list rather than
      // inventing one. The state then says somebody has the hash on file and
      // cannot say who, which is a claim from nowhere — so it loses the
      // reassuring rendering, because the source is what the reassurance was
      // resting on.
      return (
        <p className="text-xs font-medium text-amber-700">
          {who} reports this file as known but names no catalogue, so there is nothing behind the
          claim. That is not a clean result.
        </p>
      )
    }

    case 'unseen':
      return (
        <p className="text-xs font-medium text-amber-700">
          {who} has never seen this file. That is not a clean result: nobody has ever submitted it
          for analysis.
        </p>
      )

    case 'unscanned':
      return (
        <p className="text-xs font-medium text-amber-700">
          {who} knows this file but holds no verdict for it — it has not been analysed. That is not
          a clean result.
        </p>
      )

    case 'unavailable':
      // A lookup that was ATTEMPTED and did not finish: a spent allowance, a
      // service that is down, a key that was rejected. It is not a verdict and
      // it never displaces one — the server keeps it out of the severity
      // ordering, so it reaches the summary only when nothing answered at all.
      //
      // It is on the wire, and rendered, because an operator whose key has
      // been rejected has to be able to see that. Hiding it behind a sibling's
      // good answer is how a dead integration goes unnoticed for a month.
      return (
        <p className="text-xs text-gray-500">
          {label}: the lookup did not complete, so no verdict came back. It is worth checking the
          service’s key and allowance.
        </p>
      )

    default:
      // Unreachable through the type and handled anyway. A state this
      // frontend does not recognise is not a verdict, and the safe reading of
      // one is the same as a lookup that did not finish.
      return <p className="text-xs text-gray-500">{label}: not checked.</p>
  }
}

/**
 * "Check again": ask a service about this hash now.
 *
 * Disabled rather than hidden inside the seven-day floor, because a control
 * that vanishes teaches nobody anything — the reader is left wondering whether
 * the feature exists. It says when it clears instead, which is the only form
 * of "no" a reader can act on.
 *
 * `provider` names one service, which is what the expanded view's controls do:
 * each verdict has its own expiry clock, so asking all four to answer one
 * question would spend three allowances for nothing. Omitted on the row's own
 * control, where its absence means "every enabled service" — the behaviour
 * there was before there was more than one.
 *
 * `armed` is the server's own per-service answer to "would a re-check be
 * attempted", and it wins where it is given: the floor and the finality rules
 * live there, and this is deliberately not a second copy of them. Where it is
 * not given the floor is worked out from the fetch time, which is what the
 * row's merged control has to do — the summary is one service's timestamp and
 * the server does not compute a merged verdict's eligibility.
 *
 * The answer replaces the row's attachment rather than invalidating the list:
 * the response IS the updated attachment, so a refetch would ask the server
 * for something it has just sent. A verdict that came back unchanged still
 * carries a new fetch time, which re-locks this control — otherwise the next
 * reader asks again for nothing.
 */
function CheckAgain({
  ticketId,
  attachment,
  provider,
  fetchedAt,
  armed,
}: RowProps & { provider?: string; fetchedAt: string | null | undefined; armed?: boolean }) {
  const qc = useQueryClient()
  const [refusal, setRefusal] = useState<string | null>(null)

  const recheck = useMutation({
    mutationFn: () => recheckAttachmentReputation(ticketId, attachment.id, provider),
    onSuccess: (updated) => {
      setRefusal(null)
      qc.setQueryData<Attachment[]>(['attachments', ticketId], (list) =>
        (list ?? []).map((a) => (a.id === updated.id ? updated : a))
      )
    },
    onError: (err) => setRefusal(refusalMessage(err)),
  })

  // Read once, when the row mounts. A clock read during render is impure —
  // the same render would produce different output on a re-render — and a
  // control that arms itself mid-read would be surprising anyway. The page is
  // open for minutes; the floor is seven days.
  const [now] = useState(() => Date.now())

  const next = nextCheckAt(fetchedAt)
  const ready = armed ?? (next === null || next.getTime() <= now)

  return (
    <div className="space-y-1">
      <Button
        size="sm"
        variant="outline"
        disabled={!ready || recheck.isPending}
        onClick={() => recheck.mutate()}
      >
        {recheck.isPending ? 'Checking…' : 'Check again'}
      </Button>
      {/* `next` is null only for a verdict carrying no usable fetch time,
          which also leaves the control armed — so the pair below cannot both
          be false. Held shut by the server's `armed` with no date to print is
          the one case that can, and a sentence with a blank in it is worse
          than no sentence. */}
      {!ready && next && (
        <p className="text-xs text-gray-500">
          Checked within the last seven days. It can be checked again on {formatDate(next)}.
        </p>
      )}
      {refusal && (
        <p role="status" className="text-xs font-medium text-amber-700">
          {refusal}
        </p>
      )}
    </div>
  )
}

/**
 * Why the server said no, in words a reader can act on.
 *
 * Our handler always sends one, and it is better than anything that could be
 * written here — the too-soon refusal names the date it clears. The fallbacks
 * are for a refusal that arrives with no API error body at all, which is what
 * a proxy in front of the app produces: "Request failed with status code 503"
 * is not a sentence to show anybody, and the two statuses mean different
 * things — one clears by waiting, the other needs an operator.
 */
function refusalMessage(err: unknown): string {
  const { status, message } = apiRefusal(err)
  if (message) return message

  switch (status) {
    case 429:
      return 'This file was checked too recently to ask again. A file can be re-checked once every seven days.'
    case 503:
      return 'The reputation lookup could not be made: it is either not configured on this instance, or the day’s allowance is spent. It is worth trying again later.'
    default:
      return extractError(err)
  }
}

interface ConfirmationProps extends RowProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * The one place the bytes of a quarantined file are reachable from.
 *
 * It names the file, the detection and the password rather than asking a
 * generic question — one click that names the malware buys the moment of
 * attention that "Are you sure?" does not. There is no "don't ask again":
 * that option exists to make a dialog stop mattering.
 */
function QuarantineConfirmation({ ticketId, attachment, open, onOpenChange }: ConfirmationProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-[90vw] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border-2 border-red-600 bg-white p-5 shadow-lg focus:outline-none">
          <Dialog.Title className="text-base font-semibold text-red-900">
            ⚠ This file was identified as malicious
          </Dialog.Title>
          <Dialog.Description asChild>
            <div className="mt-2 space-y-2 text-sm text-gray-700">
              <p>
                <span className="break-all font-mono font-semibold">{attachment.filename}</span> was
                identified as{' '}
                <span className="font-mono font-semibold">{attachment.virus_name}</span> by the
                scanner when it was uploaded.
              </p>
              <p>
                It is stored in a password-protected archive (password{' '}
                <code className="font-mono font-semibold">{QUARANTINE_PASSWORD}</code>) so it cannot
                run by accident. Unwrapping it produces a live sample.
              </p>
              {attachment.sha256 && (
                <p className="break-all font-mono text-xs text-gray-500">
                  SHA-256 {attachment.sha256}
                </p>
              )}
            </div>
          </Dialog.Description>
          <div className="mt-5 flex justify-end gap-2">
            <Button size="sm" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <a
              href={attachmentDownloadUrl(ticketId, attachment.id)}
              className="inline-flex h-8 items-center justify-center rounded-md bg-red-600 px-3 text-xs font-medium text-white hover:bg-red-700"
              download={attachment.filename}
            >
              Download anyway
            </a>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
