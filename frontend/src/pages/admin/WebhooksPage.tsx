import { useId, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listWebhooks, createWebhook, updateWebhook, deleteWebhook } from '@/api/admin'
import { extractError, extractErrorCode } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Spinner } from '@/components/ui/spinner'
import { PlusIcon, PencilIcon, Trash2Icon } from 'lucide-react'
import type { WebhookConfig, WebhookPayloadFormat, WebhookDelivery } from '@/api/types'

// What the server accepts, in the order it lists them (notify.WebhookEvents).
// "*" is sent for "all events"; the server refuses an empty list.
const EVENTS = [
  'ticket.created',
  'ticket.assigned',
  'ticket.status_changed',
  'ticket.replied',
  'ticket.resolved',
  'ticket.closed',
  'ticket.reopened',
  'ticket.linked',
] as const

// notify.Formats, in the same order. The values are what the API stores; the
// labels are what an operator recognises.
const FORMATS: { value: WebhookPayloadFormat; label: string }[] = [
  { value: 'raw', label: 'Raw JSON' },
  { value: 'slack', label: 'Slack' },
  { value: 'teams', label: 'Microsoft Teams' },
  { value: 'discord', label: 'Discord' },
  { value: 'jira', label: 'Jira Automation' },
]

const formatLabel = (f: string) => FORMATS.find((x) => x.value === f)?.label ?? f

const FAILURE_LABEL: Record<string, string> = {
  timeout: 'timed out', dns: 'host not found', tls: 'TLS error',
  blocked_address: 'blocked address', connection: 'connection failed', other: 'error',
}

function LastDelivery({ d }: { d: WebhookDelivery | null | undefined }) {
  if (d == null) return <span className="text-xs text-gray-400">No deliveries yet</span>
  const ok = d.error === ''
  const what = ok || d.error === 'http_status' ? String(d.status) : (FAILURE_LABEL[d.error] ?? 'error')
  return (
    <div className="space-y-0.5">
      <span className={ok
        ? 'rounded bg-green-50 px-1.5 py-0.5 text-xs text-green-700'
        : 'rounded bg-red-50 px-1.5 py-0.5 text-xs text-red-700'}>
        {ok ? 'Delivered' : 'Failing'} · {what}
      </span>
      <time dateTime={d.at} className="block text-xs text-gray-500">
        {new Date(d.at).toLocaleString()}
      </time>
    </div>
  )
}

// ── Form state ────────────────────────────────────────────────────────────────

// "All events" is its own flag rather than a "*" entry in the set, so there is
// no state in which "*" and a specific event are both selected.
interface FormState {
  url: string
  secret: string
  format: WebhookPayloadFormat
  all: boolean
  events: string[]
}

const emptyForm: FormState = { url: '', secret: '', format: 'raw', all: false, events: [] }

function fromHook(h: WebhookConfig): FormState {
  // An empty list is how a subscription from before events were required is
  // stored, and delivery treats it as "everything". Show it as that, so
  // saving it does not send the empty list the server now refuses.
  const all = h.events.length === 0 || h.events.includes('*')
  return {
    url: h.url,
    secret: '',
    format: h.payload_format ?? 'raw',
    all,
    events: all ? [] : h.events,
  }
}

function eventsOf(f: FormState): string[] {
  return f.all ? ['*'] : EVENTS.filter((e) => f.events.includes(e))
}

const canSubmit = (f: FormState) => f.url.trim() !== '' && eventsOf(f).length > 0

// A blank secret means "none given" on create and "leave it alone" on edit —
// the API never sends the stored one back, so there is nothing to prefill and
// nothing to compare against.
function toInput(f: FormState) {
  return {
    url: f.url.trim(),
    payload_format: f.format,
    events: eventsOf(f),
    ...(f.secret !== '' ? { secret: f.secret } : {}),
  }
}

// The one refusal an operator is likely to hit: a hook pointed at something on
// their own network. Said plainly rather than as a generic failure.
function describe(err: unknown): string {
  const message = extractError(err)
  if (extractErrorCode(err) === 'invalid_url') {
    return `The URL was refused: ${message}. Webhook targets must be public http or https addresses; private, loopback and link-local addresses are blocked.`
  }
  return message
}

