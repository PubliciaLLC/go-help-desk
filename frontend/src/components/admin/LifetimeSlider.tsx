import { useId } from 'react'

// The longest a new API key may live (#450): six stops, 1 to 5 years and Never.
// The setting stores the number of years, or -1 for Never.

const STOPS = [1, 2, 3, 4, 5, -1] as const
const NEVER = -1
const DEFAULT_YEARS = 2

function stopLabel(years: number) {
  if (years === NEVER) return 'Never'
  return years === 1 ? '1 year' : `${years} years`
}

function valueText(years: number) {
  return years === NEVER ? 'No limit' : stopLabel(years)
}

export function LifetimeSlider({ value, onChange }: { value?: number; onChange: (years: number) => void }) {
  const id = useId()
  const years = value !== undefined && (STOPS as readonly number[]).includes(value) ? value : DEFAULT_YEARS
  const stop = STOPS.indexOf(years as (typeof STOPS)[number]) + 1 // 1..6

  function setStop(n: number) {
    onChange(STOPS[Math.min(STOPS.length, Math.max(1, n)) - 1])
  }

  // A native range moves with the arrows already; this makes it explicit, so
  // exactly one stop per press does not depend on the browser's step handling.
  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    const delta: Record<string, number> = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }
    if (e.key in delta) {
      e.preventDefault()
      setStop(stop + delta[e.key])
    } else if (e.key === 'Home') {
      e.preventDefault()
      setStop(1)
    } else if (e.key === 'End') {
      e.preventDefault()
      setStop(STOPS.length)
    }
  }

  return (
    <div className="w-full max-w-md space-y-1">
      <label htmlFor={id} className="block text-sm font-medium text-gray-700">
        Longest an API key may live
      </label>
      <input
        id={id}
        type="range"
        min={1}
        max={STOPS.length}
        step={1}
        value={stop}
        aria-valuetext={valueText(years)}
        onChange={(e) => setStop(Number(e.target.value))}
        onKeyDown={onKeyDown}
        className="w-full"
      />
      <div data-testid="lifetime-ticks" className="flex justify-between text-xs text-gray-500" aria-hidden="true">
        {STOPS.map((y) => (
          <span key={y} className={y === years ? 'font-semibold text-gray-900' : ''}>{stopLabel(y)}</span>
        ))}
      </div>
      {(years === NEVER || years > DEFAULT_YEARS) && (
        <p role="alert" aria-label="Keys may live longer than 2 years" className="rounded border border-red-200 bg-red-50 px-3 py-2 text-xs font-medium text-red-700">
          {years === NEVER
            ? 'Warning: with no limit, an API key can be made that never expires. A leaked key then works until someone notices and revokes it.'
            : `Warning: keys can live up to ${years} years. The default is 2. A leaked key keeps working until it expires or is revoked.`}
        </p>
      )}
    </div>
  )
}
