import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select } from '@/components/ui/select'
import type { KpiPresetDays, KpiSearch } from '@/lib/kpi'
import type { Category, Group, Priority } from '@/api/types'

// The controls above the board: they are not part of what an image shows, so
// they are not inside it. The state is the address, so every change is a
// callback that writes it; the exception is the pair of dates, which wait for
// Apply so that a half-typed range is not sent.

const PRESETS: KpiPresetDays[] = [7, 30, 90]
const PRIORITIES: { value: Priority; label: string }[] = [
  { value: 'critical', label: 'Critical' },
  { value: 'high', label: 'High' },
  { value: 'medium', label: 'Medium' },
  { value: 'low', label: 'Low' },
]
const LABEL: Record<Priority, string> = { critical: 'Critical', high: 'High', medium: 'Medium', low: 'Low' }

// A pointer that is a finger gets 44 px targets.
const TARGET = 'pointer-coarse:min-h-11'

interface Props {
  search: KpiSearch
  onChange: (s: KpiSearch) => void
  // The dates on screen, which Custom starts from.
  shown: { from: string; to: string }
  categories: Category[]
  groups: Group[]
  // The server's refusal of the request, if it was one.
  message: string | null
}

function DateRange({ from, to, onApply }: { from: string; to: string; onApply: (from: string, to: string) => void }) {
  const [draft, setDraft] = useState({ from, to })
  return (
    <form
      role="group"
      aria-label="Custom range"
      className="flex flex-col gap-2 sm:flex-row sm:items-end"
      onSubmit={(e) => {
        e.preventDefault()
        onApply(draft.from, draft.to)
      }}
    >
      <div className="min-w-0 space-y-1">
        <Label htmlFor="reports-from">From</Label>
        <Input
          id="reports-from"
          type="date"
          value={draft.from}
          className="pointer-coarse:h-11"
          onChange={(e) => setDraft((d) => ({ ...d, from: e.target.value }))}
        />
      </div>
      <div className="min-w-0 space-y-1">
        <Label htmlFor="reports-to">To</Label>
        <Input
          id="reports-to"
          type="date"
          value={draft.to}
          className="pointer-coarse:h-11"
          onChange={(e) => setDraft((d) => ({ ...d, to: e.target.value }))}
        />
      </div>
      <Button type="submit" className={TARGET}>
        Apply
      </Button>
    </form>
  )
}

export function ReportsControls({ search, onChange, shown, categories, groups, message }: Props) {
  const range = search.range
  const custom = range.kind === 'custom'
  const several = search.priorities.length > 1

  return (
    <div className="space-y-3">
      <div role="group" aria-label="Period" className="flex flex-wrap gap-2">
        {PRESETS.map((d) => {
          const on = range.kind === 'preset' && range.days === d
          return (
            <Button
              key={d}
              type="button"
              variant={on ? 'default' : 'outline'}
              aria-pressed={on}
              className={TARGET}
              onClick={() => onChange({ ...search, range: { kind: 'preset', days: d } })}
            >
              {d} days
            </Button>
          )
        })}
        <Button
          type="button"
          variant={custom ? 'default' : 'outline'}
          aria-pressed={custom}
          className={TARGET}
          onClick={() => {
            if (!custom) onChange({ ...search, range: { kind: 'custom', from: shown.from, to: shown.to } })
          }}
        >
          Custom
        </Button>
      </div>

      {range.kind === 'custom' && (
        // Remounted when the address changes, so the inputs show what was applied.
        <DateRange
          key={`${range.from}|${range.to}`}
          from={range.from ?? ''}
          to={range.to ?? ''}
          onApply={(from, to) => onChange({ ...search, range: { kind: 'custom', from: from || null, to: to || null } })}
        />
      )}
      {message && (
        <p role="alert" className="text-sm text-red-700 wrap-anywhere">
          {message}
        </p>
      )}

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="min-w-0 space-y-1">
          <Label htmlFor="reports-category">Category</Label>
          <Select
            id="reports-category"
            className="pointer-coarse:h-11"
            value={search.categoryId ?? ''}
            onChange={(e) => onChange({ ...search, categoryId: e.target.value || null })}
          >
            <option value="">Any category</option>
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </Select>
        </div>
        <div className="min-w-0 space-y-1">
          <Label htmlFor="reports-priority">Priority</Label>
          <Select
            id="reports-priority"
            className="pointer-coarse:h-11"
            value={search.priorities.join(',')}
            onChange={(e) => {
              // A link can carry several priorities; the one entry that names
              // them all is shown for them and does nothing when chosen.
              if (e.target.value.includes(',')) return
              onChange({ ...search, priorities: e.target.value ? [e.target.value as Priority] : [] })
            }}
          >
            <option value="">Any priority</option>
            {several && <option value={search.priorities.join(',')}>{search.priorities.map((p) => LABEL[p]).join(', ')}</option>}
            {PRIORITIES.map((p) => (
              <option key={p.value} value={p.value}>
                {p.label}
              </option>
            ))}
          </Select>
        </div>
        <div className="min-w-0 space-y-1">
          <Label htmlFor="reports-group">Group</Label>
          <Select
            id="reports-group"
            className="pointer-coarse:h-11"
            value={search.groupId ?? ''}
            onChange={(e) => onChange({ ...search, groupId: e.target.value || null })}
          >
            <option value="">Any group</option>
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </Select>
        </div>
      </div>
    </div>
  )
}