function WebhookFields({
  form,
  onChange,
  secretHint,
}: {
  form: FormState
  onChange: (f: FormState) => void
  secretHint: string
}) {
  const id = useId()

  function toggle(event: string, on: boolean) {
    onChange({
      ...form,
      events: on ? [...form.events, event] : form.events.filter((e) => e !== event),
    })
  }

  return (
    <div className="space-y-4">
      <div className="space-y-1">
        <Label htmlFor={`${id}-url`} className="text-xs">Webhook URL</Label>
        <Input
          id={`${id}-url`}
          type="url"
          placeholder="https://hooks.example.com/services/…"
          value={form.url}
          onChange={(e) => onChange({ ...form, url: e.target.value })}
        />
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor={`${id}-format`} className="text-xs">Payload format</Label>
          <Select
            id={`${id}-format`}
            value={form.format}
            onChange={(e) => onChange({ ...form, format: e.target.value as WebhookPayloadFormat })}
          >
            {FORMATS.map((f) => (
              <option key={f.value} value={f.value}>{f.label}</option>
            ))}
          </Select>
          <p className="text-xs text-gray-500">
            Raw sends the full event. The others reshape it for that service&apos;s incoming
            webhook.
          </p>
        </div>
        <div className="space-y-1">
          <Label htmlFor={`${id}-secret`} className="text-xs">Secret</Label>
          <Input
            id={`${id}-secret`}
            type="password"
            autoComplete="new-password"
            value={form.secret}
            onChange={(e) => onChange({ ...form, secret: e.target.value })}
          />
          <p className="text-xs text-gray-500">{secretHint}</p>
        </div>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-xs font-medium text-gray-700">Events</legend>
        <div className="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2 lg:grid-cols-3">
          <label className="flex min-h-[24px] items-center gap-2 text-sm font-medium text-gray-900">
            <input
              type="checkbox"
              checked={form.all}
              onChange={(e) => onChange({ ...form, all: e.target.checked })}
            />
            <span>All events</span>
          </label>
          {EVENTS.map((e) => (
            <label key={e} className="flex min-h-[24px] items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
                checked={form.all || form.events.includes(e)}
                disabled={form.all}
                onChange={(ev) => toggle(e, ev.target.checked)}
              />
              <span className="font-mono text-xs">{e}</span>
            </label>
          ))}
        </div>
      </fieldset>
    </div>
  )
}

// ── Edit row ──────────────────────────────────────────────────────────────────

