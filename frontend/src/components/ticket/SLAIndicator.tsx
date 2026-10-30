import { useEffect, useRef, useState } from 'react'

import type { SLAColor, SLAEarlierRecord, SLATargetStatus, TicketSLA } from '@/api/types'
import { slaTargetText } from '@/lib/format'

// Renders the response and resolution targets of a ticket's live SLA status.
// sla == null (no matching policy, or SLA tracking off for this instance) is
// the only thing this component checks for `sla` itself — nothing else here
// re-derives that decision, the same way nothing re-derives the color.
//
// Two shapes, same data: `compact` for the queue column (two small dots,
// nothing else competing for space in the row) and the default for the
// ticket header (two outline pills, matching the status chip's shape so it
// reads as one family with it rather than a fourth competing style).
export function SLAIndicator({
  sla,
  compact,
}: {
  sla: TicketSLA | null | undefined
  compact?: boolean
}) {
  if (sla == null) return null

  return (
    <>
      <SLATarget label="Response" target={sla.response} compact={compact} />
      <SLATarget label="Resolution" target={sla.resolution} compact={compact} />
      {!compact && sla.earlier != null && <SLARestarted earlier={sla.earlier} />}
    </>
  )
}

// "SLA restarted" (#443): the SLA clock really restarted, which only a reopen
// does (a staff priority change keeps the clock and never shows this). The
// badge opens a small panel with the earlier record's facts; the record itself
// stays on the server. Escape closes the panel and gives focus back to the
// badge.
function SLARestarted({ earlier }: { earlier: SLAEarlierRecord }) {
  const [open, setOpen] = useState(false)
  const badge = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      setOpen(false)
      badge.current?.focus()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [open])

  return (
    <span className="relative inline-block">
      <button
        ref={badge}
        type="button"
        data-testid="sla-restarted"
        aria-expanded={open}
        title="The SLA clock restarted when this ticket was reopened. The earlier record is kept; select to see it."
        onClick={() => setOpen((o) => !o)}
        className="inline-flex items-center rounded-full border border-gray-300 px-2 py-0.5 text-xs font-medium text-gray-700 underline decoration-dotted underline-offset-2 hover:bg-gray-50"
      >
        SLA restarted
      </button>
      {open && (
        <dl
          data-testid="sla-earlier"
          className="absolute left-0 z-10 mt-1 w-64 space-y-1 rounded-md border border-gray-200 bg-white p-3 text-xs text-gray-700 shadow-md"
        >
          <div>
            <dt className="font-medium text-gray-900">Earlier SLA record</dt>
            <dd>{earlier.policy_name}</dd>
          </div>
          <div>
            <dt className="text-gray-500">Started</dt>
            <dd>{new Date(earlier.started_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-gray-500">Replaced</dt>
            <dd>{new Date(earlier.superseded_at).toLocaleString()}</dd>
          </div>
          <div>
            <dt className="text-gray-500">Outcome</dt>
            <dd>{earlier.outcome === 'breached' ? 'Breached' : 'Met'}</dd>
          </div>
        </dl>
      )}
    </span>
  )
}

const DOT_COLOR: Record<SLAColor, string> = {
  green: 'bg-green-500',
  amber: 'bg-amber-500',
  red: 'bg-red-500',
}

const PILL_COLOR: Record<SLAColor, string> = {
  green: 'border-green-300 text-green-700',
  amber: 'border-amber-300 text-amber-700',
  red: 'border-red-300 text-red-700',
}

function SLATarget({
  label,
  target,
  compact,
}: {
  label: 'Response' | 'Resolution'
  target: SLATargetStatus
  compact?: boolean
}) {
  const met = target.met_at != null
  const text = slaTargetText(target)
  const title = `${label} SLA: ${text}`

  if (compact) {
    return (
      <span
        data-target={label.toLowerCase()}
        data-color={target.color}
        data-met={met}
        title={title}
        aria-label={title}
        className={`inline-block h-2.5 w-2.5 rounded-full ${DOT_COLOR[target.color]} ${met ? 'opacity-60' : ''}`}
      />
    )
  }

  return (
    <span
      data-target={label.toLowerCase()}
      data-color={target.color}
      data-met={met}
      title={title}
      aria-label={title}
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium ${PILL_COLOR[target.color]}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${DOT_COLOR[target.color]}`} />
      {label} · {text}
    </span>
  )
}
