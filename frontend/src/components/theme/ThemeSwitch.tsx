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
  const labelledElsewhere = useThemeStore((s) => s.labelledSwitches > 0)
  const trackLabelled = useThemeStore((s) => s.trackLabelled)
  // Two switches are on screen at once (the desktop sidebar and the drawer), so
  // each needs its own radio group name or the browser would treat them as one.
  const name = useId()
  const labelled = variant === 'labelled'

  // The labelled switch (Your account) announces errors; the one in the sidebar
  // then stays quiet so a screen reader does not hear the same sentence twice.
  useEffect(() => {
    if (!labelled) return
    trackLabelled(1)
    return () => trackLabelled(-1)
  }, [labelled, trackLabelled])

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
      <fieldset
        // The icon form has no room for a line of text, so a failed save shows as a
        // tooltip and a red frame (and is read out, below).
        title={!labelled && error ? error : undefined}
        className={cn(
          'm-0 min-w-0 border-0 p-0',
          labelled ? 'flex flex-wrap gap-2' : 'inline-flex rounded-md border bg-card p-px',
          !labelled && (error ? 'border-danger-border' : 'border-border'),
        )}
      >
        <legend className="sr-only">Appearance</legend>
        {OPTIONS.map(({ value, label, Icon }) => (
          <label
            key={value}
            title={label}
            className={cn(
              'flex cursor-pointer items-center justify-center rounded transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring',
              labelled
                ? 'gap-2 rounded-md border border-border-strong px-3 py-2 text-sm has-[:checked]:border-ring has-[:checked]:bg-selected has-[:checked]:text-selected-fg'
                : // The checked option is told apart by colour and a border, not by a fill
                  // alone (a gray on white is 1.1:1, under the 3:1 WCAG asks of a state
                  // indicator). 32px wide on a coarse pointer: three of them and Sign out
                  // fit the sidebar's row; the height stays 44px.
                  'h-7 w-7 border border-transparent pointer-coarse:h-11 pointer-coarse:w-8 has-[:checked]:border-ring has-[:checked]:bg-selected has-[:checked]:text-selected-fg',
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
      {/* Always rendered, so a screen reader hears the text when it appears. */}
      <span
        aria-live={labelled || !labelledElsewhere ? 'polite' : undefined}
        className={cn(
          (!message || !labelled) && 'sr-only',
          message && error && labelled && 'mt-2 text-sm text-danger-fg',
          message && !error && 'mt-2 text-sm text-success-fg',
        )}
      >
        {message}
      </span>
    </div>
  )
}
