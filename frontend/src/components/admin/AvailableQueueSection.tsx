import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings } from '@/api/admin'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Section } from '@/components/admin/Section'

// #676, #674: who may open the Available view (tickets with no group and no person
// assigned). Self-contained: it reads the settings dump and writes only
// available_queue_roles, so it can sit in any settings section. Administrators
// are always in; the stored value only decides Staff. It is a Section of its own
// (#674: the Privacy page lists it after Ticket visibility).
//
// It keeps its own query key. The Settings page holds the same dump as a
// page-wide draft and replaces the draft whenever the dump changes, so a save
// here that refetched the page's query would discard every other unsaved edit
// on the page. The page's own save invalidates the parent key, which still
// reaches this one.

const KEY = 'available_queue_roles'
const QUERY_KEY = ['admin', 'settings', KEY]

/** The stored roles. A missing or damaged value reads as admins only, which is
 *  what the server enforces too. */
function storedStaff(settings: Record<string, unknown> | undefined): boolean {
  const v = settings?.[KEY]
  return Array.isArray(v) && v.includes('staff')
}

export function AvailableQueueSection() {
  const qc = useQueryClient()
  const { data: settings } = useQuery({
    queryKey: QUERY_KEY,
    queryFn: getSettings,
  })
  // null: the box shows what is stored. A value: the operator has changed it
  // and not yet saved.
  const [draft, setDraft] = useState<boolean | null>(null)
  const [error, setError] = useState('')

  const staff = draft ?? storedStaff(settings)
  const enforced = settings?.ticket_scope_enforced === true

  const save = useMutation({
    mutationFn: () => updateSettings({ [KEY]: staff ? ['admin', 'staff'] : ['admin'] }),
    onSuccess: () => {
      setDraft(null)
      setError('')
      qc.invalidateQueries({ queryKey: QUERY_KEY, exact: true })
      qc.invalidateQueries({ queryKey: ['me'] })
    },
    onError: (err) => setError(extractError(err)),
  })

  return (
    <Section title="Available queue">
      <div className="px-5 py-4">
        <p className="text-sm text-fg-faint">
          Who can open the Available view: tickets with no group and no person assigned.
        </p>

        <div className="mt-3 flex flex-col gap-2">
          <label className="flex items-center gap-1.5 select-none text-sm text-fg-soft">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-border-strong text-primary focus:ring-ring"
              checked
              disabled
            />
            Administrators <span className="text-fg-faint">always</span>
          </label>
          <label className="flex items-center gap-1.5 cursor-pointer select-none text-sm text-fg-soft">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-border-strong text-primary focus:ring-ring"
              checked={staff}
              disabled={!settings}
              onChange={(e) => setDraft(e.target.checked)}
            />
            Staff
          </label>
        </div>

        {enforced && (
          <p className="mt-3 text-sm text-fg-faint">
            With Limit staff to their group scope on, staff see only the available tickets in their
            groups&apos; categories. Tick Staff if your staff pick up new tickets themselves: otherwise
            they find them only through Search.
          </p>
        )}

        {error && <p role="alert" className="mt-3 text-sm text-danger-fg">{error}</p>}

        <div className="mt-3">
          <Button
            type="button"
            size="sm"
            disabled={!settings || save.isPending}
            onClick={() => {
              setError('')
              save.mutate()
            }}
          >
            Save
          </Button>
        </div>
      </div>
    </Section>
  )
}
