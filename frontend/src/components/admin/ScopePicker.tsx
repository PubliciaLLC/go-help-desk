import type { ScopeInfo } from '@/api/types'

/**
 * Picks the scopes a credential carries.
 *
 * There is no "all" shortcut on purpose. A credential that should reach
 * everything selects everything, so what it can do is legible from the
 * credential itself, and a resource added later does not silently widen
 * credentials that already exist.
 *
 * Selecting write selects read with it, because the server treats write as
 * implying read and a checkbox that looks unchecked while being in force would
 * be lying about what the credential can do.
 */
export function ScopePicker({
  catalogue,
  selected,
  onChange,
  disabled,
}: {
  catalogue: ScopeInfo[]
  selected: string[]
  onChange: (next: string[]) => void
  disabled?: boolean
}) {
  const byResource = new Map<string, ScopeInfo[]>()
  for (const s of catalogue) {
    const list = byResource.get(s.resource) ?? []
    list.push(s)
    byResource.set(s.resource, list)
  }

  const has = (scope: string) => selected.includes(scope)

  function toggle(s: ScopeInfo) {
    const readScope = `${s.resource}:read`
    const writeScope = `${s.resource}:write`
    const next = new Set(selected)

    if (has(s.scope)) {
      next.delete(s.scope)
      // Unchecking read must also drop write: write implies read on the
      // server, so leaving write behind would keep read in force while the
      // box shows it off.
      if (s.action === 'read') next.delete(writeScope)
    } else {
      next.add(s.scope)
      if (s.action === 'write') next.add(readScope)
    }
    onChange([...next].sort())
  }

  const label = (resource: string) => resource.replace(/_/g, ' ')

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between">
        <p className="text-sm font-medium text-gray-700">Permissions</p>
        <p className="text-xs text-gray-500">
          {selected.length === 0
            ? 'No permissions — this credential will be refused everywhere'
            : `${selected.length} selected`}
        </p>
      </div>

      <div className="rounded border divide-y">
        {[...byResource.entries()].map(([resource, scopes]) => (
          <div key={resource} className="flex items-center justify-between gap-4 px-3 py-2">
            <span className="text-sm capitalize text-gray-800">{label(resource)}</span>
            <div className="flex gap-4">
              {scopes.map((s) => (
                <label
                  key={s.scope}
                  className="flex items-center gap-1.5 text-xs text-gray-600"
                >
                  <input
                    type="checkbox"
                    checked={has(s.scope)}
                    disabled={disabled}
                    onChange={() => toggle(s)}
                    aria-label={s.scope}
                    className="h-3.5 w-3.5 rounded border-gray-300"
                  />
                  {s.action}
                </label>
              ))}
            </div>
          </div>
        ))}
      </div>

      {selected.length === 0 && (
        <p className="text-xs text-amber-700">
          A credential with no permissions cannot do anything. Select at least one.
        </p>
      )}
    </div>
  )
}
