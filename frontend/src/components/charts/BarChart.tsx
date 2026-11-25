import { TONE_CLASS } from './tones'
import type { ChartTone } from './tones'

export interface BarRow {
  key: string // stable id, React key
  label: string // full label; truncated on screen, whole in title and the table
  value: number
}

export interface BarChartProps {
  rows: BarRow[]
  ariaLabel: string // what the chart shows
  tone?: ChartTone // default blue
  formatValue?: (n: number) => string // default String
  emptyText?: string // shown instead of the bars when there are no rows or every value is 0
}

const ROW = 28 // CSS px a row is tall, in the HTML columns and in the svg's user units
const BAR = 18
const WIDTH = 100

// Labels and values are HTML so they wrap, truncate and scale like the rest of
// the page; the bars are one svg between them, stretched to the column. Row i
// of the svg is the 28 px beside label i, at any width. No text in the svg.
export function BarChart({ rows, ariaLabel, tone = 'blue', formatValue = String, emptyText }: BarChartProps) {
  const max = Math.max(0, ...rows.map((r) => r.value))
  if (max === 0) {
    return (
      <figure>
        <div className="flex items-center justify-center py-6 text-sm text-fg-faint">{emptyText}</div>
      </figure>
    )
  }

  const height = rows.length * ROW
  return (
    // relative + overflow-hidden: a table keeps its content's width whatever
    // sr-only says, so a 120-character label would otherwise make the hidden
    // table, positioned absolutely, widen the document.
    <figure className="relative overflow-hidden">
      <div className="grid grid-cols-[minmax(0,40%)_minmax(0,1fr)_auto] gap-x-2">
        <div>
          {rows.map((r) => (
            <div key={r.key} title={r.label} className="h-7 truncate text-sm leading-7 text-fg-soft">
              {r.label}
            </div>
          ))}
        </div>
        <svg
          role="img"
          aria-label={ariaLabel}
          viewBox={`0 0 ${WIDTH} ${height}`}
          preserveAspectRatio="none"
          width="100%"
          style={{ height }}
          className={`block ${TONE_CLASS[tone]}`}
        >
          {rows.map((r, i) => (
            <rect
              key={r.key}
              x={0}
              y={i * ROW + (ROW - BAR) / 2}
              width={(r.value / max) * WIDTH}
              height={BAR}
              fill="currentColor"
            />
          ))}
        </svg>
        <div className="text-right">
          {rows.map((r) => (
            <div key={r.key} className="h-7 text-sm leading-7 tabular-nums text-fg-soft">
              {formatValue(r.value)}
            </div>
          ))}
        </div>
      </div>
      <table className="sr-only">
        <caption>{ariaLabel}</caption>
        <thead>
          <tr>
            <th scope="col">Label</th>
            <th scope="col">Value</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key}>
              <th scope="row">{r.label}</th>
              <td>{formatValue(r.value)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  )
}
