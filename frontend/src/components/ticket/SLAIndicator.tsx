import type { SLAColor, SLATargetStatus, TicketSLA } from '@/api/types'
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
    </>
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
