import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'
import { useState, useEffect } from 'react'

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
  function setBool(key: string, v: boolean) { setLocal((s) => ({ ...s, [key]: v })) }
  function setNum(key: string, v: number) { setLocal((s) => ({ ...s, [key]: v })) }
  function setStr(key: string, v: string) { setLocal((s) => ({ ...s, [key]: v })) }

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
          <SettingRow
            label="SAML SSO"
            description="Authenticate users via your SAML 2.0 identity provider (Okta, Azure AD, Google Workspace). Requires SAML environment variables to be configured. Admins retain local login as a failsafe."
          >
            <Toggle checked={bool('saml_enabled')} onChange={(v) => setBool('saml_enabled', v)} />
          </SettingRow>
          <SettingRow
            label="Multi-factor authentication"
            description="Prompt users to enroll in TOTP (Google Authenticator, Authy) on their next login. Once enrolled, a one-time code is required at each sign-in."
          >
            <Toggle checked={bool('mfa_enabled')} onChange={(v) => setBool('mfa_enabled', v)} />
          </SettingRow>
          <SettingRow
            label="Enforce MFA for roles"
            description="Comma-separated list of roles that must enroll in MFA (e.g. admin,staff). Leave blank to make MFA optional for all roles."
          >
            <Input
              className="w-52"
              placeholder="e.g. admin,staff"
              value={str('mfa_enforced_roles')}
              onChange={(e) => setStr('mfa_enforced_roles', e.target.value)}
            />
          </SettingRow>
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
