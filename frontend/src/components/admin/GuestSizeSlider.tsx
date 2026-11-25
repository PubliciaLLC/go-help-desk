import { useId } from 'react'

// A guest's per-file maximum (#445, #673): 1 MB up to the per-file limit for
// every upload, 5 by default. Stored as whole megabytes in
// guest_attachment_max_mb.
//
// The per-file limit is the slider's top, so the slider never offers a figure
// the server would not honour. A stored value above it (the limit was lowered
// after the guest figure was chosen) is shown at the top with a note, because
// the guest is held to the limit until either changes and the stored figure is
// left alone.

const MIN = 1
const STORED_MAX = 100
const DEFAULT_MB = 5

export function GuestSizeSlider({ value, onChange, instanceMaxMB, disabled }: {
  value?: number
  onChange: (mb: number) => void
  /** The per-file limit for every upload, in MB: the slider's top. */
  instanceMaxMB: number
  disabled?: boolean
}) {
  const id = useId()
  const noteId = useId()
  const max = instanceMaxMB
  // The reader's rule: none, or outside 1 to 100, is 5.
  const stored = value !== undefined && value >= MIN && value <= STORED_MAX ? value : DEFAULT_MB
  const mb = Math.min(stored, max)
  const capped = stored > max

  function set(n: number) {
    onChange(Math.min(max, Math.max(MIN, n)))
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
      set(max)
    }
  }

  return (
    <div className="w-full max-w-md space-y-1">
      <label htmlFor={id} className="block text-sm font-medium text-fg-soft">
        Largest file a guest may attach
      </label>
      <input
        id={id}
        type="range"
        min={MIN}
        max={max}
        step={1}
        value={mb}
        disabled={disabled}
        aria-valuetext={capped ? `${mb} MB, saved as ${stored} MB, limited by the per-file limit` : `${mb} MB`}
        aria-describedby={capped ? noteId : undefined}
        onChange={(e) => set(Number(e.target.value))}
        onKeyDown={onKeyDown}
        className="w-full"
      />
      <div data-testid="guest-size-ticks" className="flex justify-between text-xs text-fg-faint" aria-hidden="true">
        {[MIN, max].map((t) => <span key={t}>{t} MB</span>)}
      </div>
      <p className="text-sm text-fg-soft" aria-hidden="true">{mb} MB</p>
      {capped && (
        <p
          id={noteId}
          role="note"
          className="rounded border border-warning-border bg-warning-soft px-3 py-2 text-xs text-warning-fg-strong"
        >
          Saved as {stored} MB. Every upload is limited to {max} MB, so guests are held to {max} MB until you raise that limit or lower this one.
        </p>
      )}
    </div>
  )
}
