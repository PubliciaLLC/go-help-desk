import { useEffect, useId, useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronDownIcon, XIcon } from 'lucide-react'
import { lookupRequesters } from '@/api/tickets'
import type { Priority, SourceChannel, Status } from '@/api/types'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { MIN_QUERY, queryLength } from './requester'
import { CHANNELS, PRIORITIES, SLA_STATES, type TicketSearch } from '@/lib/ticketSearch'
import { useFilterLookups } from '@/lib/useFilterLookups'
import { useRequesterById } from '@/lib/useRequesterById'

// The filter form. One component for both layouts: the panel under the toolbar
// on a desktop (every change applies at once) and the full-screen dialog on a
// phone (changes wait for Show results). It holds no state of its own about
// the search: the caller gives it a value and takes back patches, so the panel
// edits the address and the dialog edits a draft with the same code.

const CHANNEL_LABEL: Record<SourceChannel, string> = {
  web: 'Web',
  guest: 'Email / guest',
  phone: 'Phone',
  api: 'API',
  mcp: 'MCP',
}
const SLA_LABEL = { breached: 'Breached', at_risk: 'At risk' }
const cap = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

type Layout = 'panel' | 'dialog'

function toggle<T>(list: T[] | undefined, v: T): T[] | undefined {
  const cur = list ?? []
  const next = cur.includes(v) ? cur.filter(x => x !== v) : [...cur, v]
  return next.length > 0 ? next : undefined
}

function Group({ legend, layout, row, children }: { legend: string; layout: Layout; row?: boolean; children: ReactNode }) {
  return (
    <fieldset className="min-w-0">
      <legend className="mb-1 text-xs font-semibold text-gray-700">{legend}</legend>
      <div className={layout === 'dialog' ? 'grid grid-cols-2 gap-x-3' : row ? 'flex flex-wrap gap-x-5' : 'flex flex-col'}>{children}</div>
    </fieldset>
  )
}

function Check({
  label,
  checked,
  onChange,
  layout,
}: {
  label: string
  checked: boolean
  onChange: () => void
  layout: Layout
}) {
  return (
    <label
      className={
        'flex cursor-pointer items-center gap-2 text-sm text-gray-700 ' +
        (layout === 'dialog' ? 'min-h-11' : 'min-h-7 pointer-coarse:min-h-11')
      }
    >
      <input
        type="checkbox"
        className={layout === 'dialog' ? 'h-5 w-5 shrink-0 rounded border-gray-300' : 'h-4 w-4 shrink-0 rounded border-gray-300'}
        checked={checked}
        onChange={onChange}
      />
      <span className="min-w-0 wrap-anywhere">{label}</span>
    </label>
  )
}

function Field({ label, htmlFor, children }: { label: string; htmlFor: string; children: ReactNode }) {
  return (
    <div className="min-w-0 space-y-1">
      <label htmlFor={htmlFor} className="block text-xs font-semibold text-gray-700">
        {label}
      </label>
      {children}
    </div>
  )
}

// Selects and date boxes are 44 px tall in the dialog: that is the whole
// point of it being a separate layout.
const control = (layout: Layout) => (layout === 'dialog' ? 'h-11' : 'pointer-coarse:h-11')

