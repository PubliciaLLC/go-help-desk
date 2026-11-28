import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'

import { renderWithQuery } from '@/test/render'
import { AttachmentList, QuarantineBanner } from './AttachmentList'
import type { Attachment } from '@/api/types'

// The page-level tests in src/pages cover what a reader sees on a ticket.
// These cover the one decision this component makes before any of that: which
// of the two rows an attachment gets, and what the banner above them counts.
//
// Worth testing here rather than through the page because the distinction is
// deliberately narrow. A stored scanner verdict, and nothing else, earns the
// loud treatment — a content/name mismatch is a quieter, different signal, and
// an uninspected file is not a finding at all. Widen it and the red banner
// starts appearing on tickets where nothing is wrong, which is how a warning
// stops being read. Narrow it and a live sample downloads in one click.

function attachment(over: Partial<Attachment> = {}): Attachment {
  return {
    id: 'att-1',
    ticket_id: 'tk-1',
    filename: 'report.pdf',
    mime_type: 'application/pdf',
    size_bytes: 1024,
    created_at: '2026-09-01T10:00:00Z',
    detected_mime: 'application/pdf',
    sha256: 'a'.repeat(64),
    virus_name: null,
    mismatch: null,
    reputation_url: null,
    reputation: null,
    ...over,
  }
}

/** A file the scanner named. The only thing that earns the loud treatment. */
function infected(over: Partial<Attachment> = {}): Attachment {
  return attachment({
    id: 'att-bad',
    filename: 'invoice.exe.zip',
    mime_type: 'application/zip',
    detected_mime: 'application/vnd.microsoft.portable-executable',
    virus_name: 'EICAR-Test-File',
    ...over,
  })
}

/** Content that contradicts the name. A finding, but not this one. */
function mismatched(over: Partial<Attachment> = {}): Attachment {
  return attachment({
    id: 'att-odd',
    filename: 'report.pdf',
    detected_mime: 'image/png',
    mismatch: true,
    ...over,
  })
}

describe('the quarantine banner', () => {
  it('says nothing when the scanner named nothing', () => {
    renderWithQuery(
      <QuarantineBanner attachments={[attachment(), mismatched(), attachment({ id: 'att-2', sha256: null, detected_mime: null })]} />,
    )
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('counts only the files the scanner named, and agrees with itself about one', () => {
    renderWithQuery(<QuarantineBanner attachments={[attachment(), mismatched(), infected()]} />)

    const alert = screen.getByRole('alert')
    // "1 attachment ... was", not "1 attachments ... were". The count comes
    // from the data and the verb has to follow it.
    expect(alert.textContent).toMatch(/1 attachment\b/)
    expect(alert.textContent).toMatch(/\bwas identified as malicious\b/)
    expect(alert.textContent).not.toMatch(/attachments/)
  })

  it('agrees with itself about several', () => {
    renderWithQuery(
      <QuarantineBanner
        attachments={[infected(), infected({ id: 'att-bad-2', filename: 'setup.exe.zip' }), mismatched()]}
      />,
    )

    const alert = screen.getByRole('alert')
    expect(alert.textContent).toMatch(/2 attachments\b/)
    expect(alert.textContent).toMatch(/\bwere identified as malicious\b/)
  })
})

describe('which row an attachment gets', () => {
  it('puts a file the scanner named behind a confirmation, with no link on the row', () => {
    renderWithQuery(<AttachmentList ticketId="tk-1" attachments={[infected()]} />)

    const row = screen.getByText('invoice.exe.zip').closest('div')
    expect(row).not.toBeNull()
    expect(screen.getByRole('button', { name: /download/i })).toBeDefined()

    // Deliberately not an <a href> that calls preventDefault: that is still
    // reachable by middle-click, ctrl-click, "Save link as" and the keyboard
    // context menu. There must be no link to the file on this row at all.
    expect(screen.queryByRole('link', { name: /invoice\.exe\.zip/ })).toBeNull()

    // The archive password is on the row, because a reader who cannot open
    // the sample cannot triage it, and the password protects nothing.
    expect(screen.getByText('infected')).toBeDefined()
  })

  it('leaves a mismatch as an ordinary one-click download', () => {
    renderWithQuery(<AttachmentList ticketId="tk-1" attachments={[mismatched()]} />)

    const link = screen.getByRole('link', { name: /report\.pdf/ })
    expect(link.getAttribute('download')).toBe('report.pdf')
    expect(screen.queryByRole('button', { name: /download/i })).toBeNull()

    // Said, not shouted. The content is still named on the row.
    expect(screen.getByText(/PNG/i)).toBeDefined()
  })

  it('keeps the two apart in one list', () => {
    renderWithQuery(
      <AttachmentList ticketId="tk-1" attachments={[attachment(), infected(), mismatched({ id: 'att-odd' })]} />,
    )

    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(within(rows[0]).getByRole('link', { name: /report\.pdf/ })).toBeDefined()
    expect(within(rows[1]).getByRole('button', { name: /download/i })).toBeDefined()
    expect(within(rows[2]).getByRole('link', { name: /report\.pdf/ })).toBeDefined()
  })
})
