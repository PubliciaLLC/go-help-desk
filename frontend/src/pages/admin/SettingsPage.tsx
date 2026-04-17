import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings, getSAMLConfig, saveSAMLConfig, getSiteConfig, uploadLogo, deleteLogo, listStatuses, listCategories, listSLAPolicies, createSLAPolicy, updateSLAPolicy, deleteSLAPolicy } from '@/api/admin'
import type { SLAPolicy } from '@/api/types'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'
import { useState, useEffect, useRef } from 'react'

// ── Shared primitives ─────────────────────────────────────────────────────────

function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative inline-flex h-6 w-11 shrink-0 cursor-pointer items-center rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-offset-2',
        checked ? 'bg-blue-600' : 'bg-gray-200'
      )}
    >
      <span
        className={cn(
          'inline-block h-5 w-5 transform rounded-full bg-white shadow-sm ring-0 transition-transform',
          checked ? 'translate-x-5' : 'translate-x-0'
        )}
      />
    </button>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h2 className="mb-3 text-xs font-semibold uppercase tracking-wider text-gray-400">{title}</h2>
      <div className="divide-y rounded-lg border bg-white">{children}</div>
    </div>
  )
}

function SettingRow({
  label,
  description,
  children,
}: {
  label: string
  description?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-8 px-5 py-4">
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium text-gray-900">{label}</div>
        {description && <div className="mt-0.5 text-sm text-gray-500">{description}</div>}
      </div>
      <div className="shrink-0">{children}</div>
    </div>
  )
}

function SaveBar({ onSave, isPending, error, saved }: {
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
      {error && <p className="text-sm text-red-600">{error}</p>}
      {saved && <p className="text-sm text-green-600">Saved.</p>}
    </div>
  )
}

// ── SAML section ──────────────────────────────────────────────────────────────