// The requester: nobody, one account, or text matched against names and
// addresses (which also finds guests, who have no account to pick).
function RequesterField({
  reporter,
  requester,
  layout,
  onChange,
}: {
  reporter?: string
  requester?: string
  layout: Layout
  onChange: (patch: Partial<TicketSearch>) => void
}) {
  const id = useId()
  const [text, setText] = useState('')
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const t = setTimeout(() => setDebounced(text.trim()), 300)
    return () => clearTimeout(t)
  }, [text])

  const searching = queryLength(debounced) >= MIN_QUERY
  const { data: hits = [], isFetching } = useQuery({
    queryKey: ['requester-lookup', debounced],
    queryFn: () => lookupRequesters(debounced),
    enabled: searching && !reporter && !requester,
  })
  const picked = useRequesterById(reporter)
  const pickedQuery = reporter ? picked : null

  const clear = () => {
    setText('')
    setDebounced('')
    onChange({ reporter: undefined, requester: undefined })
  }
  const takeText = () => {
    const t = text.trim()
    if (queryLength(t) < MIN_QUERY) return
    onChange({ requester: t, reporter: undefined })
    setText('')
  }

  if (reporter || requester) {
    const who =
      reporter && pickedQuery
        ? pickedQuery.isPending
          ? { name: '…', sub: '' }
          : pickedQuery.isError || pickedQuery.data === null
            ? { name: 'Unknown account', sub: '' }
            : { name: pickedQuery.data.display_name, sub: pickedQuery.data.email }
        : { name: `Requester contains “${requester}”`, sub: '' }
    return (
      <div className="space-y-1">
        <span className="block text-xs font-semibold text-gray-700">Requester</span>
        <div className="flex items-center gap-2 rounded-md border border-input bg-background px-3 py-1">
          <div className="min-w-0 flex-1 text-sm leading-tight text-gray-900">
            <div className="wrap-anywhere">{who.name}</div>
            {who.sub && <div className="wrap-anywhere text-xs text-gray-500">{who.sub}</div>}
          </div>
          <button
            type="button"
            aria-label="Clear requester"
            onClick={clear}
            className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded text-gray-500 hover:bg-gray-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 pointer-coarse:h-11 pointer-coarse:w-11"
          >
            <XIcon aria-hidden="true" className="h-4 w-4" />
          </button>
        </div>
      </div>
    )
  }

  const textOk = queryLength(text.trim()) >= MIN_QUERY
  return (
    <div className="space-y-1">
      <label htmlFor={id} className="block text-xs font-semibold text-gray-700">
        Requester
      </label>
      <Input
        id={id}
        type="search"
        className={control(layout)}
        value={text}
        maxLength={254}
        autoComplete="off"
        placeholder="Name or email, at least 2 characters"
        onChange={e => setText(e.target.value)}
        onKeyDown={e => {
          // Enter takes the free-text option, which is always available once
          // there is enough to search for.
          if (e.key === 'Enter') {
            e.preventDefault()
            takeText()
          }
        }}
      />
      {textOk && (
        <ul aria-label="Requester suggestions" className="divide-y divide-gray-100 rounded-md border border-gray-200 bg-white">
          {searching &&
            hits.map(h => (
              <li key={h.id}>
                <button
                  type="button"
                  className="flex min-h-11 w-full flex-col justify-center px-3 py-1.5 text-left text-sm hover:bg-gray-50 focus-visible:bg-gray-50 focus-visible:outline-none"
                  onClick={() => {
                    setText('')
                    onChange({ reporter: h.id, requester: undefined })
                  }}
                >
                  <span className="font-medium text-gray-900 wrap-anywhere">{h.display_name}</span>
                  <span className="text-xs text-gray-500 wrap-anywhere">{h.email}</span>
                </button>
              </li>
            ))}
          <li>
            <button
              type="button"
              className="min-h-11 w-full px-3 py-1.5 text-left text-sm text-blue-700 hover:bg-gray-50 focus-visible:bg-gray-50 focus-visible:outline-none"
              onClick={takeText}
            >
              <span className="wrap-anywhere">Any requester matching “{text.trim()}”</span>
            </button>
          </li>
        </ul>
      )}
      <p role="status" className="text-xs text-gray-500">
        {textOk && searching && !isFetching && hits.length === 0
          ? 'No account matches. Use the last option to match guests and addresses too.'
          : 'Pick a person, or match part of a name or address. Guests are found too.'}
      </p>
    </div>
  )
}

