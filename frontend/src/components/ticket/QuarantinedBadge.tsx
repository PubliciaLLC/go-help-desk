// "Quarantined" (#628): a file on this ticket was identified as malicious by the
// scanner and held back. Staff scanning the queue cannot otherwise tell without
// opening each ticket. The server decides it (attachments.virus_name is set) and
// sends it to staff and administrators only, so this renders what it is given.
export function QuarantinedBadge({ hasQuarantined }: { hasQuarantined: boolean | undefined }) {
  if (!hasQuarantined) return null

  return (
    <span
      data-testid="quarantined-badge"
      title="A file attached to this ticket was identified as malicious by the scanner and quarantined."
      className="inline-flex items-center rounded-full border border-red-200 bg-red-50 px-2 py-0.5 text-xs font-medium text-red-800"
    >
      Quarantined file
    </span>
  )
}
