import type { ChartTone } from './tones'
import { TONE_CLASS } from './tones'

export interface ChartLegendItem {
  label: string
  tone: ChartTone
  value?: string
}

export function ChartLegend({ items }: { items: ChartLegendItem[] }) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-gray-700">
      {items.map((item) => (
        <li key={item.label} className="flex items-center gap-1.5">
          <span aria-hidden="true" className={`inline-block size-2.5 rounded-sm bg-current ${TONE_CLASS[item.tone]}`} />
          <span>{item.label}</span>
          {item.value !== undefined && <span className="font-medium tabular-nums">{item.value}</span>}
        </li>
      ))}
    </ul>
  )
}