export function TicketFilters({
  value,
  onChange,
  staff,
  slaOn,
  statuses,
  layout,
}: {
  value: TicketSearch
  onChange: (patch: Partial<TicketSearch>) => void
  staff: boolean
  // Offered only while SLA tracking is on; the server answers 400 otherwise.
  slaOn: boolean
  statuses: Status[]
  layout: Layout
}) {
  const uid = useId()
  const { categories, types, items, staff: people, groups } = useFilterLookups(value, { staff, wantPeople: true })
  // The dialog keeps the rarely used fields behind a button; the panel has room.
  const [more, setMore] = useState(false)
  const dialog = layout === 'dialog'
  const showMore = !dialog || more

  const dateRange = (label: string, from: 'created_from' | 'updated_from', to: 'created_to' | 'updated_to') => {
    const bad = value[from] && value[to] && value[from]! > value[to]!
    return (
      <div className="min-w-0 space-y-1">
        <span className="block text-xs font-semibold text-gray-700">{label}</span>
        <div className={dialog ? 'space-y-2' : 'flex items-center gap-2'}>
          <Input
            type="date"
            aria-label={`${label} from`}
            aria-invalid={bad || undefined}
            className={control(layout)}
            value={value[from] ?? ''}
            onChange={e => onChange({ [from]: e.target.value || undefined })}
          />
          {!dialog && <span className="text-sm text-gray-500">to</span>}
          <Input
            type="date"
            aria-label={`${label} to`}
            aria-invalid={bad || undefined}
            className={control(layout)}
            value={value[to] ?? ''}
            onChange={e => onChange({ [to]: e.target.value || undefined })}
          />
        </div>
        {bad && (
          <p role="alert" className="text-xs text-red-600">
            The end date is before the start date.
          </p>
        )}
      </div>
    )
  }

  const selectOf = (
    label: string,
    any: string,
    current: string | undefined,
    options: { id: string; name: string }[],
    set: (v: string | undefined) => void,
    disabled = false,
  ) => {
    const id = `${uid}-${label}`
    return (
      <Field label={label} htmlFor={id}>
        <Select
          id={id}
          className={control(layout)}
          value={current ?? ''}
          disabled={disabled}
          onChange={e => set(e.target.value || undefined)}
        >
          <option value="">{any}</option>
          {options.map(o => (
            <option key={o.id} value={o.id}>
              {o.name}
            </option>
          ))}
        </Select>
      </Field>
    )
  }

  const fields = {
    status: (
      <Group legend="Status" layout={layout}>
        {statuses.map(s => (
          <Check
            key={s.id}
            layout={layout}
            label={s.name}
            checked={value.status?.includes(s.id) ?? false}
            onChange={() => onChange({ status: toggle(value.status, s.id) })}
          />
        ))}
      </Group>
    ),
    priority: (
      <Group legend="Priority" layout={layout}>
        {PRIORITIES.map((p: Priority) => (
          <Check
            key={p}
            layout={layout}
            label={cap(p)}
            checked={value.priority?.includes(p) ?? false}
            onChange={() => onChange({ priority: toggle(value.priority, p) })}
          />
        ))}
      </Group>
    ),
    requester: staff && (
      <RequesterField reporter={value.reporter} requester={value.requester} layout={layout} onChange={onChange} />
    ),
    category: selectOf('Category', 'Any category', value.category, categories, v =>
      onChange({ category: v, type: undefined, item: undefined }),
    ),
    type: selectOf(
      'Type',
      'Any type',
      value.type,
      types,
      v => onChange({ type: v, item: undefined }),
      !value.category,
    ),
    item: selectOf('Item', 'Any item', value.item, items, v => onChange({ item: v }), !value.type),
    assignee:
      staff &&
      selectOf(
        'Assignee',
        'Anyone',
        value.assignee,
        people.map(p => ({ id: p.id, name: p.display_name })),
        v => onChange({ assignee: v }),
      ),
    group:
      staff &&
      selectOf('Group', 'Any group', value.group, groups, v => onChange({ group: v })),
    sla:
      staff &&
      slaOn && (
        <Field label="SLA" htmlFor={`${uid}-sla`}>
          <Select
            id={`${uid}-sla`}
            className={control(layout)}
            value={value.sla ?? ''}
            onChange={e => onChange({ sla: (e.target.value || undefined) as TicketSearch['sla'] })}
          >
            <option value="">Any</option>
            {SLA_STATES.map(s => (
              <option key={s} value={s}>
                {SLA_LABEL[s]}
              </option>
            ))}
          </Select>
        </Field>
      ),
    created: dateRange('Created', 'created_from', 'created_to'),
    updated: staff && dateRange('Last updated', 'updated_from', 'updated_to'),
    channel: staff && (
      <Group legend="Came in by" layout={layout} row>
        {CHANNELS.map(c => (
          <Check
            key={c}
            layout={layout}
            label={CHANNEL_LABEL[c]}
            checked={value.channel?.includes(c) ?? false}
            onChange={() => onChange({ channel: toggle(value.channel, c) })}
          />
        ))}
      </Group>
    ),
  }

  if (dialog) {
    return (
      <div className="space-y-5">
        {fields.status}
        {fields.priority}
        {fields.requester}
        {fields.category}
        {fields.type}
        {fields.created}
        <button
          type="button"
          aria-expanded={more}
          onClick={() => setMore(v => !v)}
          className="flex min-h-11 w-full items-center justify-between gap-3 rounded-md text-left text-sm text-blue-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
        >
          <span>{staff ? 'More filters: item, assignee, group, last updated, came in by' + (slaOn ? ', SLA' : '') : 'More filters: item'}</span>
          <ChevronDownIcon aria-hidden="true" className={'h-4 w-4 shrink-0 transition-transform ' + (more ? 'rotate-180' : '')} />
        </button>
        {showMore && (
          <div className="space-y-5">
            {fields.item}
            {fields.assignee}
            {fields.group}
            {fields.updated}
            {fields.channel}
            {fields.sla}
          </div>
        )}
      </div>
    )
  }

  // The desktop panel, in the order of the approved mock-up.
  return (
    <div className="grid grid-cols-2 gap-x-6 gap-y-5 lg:grid-cols-4">
      {fields.status}
      {fields.priority}
      <div className="col-span-2 grid grid-cols-1 gap-y-4 lg:grid-cols-2 lg:gap-x-6">
        {fields.requester}
        {staff && <div className="space-y-4">{fields.assignee}{fields.group}</div>}
      </div>
      {fields.category}
      {fields.type}
      {fields.item}
      {fields.sla}
      <div className="col-span-2">{fields.created}</div>
      {fields.updated && <div className="col-span-2">{fields.updated}</div>}
      {fields.channel && <div className="col-span-2 lg:col-span-4">{fields.channel}</div>}
    </div>
  )
}
