import { ChartLegend } from './ChartLegend'
import { areaPath, linePath, niceCeiling, pointsFor } from './geometry'
import { TONE_CLASS } from './tones'
import type { ChartTone } from './tones'

export interface ChartSeries {
  key: string // stable id, React key and the data table's column
  label: string // legend and table header
  values: number[] // one per label; must be labels.length
  tone: ChartTone
}

export interface AreaChartProps {
  labels: string[] // x categories, oldest first (ISO dates for a day series)
  series: ChartSeries[] // drawn in order, the first underneath
  ariaLabel: string // what the chart shows
  height?: number // CSS px, default 160; the width is the container's
  formatLabel?: (label: string) => string // x labels and the table's first column; default identity
  formatValue?: (value: number) => string // axis maximum, legend totals and table cells; default String
  emptyText?: string // shown instead of the marks when there are no labels or every value is 0
}

const WIDTH = 100

// Inline SVG, no text inside it: the svg stretches to its container, so every
// label sits in HTML around it. Values are also in a visually hidden table.
export function AreaChart({
  labels,
  series,
  ariaLabel,
  height = 160,
  formatLabel = (l) => l,
  formatValue = String,
  emptyText,
}: AreaChartProps) {
  for (const s of series) {
    if (s.values.length !== labels.length) {
      throw new Error(`AreaChart: series "${s.key}" has ${s.values.length} values for ${labels.length} labels`)
    }
  }

  const legend = series.map((s) => ({
    label: s.label,
    tone: s.tone,
    value: formatValue(s.values.reduce((sum, v) => sum + v, 0)),
  }))

  const empty = labels.length === 0 || series.every((s) => s.values.every((v) => v === 0))
  if (empty) {
    return (
      <figure>
        <div className="flex items-center justify-center text-sm text-fg-faint" style={{ height }}>
          {emptyText}
        </div>
        <figcaption className="mt-2">
          <ChartLegend items={legend} />
        </figcaption>
      </figure>
    )
  }

  const max = niceCeiling(Math.max(...series.flatMap((s) => s.values)))
  const first = labels[0]
  const last = labels[labels.length - 1]

  return (
    <figure>
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2">
        <div className="text-right text-xs tabular-nums text-fg-faint" style={{ height }}>
          {formatValue(max)}
        </div>
        <svg
          role="img"
          aria-label={ariaLabel}
          viewBox={`0 0 ${WIDTH} ${height}`}
          preserveAspectRatio="none"
          width="100%"
          style={{ height }}
          className="block overflow-visible"
        >
          {series.map((s) => {
            const pts = pointsFor(s.values, WIDTH, height, max)
            return (
              <g key={s.key} className={TONE_CLASS[s.tone]}>
                <path d={areaPath(pts, height)} fill="currentColor" fillOpacity={0.15} />
                <path
                  d={linePath(pts)}
                  fill="none"
                  stroke="currentColor"
                  strokeWidth={2}
                  vectorEffect="non-scaling-stroke"
                />
              </g>
            )
          })}
          <line
            x1={0}
            x2={WIDTH}
            y1={height}
            y2={height}
            stroke="currentColor"
            strokeWidth={1}
            vectorEffect="non-scaling-stroke"
            className={TONE_CLASS.gray}
          />
        </svg>
        <span aria-hidden="true" />
        <div className="mt-1 flex justify-between text-xs text-fg-faint">
          <span>{formatLabel(first)}</span>
          <span>{formatLabel(last)}</span>
        </div>
      </div>
      <table className="sr-only">
        <thead>
          <tr>
            <th scope="col">{''}</th>
            {series.map((s) => (
              <th key={s.key} scope="col">
                {s.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {labels.map((label, i) => (
            <tr key={label}>
              <th scope="row">{formatLabel(label)}</th>
              {series.map((s) => (
                <td key={s.key}>{formatValue(s.values[i])}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <figcaption className="mt-2">
        <ChartLegend items={legend} />
      </figcaption>
    </figure>
  )
}
