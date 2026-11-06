// "Reopened" (#464): the ticket was moved from Resolved or Closed back to an
// open status, and has not been resolved again. The server keeps a count that
// is never reset (it is what limits the priority bump to the first reopen), so
// "until it is resolved again" is decided here, from the ticket's current
// status. An unknown status shows nothing rather than guessing.
export function ReopenedBadge({
  reopenCount,
  statusName,
}: {
  reopenCount: number | undefined
  statusName: string | undefined
}) {
  if (!reopenCount || reopenCount <= 0) return null
  if (statusName === undefined || statusName === 'Resolved' || statusName === 'Closed') return null

  return (
    <span
      data-testid="reopened-badge"
      title="This ticket was reopened after it had been resolved."
      className="inline-flex items-center rounded-full border border-amber-300 bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-800"
    >
      Reopened
    </span>
  )
}
