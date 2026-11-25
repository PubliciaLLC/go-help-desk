import { createContext, useContext, useId } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getSecurityWarnings } from '@/api/admin'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// The controls and row layout every settings section is built from (#674).
// Moved out of SettingsPage.tsx unchanged. Section is the one already in
// components/admin/Section.tsx, re-exported so a section imports its parts
// from one place.
export { Section } from '@/components/admin/Section'

// The id of the label of the SettingRow a control sits in, so a switch with no
// label of its own is named by it.
const RowLabelId = createContext<string | undefined>(undefined)

export function Toggle({ checked, onChange, label, disabled }: {
  checked: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  /** The switch's own name, where the surrounding row does not supply one. */
  label?: string
}) {
  const rowLabel = useContext(RowLabelId)
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      aria-labelledby={label ? undefined : rowLabel}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center disabled:cursor-not-allowed disabled:opacity-50 rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 focus:ring-offset-background',
        checked ? 'bg-primary' : 'bg-muted-strong'
      )}
    >
      <span
        className={cn(
          'inline-block h-5 w-5 transform rounded-full bg-primary-foreground shadow-sm ring-0 transition-transform',
          checked ? 'translate-x-5' : 'translate-x-0'
        )}
      />
    </button>
  )
}

/** Where email stands, shown beside the settings that send mail. The banner
 *  stays quiet for an install that runs without email on purpose. */
export function EmailStatusNote({ on }: { on: boolean }) {
  const { data } = useQuery({ queryKey: ['security-warnings'], queryFn: getSecurityWarnings, retry: false })
  const email = data?.email
  if (!email || email.enabled) return null
  return (
    <p role="note" className="px-5 pb-4 text-sm text-warning-fg">
      Email is not set up ({email.missing.join(' and ')} not set in docker/.env)
      {on ? ": people using this aren't getting their mail." : ', so this cannot be turned on until it is.'} Set{' '}
      {email.missing.length === 1 ? 'it' : 'both'} and restart the server.
    </p>
  )
}

export function SettingRow({
  label,
  description,
  children,
}: {
  label: string
  description?: string
  children: React.ReactNode
}) {
  const labelId = useId()
  return (
    // Stacked below sm: a control that cannot shrink (a select, a number box)
    // beside its label is wider than a phone's content box (#522).
    <div className="flex flex-col gap-2 px-5 py-4 sm:flex-row sm:items-center sm:justify-between sm:gap-8">
      <div className="min-w-0 flex-1">
        <div id={labelId} className="text-sm font-medium text-fg">{label}</div>
        {description && <div className="mt-0.5 text-sm text-fg-faint">{description}</div>}
      </div>
      <RowLabelId.Provider value={labelId}>
        <div className="min-w-0 max-w-full sm:shrink-0">{children}</div>
      </RowLabelId.Provider>
    </div>
  )
}

export function SaveBar({ onSave, isPending, error, saved }: {
  onSave: () => void
  isPending: boolean
  error: string
  saved: boolean
}) {
  return (
    <div className="flex items-center gap-3 pt-2">
      <Button onClick={onSave} disabled={isPending}>
        {isPending ? 'Saving…' : 'Save changes'}
      </Button>
      {error && <p role="alert" className="text-sm text-danger-fg">{error}</p>}
      {saved && <p className="text-sm text-success-fg">Saved.</p>}
    </div>
  )
}
