import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings } from '@/api/admin'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'

// #676: who may open the Available view (tickets with no group and no person
// assigned). Self-contained: it reads the settings dump and writes only
// available_queue_roles, so it can sit in any settings section. Administrators
// are always in; the stored value only decides Staff.

const KEY = 'available_queue_roles'

/** The stored roles. A missing or damaged value reads as admins only, which is
 *  what the server enforces too. */
function storedStaff(settings: Record<string, unknown> | undefined): boolean {
  const v = settings?.[KEY]
  return Array.isArray(v) && v.includes('staff')
}

export function AvailableQueueSection() {
  const qc = useQueryClient()
  const { data: settings, isLoading } = useQuery({
    queryKey: ['admin', 'settings'],
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
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
      qc.invalidateQueries({ queryKey: ['me'] })
    },
    onError: (err) => setError(extractError(err)),
  })

  return (
    <div className="px-5 py-4">
      <h3 className="text-sm font-medium text-gray-900">Available queue</h3>
      <p className="mt-0.5 text-sm text-gray-500">
        Who can open the Available view: tickets with no group and no person assigned.
      </p>

      <div className="mt-3 flex flex-col gap-2">
        <label className="flex items-center gap-1.5 select-none text-sm text-gray-700">
          <input
            type="checkbox"
            className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
            checked
            disabled
          />
          Administrators <span className="text-gray-400">always</span>
        </label>
        <label className="flex items-center gap-1.5 cursor-pointer select-none text-sm text-gray-700">
          <input
            type="checkbox"
            className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
            checked={staff}
            disabled={isLoading}
            onChange={(e) => setDraft(e.target.checked)}
          />
          Staff
        </label>
      </div>

      {enforced && (
        <p className="mt-3 text-sm text-gray-500">
          With Limit staff to their group scope on, staff see only the available tickets in their
          groups&apos; categories. Tick Staff if your staff pick up new tickets themselves: otherwise
          they find them only through Search.
        </p>
      )}

      {error && <p role="alert" className="mt-3 text-sm text-red-600">{error}</p>}

      <div className="mt-3">
        <Button
          type="button"
          size="sm"
          disabled={isLoading || save.isPending}
          onClick={() => {
            setError('')
            save.mutate()
          }}
        >
          Save
        </Button>
      </div>
    </div>
  )
}
