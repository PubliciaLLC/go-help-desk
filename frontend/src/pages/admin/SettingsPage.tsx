import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings, getSAMLConfig, saveSAMLConfig } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'
import { useState, useEffect, useRef } from 'react'

// ── Toggle ────────────────────────────────────────────────────────────────────

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

// ── Layout helpers ────────────────────────────────────────────────────────────

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

// ── SAML configuration section ────────────────────────────────────────────────

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
      {/* Status badge */}
      <div className="flex items-center gap-3">
        <span
          className={cn(
            'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
            saml?.configured
              ? 'bg-green-100 text-green-800'
              : 'bg-gray-100 text-gray-600'
          )}
        >
          {saml?.configured ? 'Configured' : 'Not configured'}
        </span>
        {saml?.configured && (
          <span className="text-xs text-gray-500">
            SP metadata URL:{' '}
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

      {/* Metadata URL */}
      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">IdP metadata URL</label>
        <Input
          placeholder="https://idp.example.com/saml/metadata"
          value={metadataURL}
          onChange={(e) => setMetadataURL(e.target.value)}
          className="max-w-lg font-mono text-sm"
        />
      </div>

      {/* SP Certificate */}
      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          SP certificate (PEM)
          {saml?.configured && !certPEM && (
            <span className="ml-2 text-xs font-normal text-gray-400">already configured</span>
          )}
        </label>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => certFileRef.current?.click()}>
            Upload .pem / .crt
          </Button>
          {certPEM && (
            <span className="text-xs text-gray-500 truncate max-w-xs font-mono">
              {certPEM.split('\n')[0]}…
            </span>
          )}
        </div>
        <input
          ref={certFileRef}
          type="file"
          accept=".pem,.crt,.cer"
          className="hidden"
          onChange={(e) => { const f = e.target.files?.[0]; if (f) readFile(f, setCertPEM) }}
        />
        {certPEM && (
          <textarea
            rows={4}
            className="mt-1 w-full max-w-lg rounded border border-gray-300 p-2 font-mono text-xs text-gray-600"
            value={certPEM}
            onChange={(e) => setCertPEM(e.target.value)}
          />
        )}
      </div>

      {/* SP Private Key */}
      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          SP private key (PEM)
          {saml?.configured && !keyPEM && (
            <span className="ml-2 text-xs font-normal text-gray-400">already configured — upload to replace</span>
          )}
        </label>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => keyFileRef.current?.click()}>
            Upload .pem / .key
          </Button>
          {keyPEM && (
            <span className="text-xs text-gray-500 font-mono">
              {keyPEM.split('\n')[0]}…
            </span>
          )}
        </div>
        <input
          ref={keyFileRef}
          type="file"
          accept=".pem,.key"
          className="hidden"
          onChange={(e) => { const f = e.target.files?.[0]; if (f) readFile(f, setKeyPEM) }}
        />
        {keyPEM && (
          <textarea
            rows={4}
            className="mt-1 w-full max-w-lg rounded border border-gray-300 p-2 font-mono text-xs text-gray-600"
            value={keyPEM}
            onChange={(e) => setKeyPEM(e.target.value)}
          />
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

// ── Page ──────────────────────────────────────────────────────────────────────

export function SettingsPage() {
  const qc = useQueryClient()
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

  if (isLoading) {
    return <Layout><div className="flex justify-center py-12"><Spinner /></div></Layout>
  }

  return (
    <Layout>
      <div className="max-w-2xl space-y-8">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Settings</h1>
          <p className="mt-1 text-sm text-gray-500">System-wide configuration for this Open Help Desk instance.</p>
        </div>

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
                type="number"
                min={0}
                className="w-20 text-right"
                value={num('reopen_window_days')}
                onChange={(e) => setNum('reopen_window_days', Math.max(0, parseInt(e.target.value, 10) || 0))}
              />
              <span className="text-sm text-gray-500">days</span>
            </div>
          </SettingRow>
          <SettingRow
            label="Reopen target status"
            description="The status a ticket is moved to when a user reopens it. Must match the exact name of an existing status."
          >
            <Input
              className="w-44"
              value={str('reopen_target_status_name')}
              onChange={(e) => setStr('reopen_target_status_name', e.target.value)}
            />
          </SettingRow>
        </Section>

        <Section title="Authentication">
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
          <SettingRow
            label="Multi-factor authentication"
            description="Prompt users to enroll in TOTP (Google Authenticator, Authy) on their next login. Once enrolled, a one-time code is required at each sign-in."
          >
            <Toggle checked={bool('mfa_enabled')} onChange={(v) => setBool('mfa_enabled', v)} />
          </SettingRow>
          {bool('mfa_enabled') && (
            <SettingRow
              label="Enforce MFA for roles"
              description="Roles that must enroll in MFA. Users in enforced roles are prompted on next login."
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

        <Section title="Features">
          <SettingRow
            label="SLA tracking"
            description="Enable SLA response and resolution time targets configurable per priority and category. When enabled, tickets approaching or breaching their SLA target are highlighted."
          >
            <Toggle checked={bool('sla_enabled')} onChange={(v) => setBool('sla_enabled', v)} />
          </SettingRow>
        </Section>

        <div className="flex items-center gap-3 pt-2">
          <Button onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
            {saveMutation.isPending ? 'Saving…' : 'Save settings'}
          </Button>
          {saveError && <p className="text-sm text-red-600">{saveError}</p>}
          {saved && <p className="text-sm text-green-600">Settings saved.</p>}
        </div>
      </div>
    </Layout>
  )
}
