import { useId, useRef, useState } from 'react'
import { ImageDownIcon } from 'lucide-react'
import { BarChart, VolumeTrend } from '@/components/charts'
import { Button } from '@/components/ui/button'
import { Spinner } from '@/components/ui/spinner'
import { IMAGE_FAILED, useImageExport } from './exportElement'
import { KPI_CARD_TITLES, kpiFileName, periodRange, type ComparisonText, type KpiCardKey, type KpiCardModel, type KpiFigure } from '@/lib/kpi'
import type { KpiResponse } from '@/api/types'

// One card of the Reports board, drawn from its model (lib/kpi.ts). The model
// is the data; this is the markup, and the markup is what the image export
// reads back.

const TONE_CLASS = { good: 'text-green-700', bad: 'text-red-700', neutral: 'text-gray-500' } as const

// Volume and Workload take the whole row from md up; the others share it.
const WIDE: KpiCardKey[] = ['volume', 'workload']
const CARD_CLASS = 'min-w-0 space-y-3 rounded-lg border bg-card p-4 shadow-sm'
const spanClass = (key: KpiCardKey) => (WIDE.includes(key) ? 'md:col-span-2 xl:col-span-3' : '')

// The box a card's spinner sits in, about the card's own height, so the page
// does not jump when the numbers arrive.
const LOADING_HEIGHT: Record<KpiCardKey, string> = {
  time_to_resolve: 'min-h-[7rem]',
  first_response: 'min-h-[7rem]',
  sla: 'min-h-[9rem]',
  reopen: 'min-h-[7rem]',
  volume: 'min-h-[32rem]',
  backlog: 'min-h-[13rem]',
  workload: 'min-h-[24rem]',
}

// The top ten rows of a Workload list; the rest are behind Show all.
const WORKLOAD_ROWS = 10

function Comparison({ c }: { c: ComparisonText }) {
  const cls = `text-sm ${TONE_CLASS[c.tone]}`
  if (c.srText === c.text) return <p className={cls}>{c.text}</p>
  return (
    <p className={cls}>
      <span aria-hidden="true">{c.text}</span>
      <span className="sr-only">{c.srText}</span>
    </p>
  )
}

function Figure({ f }: { f: KpiFigure }) {
  return (
    <div className="min-w-0">
      <p className="text-xs font-medium uppercase tracking-wide text-gray-500">{f.label}</p>
      <p className="text-2xl font-bold text-gray-900">{f.value}</p>
      {f.comparison && <Comparison c={f.comparison} />}
    </div>
  )
}

// The card while the numbers load.
export function ReportCardLoading({ cardKey }: { cardKey: KpiCardKey }) {
  const titleId = useId()
  return (
    <section aria-labelledby={titleId} aria-busy="true" className={`${CARD_CLASS} ${spanClass(cardKey)}`}>
      <h2 id={titleId} className="text-base font-semibold text-gray-900">
        {KPI_CARD_TITLES[cardKey]}
      </h2>
      <div className={`flex items-center justify-center ${LOADING_HEIGHT[cardKey]}`}>
        <Spinner />
      </div>
    </section>
  )
}

interface ReportCardProps {
  model: KpiCardModel
  resp: KpiResponse
  // What the image's strip says under the card (period, filters, when made).
  contextLines: () => string[]
}

export function ReportCard({ model, resp, contextLines }: ReportCardProps) {
  const titleId = useId()
  const root = useRef<HTMLElement>(null)
  const image = useImageExport()
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  return (
    <section ref={root} aria-labelledby={titleId} className={`${CARD_CLASS} ${spanClass(model.key)}`}>
      <div className="flex items-start justify-between gap-2">
        <h2 id={titleId} className="min-w-0 text-base font-semibold text-gray-900">
          {model.title}
        </h2>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          data-export="skip"
          aria-label={`Download ${model.title} as an image`}
          aria-busy={image.busy ? 'true' : undefined}
          disabled={image.busy}
          className="h-8 w-8 shrink-0 pointer-coarse:h-11 pointer-coarse:w-11"
          onClick={() => void image.run(root.current, contextLines(), kpiFileName(resp, 'png', model.key))}
        >
          <ImageDownIcon className="h-4 w-4" aria-hidden="true" />
        </Button>
      </div>
      {image.failed && (
        <p data-export="skip" className="text-sm text-red-700">
          {IMAGE_FAILED}
        </p>
      )}

      {model.emptyText ? (
        <p className="py-4 text-sm text-gray-500">{model.emptyText}</p>
      ) : (
        model.figures.length > 0 && (
          <div className="flex flex-wrap gap-x-8 gap-y-3">
            {model.figures.map((f) => (
              <Figure key={f.label} f={f} />
            ))}
          </div>
        )
      )}
      {model.lines.map((l) => (
        <p key={l} className="text-sm text-gray-500">
          {l}
        </p>
      ))}

      {model.days && (
        <VolumeTrend
          volume={{ tz: resp.tz, days: model.days }}
          ariaLabel={`Tickets created and resolved per day, ${periodRange(resp)}`}
          emptyText="No tickets were created or resolved in this period."
        />
      )}
      {model.charts.length > 0 && (
        <div className={model.key === 'volume' || model.key === 'workload' ? 'grid gap-6 md:grid-cols-2' : 'space-y-4'}>
          {model.charts.map((c) => {
            const id = `${model.key}.${c.key}`
            const limited = model.key === 'workload' && !expanded[id] && c.rows.length > WORKLOAD_ROWS
            return (
              <div key={c.key} className="min-w-0 space-y-2">
                <h3 className="text-sm font-medium text-gray-700">{c.title}</h3>
                <BarChart
                  rows={limited ? c.rows.slice(0, WORKLOAD_ROWS) : c.rows}
                  ariaLabel={`${model.title} ${c.title.toLowerCase()}`}
                  emptyText="Nothing to show."
                />
                {model.key === 'workload' && c.rows.length > WORKLOAD_ROWS && (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    data-export="skip"
                    className="pointer-coarse:min-h-11"
                    onClick={() => setExpanded((e) => ({ ...e, [id]: !e[id] }))}
                  >
                    {limited ? `Show all (${c.rows.length})` : `Show top ${WORKLOAD_ROWS}`}
                  </Button>
                )}
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}
