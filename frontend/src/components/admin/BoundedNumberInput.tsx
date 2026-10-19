import { useId, useState } from 'react'

// A whole number inside a fixed range (#445: the guest upload cap and rate).
//
// What is typed is kept as typed until the field loses focus, so clearing it to
// type "12" does not first snap to the minimum and turn the "12" into "112".
// The value reported upward is always inside the range: a number typed past
// the top is held at the top, which is also what the server would accept.
export function BoundedNumberInput({ label, hint, value, min, max, onChange, disabled }: {
  label: string
  hint: string
  value: number
  min: number
  max: number
  onChange: (n: number) => void
  disabled?: boolean
}) {
  const id = useId()
  const hintId = useId()
  const [draft, setDraft] = useState<string | null>(null)
  const clamp = (n: number) => Math.min(max, Math.max(min, n))

  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-sm font-medium text-gray-700">{label}</label>
      <input
        id={id}
        type="number"
        inputMode="numeric"
        min={min}
        max={max}
        step={1}
        value={draft ?? String(value)}
        disabled={disabled}
        aria-describedby={hintId}
        onChange={(e) => {
          setDraft(e.target.value)
          const n = parseInt(e.target.value, 10)
          if (Number.isFinite(n)) onChange(clamp(n))
        }}
        onBlur={() => setDraft(null)}
        className="w-24 rounded-md border border-gray-300 px-2 py-1 text-right text-sm disabled:cursor-not-allowed disabled:opacity-50"
      />
      <p id={hintId} className="text-xs text-gray-500">{hint}</p>
    </div>
  )
}
