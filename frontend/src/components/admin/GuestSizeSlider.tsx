import { useId } from 'react'

// A guest's per-file maximum (#445): 1 to 100 MB in steps of 1, 5 by default.
// Stored as whole megabytes in guest_attachment_max_mb.
//
// The instance-wide attachment maximum is lower than the top of the range and
// wins where it applies. The slider still lets the administrator pick above
// it, because the range is their intent; it says so rather than letting a
// figure the server will not honour pass for the limit in force.

const MIN = 1
const MAX = 100
const DEFAULT_MB = 5
const TICKS = [1, 25, 50, 75, 100]

export function GuestSizeSlider({ value, onChange, instanceMaxMB, disabled }: {
  value?: number
  onChange: (mb: number) => void
  /** The instance-wide attachment maximum, in MB. */
  instanceMaxMB: number
  disabled?: boolean
}) {
  const id = useId()
  const noteId = useId()
  const mb = value !== undefined && value >= MIN && value <= MAX ? value : DEFAULT_MB
  const capped = mb > instanceMaxMB

  function set(n: number) {
    onChange(Math.min(MAX, Math.max(MIN, n)))
  }

  // A native range already moves with the arrows; this makes one megabyte per
  // press explicit instead of depending on the browser's step handling.
  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    const delta: Record<string, number> = { ArrowRight: 1, ArrowUp: 1, ArrowLeft: -1, ArrowDown: -1 }
    if (e.key in delta) {
      e.preventDefault()
      set(mb + delta[e.key])
    } else if (e.key === 'Home') {
      e.preventDefault()
      set(MIN)
    } else if (e.key === 'End') {
      e.preventDefault()
      set(MAX)
    }
  }

  return (
    <div className="w-full max-w-md space-y-1">
      <label htmlFor={id} className="block text-sm font-medium text-gray-700">
        Largest file a guest may attach
      </label>
      <input
        id={id}
        type="range"
        min={MIN}
        max={MAX}
        step={1}
        value={mb}
        disabled={disabled}
        aria-valuetext={capped ? `${mb} MB, limited to ${instanceMaxMB} MB by the instance-wide maximum` : `${mb} MB`}
        aria-describedby={capped ? noteId : undefined}
        onChange={(e) => set(Number(e.target.value))}
        onKeyDown={onKeyDown}
        className="w-full"
      />
      <div data-testid="guest-size-ticks" className="flex justify-between text-xs text-gray-500" aria-hidden="true">
        {TICKS.map((t) => <span key={t}>{t} MB</span>)}
      </div>
      <p className="text-sm text-gray-700" aria-hidden="true">{mb} MB</p>
      {capped && (
        <p
          id={noteId}
          role="note"
          aria-label="Above the instance-wide maximum"
          className="rounded border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800"
        >
          This instance accepts at most {instanceMaxMB} MB per file, so guests are held to {instanceMaxMB} MB whatever is chosen here.
        </p>
      )}
    </div>
  )
}
