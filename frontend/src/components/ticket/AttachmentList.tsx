import { useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { attachmentDownloadUrl, recheckAttachmentReputation } from '@/api/tickets'
import { apiRefusal, extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import type { Attachment, AttachmentReputation } from '@/api/types'

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

// The two services this build knows about, keyed on a lowercased value so
// both spellings the server may send resolve: the setting's identifier
// ("virustotal") and the display name it already maps that to ("VirusTotal").
const PROVIDER_NAMES: Record<string, string> = {
  virustotal: 'VirusTotal',
  metadefender: 'MetaDefender',
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
      {/* Built by the server from whichever provider the instance is
          configured for, so nothing here knows which one that is. */}
      {attachment.reputation_url && (
        <a
          href={attachment.reputation_url}
          target="_blank"
          rel="noreferrer noopener"
          className="text-blue-600 hover:underline"
        >
          Look up ↗
        </a>
      )}
    </p>
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
 * What the configured reputation service says about this file's hash, and the
 * control for asking it again.
 *
 * On the quarantined tier only, because that is the only tier the server looks
 * anything up for — an allowance spent on holiday-request PDFs is an allowance
 * that is gone when a real sample arrives. It is additive: nothing a third
 * party says moves a file out of quarantine, so the detection, the password
 * and the confirmation above are unaffected by anything here.
 */
function ReputationLine({ ticketId, attachment }: RowProps) {
  const rep = attachment.reputation
  // Absent or null means no lookup completed: no API key configured, a spent
  // budget, a provider that was down, a request that failed. Not a verdict and
  // not a clean bill of health, and the one thing it must never do is read as
  // either.
  //
  // No re-check control either: there is nothing to refresh, and the ordinary
  // lazy lookup covers this file on the next page render. A "check again" here
  // would be a second way to spend the day's allowance with none of the rules
  // on it.
  if (!rep) {
    return (
      <p className="text-xs text-gray-500">
        Reputation service: not checked — no lookup was made for this file.
      </p>
    )
  }

  // The three states that decay. `detected` is stable — engines do not un-flag
  // a file, the server refuses a re-check of one, and spending an allowance
  // re-confirming known malware is the lookup guaranteed to tell nobody
  // anything. An unrecognised state is not a verdict at all, so it gets no
  // control either.
  const refreshable = rep.state === 'clean' || rep.state === 'unseen' || rep.state === 'unscanned'

  return (
    <div className="space-y-1">
      <ReputationVerdict rep={rep} />
      {refreshable && <CheckAgain ticketId={ticketId} attachment={attachment} />}
    </div>
  )
}

/**
 * The verdict in words, attributed to whoever made the claim.
 *
 * The provider's name is the attribution: "VirusTotal has never seen this
 * file" is a claim with a source and "the reputation service has never seen
 * this file" is a claim from nowhere. The frontend does not work the name out
 * for itself — which service an instance uses is a session-gated admin setting
 * staff cannot read, which is why the server sends both the name and a
 * finished lookup URL — and a name this build does not recognise falls back to
 * the generic wording rather than printing an operator's setting at a reader.
 *
 * The four states are four different facts and none is a synonym for another.
 * "Never seen" is the one that must not slip: for a file the local scanner has
 * already flagged, being unknown to the provider is a fact worth noticing, not
 * a shrug, and certainly not reassurance.
 */
function ReputationVerdict({ rep }: { rep: AttachmentReputation }) {
  const named = providerName(rep.provider)
  // Two shapes of the same attribution: one that opens a sentence, one that
  // labels a line of numbers.
  const who = named ?? 'The reputation service'
  const label = named ?? 'Reputation service'

  // Only the two states that represent an analysis carry the date. Neither
  // provider sends one for `unseen` or `unscanned` today — there was no
  // analysis to date — and printing one there would have the line contradict
  // itself in its own second sentence.
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

    default:
      // Unreachable through the type and handled anyway. A state this
      // frontend does not recognise is not a verdict, and the safe reading of
      // one is the same as no lookup at all.
      return <p className="text-xs text-gray-500">Reputation service: not checked.</p>
  }
}

/**
 * "Check again": ask the provider about this hash now.
 *
 * Disabled rather than hidden inside the seven-day floor, because a control
 * that vanishes teaches nobody anything — the reader is left wondering whether
 * the feature exists. It says when it clears instead, which is the only form
 * of "no" a reader can act on.
 *
 * The answer replaces the row's attachment rather than invalidating the list:
 * the response IS the updated attachment, so a refetch would ask the server
 * for something it has just sent. A verdict that came back unchanged still
 * carries a new fetch time, which re-locks this control — otherwise the next
 * reader asks again for nothing.
 */
function CheckAgain({ ticketId, attachment }: RowProps) {
  const qc = useQueryClient()
  const [refusal, setRefusal] = useState<string | null>(null)

  const recheck = useMutation({
    mutationFn: () => recheckAttachmentReputation(ticketId, attachment.id),
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

  const next = nextCheckAt(attachment.reputation?.fetched_at)
  const ready = next === null || next.getTime() <= now

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
      {!ready && (
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