function SAMLSection() {
  const qc = useQueryClient()
  const certFileRef = useRef<HTMLInputElement>(null)
  const keyFileRef = useRef<HTMLInputElement>(null)

  const [metadataURL, setMetadataURL] = useState('')
  const [certPEM, setCertPEM] = useState('')
  const [keyPEM, setKeyPEM] = useState('')
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)
  const [warning, setWarning] = useState('')

  const { data: saml, isLoading } = useQuery({
    queryKey: ['admin', 'saml'],
    queryFn: getSAMLConfig,
  })

  useEffect(() => {
    if (saml) {
      setMetadataURL(saml.metadata_url)
      setCertPEM(saml.cert_pem)
    }
  }, [saml])

  function readFile(file: File, setter: (v: string) => void) {
    const reader = new FileReader()
    reader.onload = (e) => setter((e.target?.result as string) ?? '')
    reader.readAsText(file)
  }

  const saveMutation = useMutation({
    mutationFn: () => saveSAMLConfig({ metadata_url: metadataURL, cert_pem: certPEM, key_pem: keyPEM }),
    onSuccess: (res) => {
      setSaved(true)
      setSaveError('')
      setWarning(res.warning ?? '')
      setTimeout(() => setSaved(false), 3000)
      qc.invalidateQueries({ queryKey: ['admin', 'saml'] })
    },
    onError: (err) => setSaveError(extractError(err)),
  })

  if (isLoading) return <div className="py-4 text-center text-sm text-gray-400">Loading…</div>

  const spMetadataURL = saml?.sp_metadata_url ?? ''

  return (
    <div className="space-y-4 px-5 py-4">
      <div className="flex items-center gap-3">
        <span className={cn(
          'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
          saml?.configured ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-600'
        )}>
          {saml?.configured ? 'Configured' : 'Not configured'}
        </span>
        {saml?.configured && (
          <span className="text-xs text-gray-500">
            SP metadata:{' '}
            <button
              className="font-mono text-blue-600 underline decoration-dotted hover:decoration-solid"
              onClick={() => navigator.clipboard.writeText(spMetadataURL)}
              title="Copy to clipboard"
            >
              {spMetadataURL}
            </button>
          </span>
        )}
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">IdP metadata URL</label>
        <Input
          placeholder="https://idp.example.com/saml/metadata"
          value={metadataURL}
          onChange={(e) => setMetadataURL(e.target.value)}
          className="max-w-lg font-mono text-sm"
        />
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          SP certificate (PEM)
          {saml?.configured && !certPEM && <span className="ml-2 text-xs font-normal text-gray-400">already configured</span>}
        </label>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => certFileRef.current?.click()}>Upload .pem / .crt</Button>
          {certPEM && <span className="text-xs text-gray-500 truncate max-w-xs font-mono">{certPEM.split('\n')[0]}…</span>}
        </div>
        <input ref={certFileRef} type="file" accept=".pem,.crt,.cer" className="hidden"
          onChange={(e) => { const f = e.target.files?.[0]; if (f) readFile(f, setCertPEM) }} />
        {certPEM && (
          <textarea rows={4}
            className="mt-1 w-full max-w-lg rounded border border-gray-300 p-2 font-mono text-xs text-gray-600"
            value={certPEM} onChange={(e) => setCertPEM(e.target.value)} />
        )}
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          SP private key (PEM)
          {saml?.configured && !keyPEM && <span className="ml-2 text-xs font-normal text-gray-400">already configured — upload to replace</span>}
        </label>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => keyFileRef.current?.click()}>Upload .pem / .key</Button>
          {keyPEM && <span className="text-xs text-gray-500 font-mono">{keyPEM.split('\n')[0]}…</span>}
        </div>
        <input ref={keyFileRef} type="file" accept=".pem,.key" className="hidden"
          onChange={(e) => { const f = e.target.files?.[0]; if (f) readFile(f, setKeyPEM) }} />
        {keyPEM && (
          <textarea rows={4}
            className="mt-1 w-full max-w-lg rounded border border-gray-300 p-2 font-mono text-xs text-gray-600"
            value={keyPEM} onChange={(e) => setKeyPEM(e.target.value)} />
        )}
      </div>

      <div className="flex items-center gap-3 pt-1">
        <Button size="sm" onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
          {saveMutation.isPending ? 'Saving…' : 'Save SAML config'}
        </Button>
        {saveError && <p className="text-sm text-red-600">{saveError}</p>}
        {saved && !warning && <p className="text-sm text-green-600">SAML config saved.</p>}
        {warning && <p className="text-sm text-amber-600">{warning}</p>}
      </div>
    </div>
  )
}

// ── Tab definitions ───────────────────────────────────────────────────────────

type Tab = 'general' | 'branding' | 'auth' | 'features'

const TABS: { id: Tab; label: string }[] = [
  { id: 'general',  label: 'General' },
  { id: 'branding', label: 'Branding' },
  { id: 'auth',     label: 'Authentication' },
  { id: 'features', label: 'Features' },
]

// ── Tab panels ────────────────────────────────────────────────────────────────

