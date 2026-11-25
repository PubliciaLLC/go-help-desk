import { useEffect, useId, useState } from 'react'
import { MonitorIcon, MoonIcon, SunIcon } from 'lucide-react'
import type { ThemePreference } from '@/api/types'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/store/theme'

// The appearance switch (#709): Light, Dark, Match my device. A native radio
// group, so there is no key handling of our own: Tab reaches the group once, the
// arrow keys move between and select the options, Space selects. Used in the
// sidebar footer and the pre-sign-in corner (`icons`) and in Your account
// (`labelled`). It reads and writes the theme store, so every instance agrees.

const OPTIONS: { value: ThemePreference; label: string; Icon: typeof SunIcon }[] = [
  { value: 'light', label: 'Light', Icon: SunIcon },
  { value: 'dark', label: 'Dark', Icon: MoonIcon },
  { value: 'device', label: 'Match my device', Icon: MonitorIcon },
]

const SAVED_FOR_MS = 3000

export function ThemeSwitch({ variant, className }: { variant: 'icons' | 'labelled'; className?: string }) {
  const preference = useThemeStore((s) => s.preference)
  const error = useThemeStore((s) => s.error)
  const saveCount = useThemeStore((s) => s.saveCount)
  const setPreference = useThemeStore((s) => s.setPreference)
  // Two switches are on screen at once (the desktop sidebar and the drawer), so
  // each needs its own radio group name or the browser would treat them as one.
  const name = useId()
  const labelled = variant === 'labelled'

  // "Saved." for a moment after a save lands, in the labelled form. Raised while
  // rendering, when the store's count moves past the one this switch last saw
  // (the supported way to derive state from a changing value), and cleared by a
  // timer.
  const [seen, setSeen] = useState(saveCount)
  const [saved, setSaved] = useState(false)
  if (seen !== saveCount) {
    setSeen(saveCount)
    setSaved(true)
  }
  useEffect(() => {
    if (!saved) return
    const t = setTimeout(() => setSaved(false), SAVED_FOR_MS)
    return () => clearTimeout(t)
  }, [saved, seen])

  const message = error ?? (labelled && saved ? 'Saved.' : '')

  return (
    <div className={cn('flex flex-col', labelled ? 'items-start' : 'items-end', className)}>
      <fieldset className={cn('m-0 min-w-0 border-0 p-0', labelled ? 'flex flex-wrap gap-2' : 'inline-flex rounded-md border border-border bg-card p-0.5')}>
        <legend className="sr-only">Appearance</legend>
        {OPTIONS.map(({ value, label, Icon }) => (
          <label
            key={value}
            title={label}
            className={cn(
              'flex cursor-pointer items-center justify-center rounded transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring',
              labelled
                ? 'gap-2 rounded-md border border-border-strong px-3 py-2 text-sm has-[:checked]:border-ring has-[:checked]:bg-selected has-[:checked]:text-selected-fg'
                : 'h-7 w-7 pointer-coarse:h-11 pointer-coarse:w-11 has-[:checked]:bg-muted has-[:checked]:text-fg has-[:checked]:shadow-sm',
              !labelled && 'text-fg-faint hover:text-fg',
              labelled && 'text-fg-soft',
            )}
          >
            <input
              type="radio"
              name={name}
              value={value}
              checked={preference === value}
              onChange={() => void setPreference(value)}
              className="sr-only"
            />
            <Icon className="h-4 w-4" aria-hidden="true" />
            <span className={labelled ? undefined : 'sr-only'}>{label}</span>
          </label>
        ))}
      </fieldset>
      {/* Always rendered, so a screen reader hears the text when it appears.
          role="status" is a polite live region. It is written that way, and not
          as aria-live="polite", so the sidebar's copy is not the one a page's
          own test for its live region finds first. */}
      <span
        role="status"
        className={cn(
          !message && 'sr-only',
          message && error && (labelled ? 'mt-2 text-sm text-danger-fg' : 'mt-1 max-w-48 text-right text-[10px] leading-tight text-danger-fg'),
          message && !error && 'mt-2 text-sm text-success-fg',
        )}
      >
        {message}
      </span>
    </div>
  )
}