function EditRow({ hook, onClose }: { hook: WebhookConfig; onClose: () => void }) {
  const qc = useQueryClient()
  const [form, setForm] = useState<FormState>(() => fromHook(hook))
  const [error, setError] = useState('')

  const save = useMutation({
    mutationFn: () => updateWebhook(hook.id, toInput(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'webhooks'] })
      onClose()
    },
    onError: (err) => setError(describe(err)),
  })

  return (
    <tr>
      <td colSpan={6} className="bg-gray-50 px-4 py-4">
        <form
          aria-label="Edit webhook"
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (canSubmit(form)) save.mutate()
          }}
        >
          <WebhookFields
            form={form}
            onChange={(f) => {
              setForm(f)
              setError('')
            }}
            secretHint="Leave empty to keep the current secret. Typing here replaces it."
          />
          {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
          <div className="flex gap-2">
            <Button type="submit" size="sm" disabled={!canSubmit(form) || save.isPending}>
              {save.isPending ? 'Saving…' : 'Save'}
            </Button>
            <Button type="button" size="sm" variant="outline" onClick={onClose}>
              Cancel
            </Button>
          </div>
        </form>
      </td>
    </tr>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function WebhooksPage() {
  const qc = useQueryClient()
  const [form, setForm] = useState<FormState>(emptyForm)
  const [createError, setCreateError] = useState('')
  const [rowError, setRowError] = useState('')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<WebhookConfig | null>(null)

  const { data: hooks = [], isLoading } = useQuery({
    queryKey: ['admin', 'webhooks'],
    queryFn: listWebhooks,
  })

  const refresh = () => qc.invalidateQueries({ queryKey: ['admin', 'webhooks'] })

  const createMutation = useMutation({
    mutationFn: () => createWebhook(toInput(form)),
    onSuccess: () => {
      // Everything but the secret stays so a second, similar hook is quick to
      // add; the secret is not left sitting in a field after it has been sent.
      setForm({ ...form, secret: '' })
      setCreateError('')
      refresh()
    },
    onError: (err) => setCreateError(describe(err)),
  })

  const toggleMutation = useMutation({
    mutationFn: (h: WebhookConfig) => updateWebhook(h.id, { enabled: !h.enabled }),
    onSuccess: () => {
      setRowError('')
      refresh()
    },
    onError: (err) => setRowError(describe(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteWebhook(id),
    onSuccess: () => {
      setRowError('')
      refresh()
      setPendingDelete(null)
    },
    onError: (err) => {
      setPendingDelete(null)
      setRowError(describe(err))
    },
  })

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Webhooks</h1>
          <p className="mt-1 text-sm text-gray-500">
            Send ticket events to another system as an HTTP POST. Targets must be public
            addresses. When a secret is set, each delivery is signed with it in the{' '}
            <code className="font-mono text-xs">X-GHD-Signature</code> header. A secret is never
            shown again once saved. The list shows each hook&apos;s most recent delivery. Failed deliveries are not retried.
          </p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle className="text-sm">New webhook</CardTitle>
          </CardHeader>
          <CardContent>
            <form
              aria-label="New webhook"
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault()
                if (canSubmit(form)) createMutation.mutate()
              }}
            >
              <WebhookFields
                form={form}
                onChange={(f) => {
                  setForm(f)
                  setCreateError('')
                }}
                secretHint="Optional. Used to sign deliveries; it cannot be read back afterwards."
              />
              {createError && <p role="alert" className="text-sm text-red-600">{createError}</p>}
              <Button type="submit" size="sm" disabled={!canSubmit(form) || createMutation.isPending}>
                <PlusIcon className="mr-2 h-4 w-4" />
                {createMutation.isPending ? 'Creating…' : 'Create'}
              </Button>
            </form>
          </CardContent>
        </Card>

        {rowError && <p role="alert" className="text-sm text-red-600">{rowError}</p>}

        {isLoading ? (
          <div className="flex justify-center py-12"><Spinner /></div>
        ) : (
          <div className="overflow-x-auto rounded-lg border bg-white">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-xs uppercase text-gray-500">
                <tr>
                  <th className="px-4 py-3 text-left">URL</th>
                  <th className="px-4 py-3 text-left">Format</th>
                  <th className="px-4 py-3 text-left">Events</th>
                  <th className="px-4 py-3 text-left">Status</th>
                  <th className="px-4 py-3 text-left">Last delivery</th>
                  <th className="px-4 py-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {hooks.map((h) =>
                  editingId === h.id ? (
                    <EditRow key={h.id} hook={h} onClose={() => setEditingId(null)} />
                  ) : (
                    <tr key={h.id}>
                      <td className="px-4 py-3 font-mono text-xs text-gray-900 break-all">{h.url}</td>
                      <td className="px-4 py-3 text-gray-700">{formatLabel(h.payload_format)}</td>
                      <td className="px-4 py-3">
                        <div className="flex flex-wrap gap-1">
                          {h.events.length === 0 || h.events.includes('*') ? (
                            <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700">
                              All events
                            </span>
                          ) : (
                            h.events.map((e) => (
                              <span
                                key={e}
                                className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-700"
                              >
                                {e}
                              </span>
                            ))
                          )}
                        </div>
                      </td>
                      <td className="px-4 py-3">
                        <span
                          className={
                            h.enabled
                              ? 'rounded bg-green-50 px-1.5 py-0.5 text-xs text-green-700'
                              : 'rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-500'
                          }
                        >
                          {h.enabled ? 'Enabled' : 'Disabled'}
                        </span>
                      </td>
                      <td className="px-4 py-3"><LastDelivery d={h.last_delivery} /></td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex justify-end gap-2">
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => toggleMutation.mutate(h)}
                            disabled={toggleMutation.isPending}
                          >
                            {h.enabled ? 'Disable' : 'Enable'}
                          </Button>
                          <Button size="sm" variant="outline" onClick={() => setEditingId(h.id)}>
                            <PencilIcon className="mr-1 h-3.5 w-3.5" />
                            Edit
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            className="border-red-200 text-red-600 hover:bg-red-50"
                            onClick={() => setPendingDelete(h)}
                          >
                            <Trash2Icon className="mr-1 h-3.5 w-3.5" />
                            Delete
                          </Button>
                        </div>
                      </td>
                    </tr>
                  )
                )}
                {hooks.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-8 text-center text-gray-400">
                      No webhooks yet.
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => { if (!open) setPendingDelete(null) }}
        title="Delete this webhook?"
        description={
          <>
            Deliveries to <span className="break-all font-mono text-xs">{pendingDelete?.url}</span>{' '}
            stop immediately. This cannot be undone.
          </>
        }
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </Layout>
  )
}