function GeneralPanel({
  bool, num, str,
  setBool, setNum, setStr,
  onSave, isPending, error, saved,
}: {
  bool: (k: string) => boolean
  num: (k: string) => number
  str: (k: string) => string
  setBool: (k: string, v: boolean) => void
  setNum: (k: string, v: number) => void
  setStr: (k: string, v: string) => void
  onSave: () => void
  isPending: boolean
  error: string
  saved: boolean
}) {
  const { data: statuses = [] } = useQuery({ queryKey: ['statuses'], queryFn: listStatuses })
  // Reopen target should be an active, non-system status (not Resolved/Closed).
  const targetableStatuses = statuses.filter((s) => s.active && s.kind !== 'system')

  return (
    <div className="space-y-6">
      <Section title="Submissions">
        <SettingRow
          label="Guest submission"
          description="Allow unauthenticated users to submit a ticket using only their email address. They receive a tracking number to follow up without creating an account."
        >
          <Toggle checked={bool('guest_submission_enabled')} onChange={(v) => setBool('guest_submission_enabled', v)} />
        </SettingRow>
      </Section>

      <Section title="Ticket lifecycle">
        <SettingRow
          label="Reopen window"
          description="How many days after resolution a user may reopen their ticket by adding a reply. Set to 0 to prevent reopening entirely."
        >
          <div className="flex items-center gap-2">
            <Input
              type="number" min={0} className="w-20 text-right"
              value={num('reopen_window_days')}
              onChange={(e) => setNum('reopen_window_days', Math.max(0, parseInt(e.target.value, 10) || 0))}
            />
            <span className="text-sm text-gray-500">days</span>
          </div>
        </SettingRow>
        <SettingRow
          label="Reopen target status"
          description="The status a ticket is moved to when a user reopens it."
        >
          <Select
            className="w-44"
            value={str('reopen_target_status_name')}
            onChange={(e) => setStr('reopen_target_status_name', e.target.value)}
          >
            <option value="">Select…</option>
            {targetableStatuses.map((s) => (
              <option key={s.id} value={s.name}>{s.name}</option>
            ))}
          </Select>
        </SettingRow>
      </Section>

      <SaveBar onSave={onSave} isPending={isPending} error={error} saved={saved} />
    </div>
  )
}

function BrandingPanel({
  str, setStr,
  onSave, isPending, error, saved,
}: {
  str: (k: string) => string
  setStr: (k: string, v: string) => void
  onSave: () => void
  isPending: boolean
  error: string
  saved: boolean
}) {
  const qc = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [logoError, setLogoError] = useState('')
  const [uploading, setUploading] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirmDeleteLogo, setConfirmDeleteLogo] = useState(false)
  const [logoKey, setLogoKey] = useState(0)

  const { data: siteConfig } = useQuery({ queryKey: ['site-config'], queryFn: getSiteConfig })
  const currentLogoURL = siteConfig?.logo_url ?? ''

  async function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    setLogoError('')
    setUploading(true)
    try {
      await uploadLogo(file)
      setLogoKey((k) => k + 1)
      qc.invalidateQueries({ queryKey: ['site-config'] })
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
    } catch (err) {
      setLogoError(extractError(err))
    } finally {
      setUploading(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }

  async function handleDeleteLogo() {
    setLogoError('')
    setDeleting(true)
    try {
      await deleteLogo()
      setLogoKey((k) => k + 1)
      qc.invalidateQueries({ queryKey: ['site-config'] })
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
      setConfirmDeleteLogo(false)
    } catch (err) {
      setLogoError(extractError(err))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <div className="space-y-6">
      <Section title="Identity">
        <SettingRow
          label="Site name"
          description="Displayed in the sidebar header and browser tab when no logo is set."
        >
          <Input
            className="w-56"
            placeholder="Go Help Desk"
            value={str('site_name')}
            onChange={(e) => setStr('site_name', e.target.value)}
          />
        </SettingRow>

        <div className="px-5 py-4 space-y-3">
          <div>
            <div className="text-sm font-medium text-gray-900">Logo</div>
            <div className="mt-0.5 text-sm text-gray-500">
              Target size: <span className="font-medium">320 × 64 px</span> · PNG, SVG, JPG, or GIF · Max 2 MB.
              Larger images are scaled proportionally to fit. The logo replaces the site name in the sidebar.
            </div>
          </div>

          {currentLogoURL && (
            <div className="flex items-center gap-3">
              <img
                src={`${currentLogoURL}?v=${logoKey}`}
                alt="Current logo"
                className="h-8 max-w-[200px] rounded border object-contain p-1"
              />
              <Button
                variant="outline"
                size="sm"
                onClick={() => setConfirmDeleteLogo(true)}
                disabled={deleting || uploading}
              >
                {deleting ? 'Removing…' : 'Remove logo'}
              </Button>
            </div>
          )}
          <ConfirmDialog
            open={confirmDeleteLogo}
            onOpenChange={setConfirmDeleteLogo}
            title="Remove site logo?"
            description="The default logo will be shown until you upload a new one."
            confirmLabel="Remove logo"
            isPending={deleting}
            onConfirm={handleDeleteLogo}
          />

          <div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => fileInputRef.current?.click()}
              disabled={uploading || deleting}
            >
              {uploading ? 'Uploading…' : currentLogoURL ? 'Replace logo' : 'Upload logo'}
            </Button>
            <input
              ref={fileInputRef}
              type="file"
              accept=".png,.jpg,.jpeg,.gif,.svg"
              className="hidden"
              onChange={handleFileChange}
            />
          </div>

          {logoError && <p className="text-sm text-red-600">{logoError}</p>}
        </div>
      </Section>

      <SaveBar onSave={onSave} isPending={isPending} error={error} saved={saved} />
    </div>
  )
}

