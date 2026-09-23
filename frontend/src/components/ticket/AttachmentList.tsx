import { useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { attachmentDownloadUrl } from '@/api/tickets'
import { Button } from '@/components/ui/button'
import type { Attachment } from '@/api/types'

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