function AuthPanel({
  bool, strArr,
  setBool, toggleStrArr,
  onSave, isPending, error, saved,
}: {
  bool: (k: string) => boolean
  strArr: (k: string) => string[]
  setBool: (k: string, v: boolean) => void
  toggleStrArr: (k: string, item: string, checked: boolean) => void
  onSave: () => void
  isPending: boolean
  error: string
  saved: boolean
}) {
  return (
    <div className="space-y-6">
      <Section title="SAML">
        <div>
          <SettingRow
            label="Enable SAML login"
            description="Authenticate users via your SAML 2.0 identity provider (Okta, Azure AD, Google Workspace). Admins always retain local login as a failsafe."
          >
            <Toggle checked={bool('saml_enabled')} onChange={(v) => setBool('saml_enabled', v)} />
          </SettingRow>
          {bool('saml_enabled') && (
            <div className="border-t bg-gray-50">
              <div className="px-5 pt-3 pb-0">
                <p className="text-xs font-medium uppercase tracking-wider text-gray-400">SAML configuration</p>
              </div>
              <SAMLSection />
            </div>
          )}
        </div>
      </Section>

      <Section title="Multi-factor authentication">
        <SettingRow
          label="Enable MFA"
          description="Allow users to opt in to TOTP (Google Authenticator, Authy, 1Password). Users can enable MFA from their profile; enrolled users are prompted for a one-time code at each sign-in."
        >
          <Toggle checked={bool('mfa_enabled')} onChange={(v) => setBool('mfa_enabled', v)} />
        </SettingRow>
        {bool('mfa_enabled') && (
          <SettingRow
            label="Require MFA for roles"
            description="Users in the selected roles must enroll in MFA to sign in. Unenrolled users are forced through setup on their next login; leave all unchecked to keep MFA opt-in."
          >
            <div className="flex gap-4">
              {(['admin', 'staff', 'user'] as const).map((r) => (
                <label key={r} className="flex items-center gap-1.5 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                    checked={strArr('mfa_enforced_roles').includes(r)}
                    onChange={(e) => toggleStrArr('mfa_enforced_roles', r, e.target.checked)}
                  />
                  <span className="text-sm capitalize text-gray-700">{r}</span>
                </label>
              ))}
            </div>
          </SettingRow>
        )}
      </Section>

      <SaveBar onSave={onSave} isPending={isPending} error={error} saved={saved} />
    </div>
  )
}

// ── SLA policies blade ────────────────────────────────────────────────────────

const PRIORITIES = ['critical', 'high', 'medium', 'low'] as const
const PRIORITY_COLORS: Record<string, string> = {
  critical: 'bg-red-100 text-red-700',
  high: 'bg-orange-100 text-orange-700',
  medium: 'bg-yellow-100 text-yellow-700',
  low: 'bg-blue-100 text-blue-700',
}

function fmtMin(m: number) {
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  const rem = m % 60
  return rem ? `${h}h ${rem}m` : `${h}h`
}

type PolicyForm = {
  name: string
  priority: string
  category_id: string
  response_target_min: number
  resolution_target_min: number
}

const EMPTY_FORM: PolicyForm = {
  name: '',
  priority: 'medium',
  category_id: '',
  response_target_min: 480,
  resolution_target_min: 2880,
}

function PolicyFormRow({
  form, setForm, categories, onSave, onCancel, isPending,
}: {
  form: PolicyForm
  setForm: React.Dispatch<React.SetStateAction<PolicyForm>>
  categories: { id: string; name: string; active: boolean }[]
  onSave: () => void
  onCancel: () => void
  isPending: boolean
}) {
  return (
    <tr className="bg-blue-50">
      <td className="px-3 py-2">
        <Input
          className="h-7 text-sm"
          value={form.name}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
          placeholder="Policy name"
        />
      </td>
      <td className="px-3 py-2">
        <Select
          className="h-7 text-sm"
          value={form.priority}
          onChange={(e) => setForm((f) => ({ ...f, priority: e.target.value }))}
        >
          {PRIORITIES.map((p) => (
            <option key={p} value={p} className="capitalize">{p}</option>
          ))}
        </Select>
      </td>
      <td className="px-3 py-2">
        <Select
          className="h-7 text-sm"
          value={form.category_id}
          onChange={(e) => setForm((f) => ({ ...f, category_id: e.target.value }))}
        >
          <option value="">All categories</option>
          {categories.filter((c) => c.active).map((c) => (
            <option key={c.id} value={c.id}>{c.name}</option>
          ))}
        </Select>
      </td>
      <td className="px-3 py-2">
        <div className="flex items-center gap-1">
          <Input
            type="number" min={1} className="h-7 w-20 text-sm"
            value={form.response_target_min}
            onChange={(e) => setForm((f) => ({ ...f, response_target_min: Math.max(1, parseInt(e.target.value, 10) || 1) }))}
          />
          <span className="text-xs text-gray-400">min</span>
        </div>
      </td>
      <td className="px-3 py-2">
        <div className="flex items-center gap-1">
          <Input
            type="number" min={1} className="h-7 w-20 text-sm"
            value={form.resolution_target_min}
            onChange={(e) => setForm((f) => ({ ...f, resolution_target_min: Math.max(1, parseInt(e.target.value, 10) || 1) }))}
          />
          <span className="text-xs text-gray-400">min</span>
        </div>
      </td>
      <td className="px-3 py-2">
        <div className="flex gap-2">
          <Button size="sm" onClick={onSave} disabled={isPending}>Save</Button>
          <Button size="sm" variant="outline" onClick={onCancel}>Cancel</Button>
        </div>
      </td>
    </tr>
  )
}

function SLAPoliciesSection() {
  const qc = useQueryClient()
  const [editingId, setEditingId] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<SLAPolicy | null>(null)
  const [showAdd, setShowAdd] = useState(false)
  const [form, setForm] = useState<PolicyForm>(EMPTY_FORM)
  const [formError, setFormError] = useState('')

  const { data: policies = [] } = useQuery({ queryKey: ['sla-policies'], queryFn: listSLAPolicies })
  const { data: categories = [] } = useQuery({ queryKey: ['categories'], queryFn: listCategories })

  function startEdit(p: SLAPolicy) {
    setEditingId(p.id)
    setShowAdd(false)
    setFormError('')
    setForm({
      name: p.name,
      priority: p.priority,
      category_id: p.category_id ?? '',
      response_target_min: p.response_target_min,
      resolution_target_min: p.resolution_target_min,
    })
  }

  function startAdd() {
    setShowAdd(true)
    setEditingId(null)
    setFormError('')
    setForm(EMPTY_FORM)
  }

  const createMutation = useMutation({
    mutationFn: () => createSLAPolicy({
      name: form.name,
      priority: form.priority,
      category_id: form.category_id || undefined,
      response_target_min: form.response_target_min,
      resolution_target_min: form.resolution_target_min,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sla-policies'] })
      setShowAdd(false)
      setForm(EMPTY_FORM)
    },
    onError: (err) => setFormError(extractError(err)),
  })

  const updateMutation = useMutation({
    mutationFn: (id: string) => updateSLAPolicy(id, {
      name: form.name,
      priority: form.priority,
      category_id: form.category_id || undefined,
      clear_category: !form.category_id,
      response_target_min: form.response_target_min,
      resolution_target_min: form.resolution_target_min,
    }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sla-policies'] })
      setEditingId(null)
    },
    onError: (err) => setFormError(extractError(err)),
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteSLAPolicy(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['sla-policies'] })
      setPendingDelete(null)
    },
  })

  const showTable = policies.length > 0 || showAdd

  return (
    <div className="border-t bg-gray-50">
      <div className="px-5 pt-3 pb-0">
        <p className="text-xs font-medium uppercase tracking-wider text-gray-400">SLA Policies</p>
      </div>
      <div className="px-5 py-4 space-y-3">
        {showTable ? (
          <div className="overflow-x-auto rounded border bg-white">
            <table className="w-full text-sm">
              <thead className="border-b bg-gray-50">
                <tr className="text-left text-xs font-medium text-gray-500">
                  <th className="px-3 py-2">Name</th>
                  <th className="px-3 py-2">Priority</th>
                  <th className="px-3 py-2">Category</th>
                  <th className="px-3 py-2">Response</th>
                  <th className="px-3 py-2">Resolution</th>
                  <th className="px-3 py-2"></th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {policies.map((p) =>
                  editingId === p.id ? (
                    <PolicyFormRow
                      key={p.id}
                      form={form} setForm={setForm} categories={categories}
                      onSave={() => updateMutation.mutate(p.id)}
                      onCancel={() => setEditingId(null)}
                      isPending={updateMutation.isPending}
                    />
                  ) : (
                    <tr key={p.id} className="hover:bg-gray-50">
                      <td className="px-3 py-2 font-medium">{p.name}</td>
                      <td className="px-3 py-2">
                        <span className={cn('rounded-full px-2 py-0.5 text-xs font-medium capitalize', PRIORITY_COLORS[p.priority])}>
                          {p.priority}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-gray-600">
                        {p.category_id
                          ? (categories.find((c) => c.id === p.category_id)?.name ?? '—')
                          : 'All categories'}
                      </td>
                      <td className="px-3 py-2 tabular-nums text-gray-600">{fmtMin(p.response_target_min)}</td>
                      <td className="px-3 py-2 tabular-nums text-gray-600">{fmtMin(p.resolution_target_min)}</td>
                      <td className="px-3 py-2">
                        <div className="flex gap-3">
                          <button className="text-xs text-blue-600 hover:underline" onClick={() => startEdit(p)}>Edit</button>
                          <button
                            className="text-xs text-red-600 hover:underline disabled:opacity-40"
                            onClick={() => setPendingDelete(p)}
                            disabled={deleteMutation.isPending}
                          >
                            Delete
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                )}
                {showAdd && (
                  <PolicyFormRow
                    form={form} setForm={setForm} categories={categories}
                    onSave={() => createMutation.mutate()}
                    onCancel={() => setShowAdd(false)}
                    isPending={createMutation.isPending}
                  />
                )}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="text-sm text-gray-500">No SLA policies defined.</p>
        )}
        {formError && <p className="text-sm text-red-600">{formError}</p>}
        {!showAdd && !editingId && (
          <Button size="sm" variant="outline" onClick={startAdd}>+ Add policy</Button>
        )}
      </div>
      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => { if (!open) setPendingDelete(null) }}
        title={`Delete SLA policy "${pendingDelete?.name ?? ''}"?`}
        description="Tickets currently tracking against this policy will lose their SLA targets."
        confirmLabel="Delete policy"
        isPending={deleteMutation.isPending}
        onConfirm={() => { if (pendingDelete) deleteMutation.mutate(pendingDelete.id) }}
      />
    </div>
  )
}

// ── Features panel ────────────────────────────────────────────────────────────

function FeaturesPanel({
  bool, setBool,
  onSave, isPending, error, saved,
}: {
  bool: (k: string) => boolean
  setBool: (k: string, v: boolean) => void
  onSave: () => void
  isPending: boolean
  error: string
  saved: boolean
}) {
  return (
    <div className="space-y-6">
      <Section title="SLA">
        <div>
          <SettingRow
            label="SLA tracking"
            description="Enable SLA response and resolution time targets configurable per priority and category. When enabled, tickets approaching or breaching their SLA target are highlighted."
          >
            <Toggle checked={bool('sla_enabled')} onChange={(v) => setBool('sla_enabled', v)} />
          </SettingRow>
          {bool('sla_enabled') && <SLAPoliciesSection />}
        </div>
      </Section>

      <SaveBar onSave={onSave} isPending={isPending} error={error} saved={saved} />
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export function SettingsPage() {
  const qc = useQueryClient()
  const [activeTab, setActiveTab] = useState<Tab>('general')
  const [local, setLocal] = useState<Record<string, unknown>>({})
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)

  const { data: settings, isLoading } = useQuery({
    queryKey: ['admin', 'settings'],
    queryFn: getSettings,
  })

  useEffect(() => {
    if (settings) setLocal(settings)
  }, [settings])

  const saveMutation = useMutation({
    mutationFn: () => updateSettings(local),
    onSuccess: () => {
      setSaved(true)
      setSaveError('')
      setTimeout(() => setSaved(false), 2500)
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
      qc.invalidateQueries({ queryKey: ['site-config'] })
    },
    onError: (err) => setSaveError(extractError(err)),
  })

  function bool(key: string) { return Boolean(local[key]) }
  function num(key: string) { return Number(local[key] ?? 0) }
  function str(key: string) { return String(local[key] ?? '') }
  function strArr(key: string): string[] {
    const v = local[key]
    if (Array.isArray(v)) return v as string[]
    if (typeof v === 'string' && v) return v.split(',').map((s) => s.trim()).filter(Boolean)
    return []
  }
  function setBool(key: string, v: boolean) { setLocal((s) => ({ ...s, [key]: v })) }
  function setNum(key: string, v: number) { setLocal((s) => ({ ...s, [key]: v })) }
  function setStr(key: string, v: string) { setLocal((s) => ({ ...s, [key]: v })) }
  function toggleStrArr(key: string, item: string, checked: boolean) {
    setLocal((s) => {
      const current = strArr(key)
      const next = checked ? [...new Set([...current, item])] : current.filter((x) => x !== item)
      return { ...s, [key]: next }
    })
  }

  const panelProps = {
    bool, num, str, strArr,
    setBool, setNum, setStr, toggleStrArr,
    onSave: () => saveMutation.mutate(),
    isPending: saveMutation.isPending,
    error: saveError,
    saved,
  }

  if (isLoading) {
    return <Layout><div className="flex justify-center py-12"><Spinner /></div></Layout>
  }

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Settings</h1>
          <p className="mt-1 text-sm text-gray-500">System-wide configuration for this instance.</p>
        </div>

        {/* Tab bar */}
        <div className="border-b">
          <nav className="-mb-px flex gap-6">
            {TABS.map((tab) => (
              <button
                key={tab.id}
                onClick={() => setActiveTab(tab.id)}
                className={cn(
                  'border-b-2 pb-3 text-sm font-medium whitespace-nowrap transition-colors',
                  activeTab === tab.id
                    ? 'border-blue-600 text-blue-600'
                    : 'border-transparent text-gray-500 hover:border-gray-300 hover:text-gray-700'
                )}
              >
                {tab.label}
              </button>
            ))}
          </nav>
        </div>

        {/* Active panel */}
        <div className="max-w-2xl">
          {activeTab === 'general'  && <GeneralPanel  {...panelProps} />}
          {activeTab === 'branding' && <BrandingPanel {...panelProps} />}
          {activeTab === 'auth'     && <AuthPanel     {...panelProps} />}
          {activeTab === 'features' && <FeaturesPanel {...panelProps} />}
        </div>
      </div>
    </Layout>
  )
}
