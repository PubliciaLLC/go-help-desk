import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { getSettings, updateSettings, getSAMLConfig, saveSAMLConfig, getOIDCConfig, saveOIDCConfig, getSiteConfig, uploadLogo, deleteLogo, listStatuses, listCategories, listSLAPolicies, createSLAPolicy, updateSLAPolicy, deleteSLAPolicy } from '@/api/admin'
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
import { fmtMin } from '@/lib/format'

// ── Shared primitives ─────────────────────────────────────────────────────────

function Toggle({ checked, onChange, label }: {
  checked: boolean
  onChange: (v: boolean) => void
  /** The switch's own name, where the surrounding row does not supply one. */
  label?: string
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
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

  /* The effects below seed local form state from a react-query result. That is
     the cascading-render pattern react-hooks/set-state-in-effect warns about
     (https://react.dev/learn/you-might-not-need-an-effect). Suppressed here,
     not refactored: switching the lint gate on should not also change how the
     admin forms behave. TODO: derive the state, or remount the form with a key.
     eslint-disable-next-line is not enough — the rule reports at each setState
     call, so the whole effect is wrapped. */
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (saml) {
      setMetadataURL(saml.metadata_url)
      setCertPEM(saml.cert_pem)
    }
  }, [saml])
  /* eslint-enable react-hooks/set-state-in-effect */

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


// ── OIDC section ─────────────────────────────────────────────────────────────

function OIDCSection() {
  const qc = useQueryClient()

  const [issuerURL, setIssuerURL] = useState('')
  const [clientID, setClientID] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [enabled, setEnabled] = useState(false)
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState(false)

  const { data: oidc, isLoading } = useQuery({
    queryKey: ['admin', 'oidc'],
    queryFn: getOIDCConfig,
  })

  /* Same pattern as the SAML effect above — see the note there. Suppressed, not
     refactored, so enabling the lint gate does not change admin form behaviour. */
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (oidc) {
      setIssuerURL(oidc.issuer_url ?? '')
      setClientID(oidc.client_id ?? '')
      setClientSecret('')
      setEnabled(oidc.enabled ?? false)
    }
  }, [oidc])
  /* eslint-enable react-hooks/set-state-in-effect */

  const saveMutation = useMutation({
    mutationFn: () =>
      saveOIDCConfig({
        enabled,
        issuer_url: issuerURL,
        client_id: clientID,
        client_secret: clientSecret,
      }),
    onSuccess: () => {
      setSaved(true)
      setSaveError('')
      setClientSecret('')
      qc.invalidateQueries({ queryKey: ['admin', 'oidc'] })
      setTimeout(() => setSaved(false), 3000)
    },
    onError: (err) => {
      setSaveError(extractError(err))
      setSaved(false)
    },
  })

  if (isLoading) {
    return <div className="py-4 text-center text-sm text-gray-400">Loading…</div>
  }

  const redirectURL = oidc?.redirect_url ?? ''
  const configured = oidc?.configured ?? false

  return (
    <div className="space-y-4 px-5 py-4">
      <div className="flex items-center gap-3">
        <span className={cn(
          'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
          configured ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-600'
        )}>
          {configured ? 'Configured' : 'Not configured'}
        </span>

        {configured && (
          <span className="text-xs text-gray-500">
            Redirect URL:{' '}
            <button
              type="button"
              className="font-mono text-blue-600 underline decoration-dotted hover:decoration-solid"
              onClick={() => navigator.clipboard.writeText(redirectURL)}
              title="Copy to clipboard"
            >
              {redirectURL}
            </button>
          </span>
        )}
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          Enable OIDC login
        </label>
        <Toggle checked={enabled} onChange={setEnabled} />
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          Issuer URL
        </label>
        <Input
          placeholder="https://login.example.com"
          value={issuerURL}
          onChange={(e) => setIssuerURL(e.target.value)}
          className="max-w-lg font-mono text-sm"
        />
        <p className="text-xs text-gray-500">
          The OpenID Connect issuer URL for your identity provider.
        </p>
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          Client ID
        </label>
        <Input
          placeholder="your-client-id"
          value={clientID}
          onChange={(e) => setClientID(e.target.value)}
          className="max-w-lg font-mono text-sm"
        />
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          Client secret
          {configured && !clientSecret && (
            <span className="ml-2 text-xs font-normal text-gray-400">
              already configured
            </span>
          )}
        </label>
        <Input
          type="password"
          placeholder={configured ? 'Leave blank to keep existing secret' : 'Client secret'}
          value={clientSecret}
          onChange={(e) => setClientSecret(e.target.value)}
          className="max-w-lg font-mono text-sm"
        />
      </div>

      {redirectURL && (
        <div className="space-y-1">
          <label className="block text-sm font-medium text-gray-700">
            Redirect URL
          </label>
          <div className="flex items-center gap-2">
            <div className="w-full max-w-lg rounded border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-gray-600">
              {redirectURL}
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => navigator.clipboard.writeText(redirectURL)}
            >
              Copy
            </Button>
          </div>
          <p className="text-xs text-gray-500">
            Automatically generated by Go Help Desk. Add this URL to your OIDC provider.
          </p>
        </div>
      )}

      <div className="flex items-center gap-3 pt-2">
        <Button
          onClick={() => saveMutation.mutate()}
          disabled={saveMutation.isPending}
        >
          {saveMutation.isPending ? 'Saving…' : 'Save changes'}
        </Button>

        {saveError && (
          <p className="text-sm text-red-600">{saveError}</p>
        )}

        {saved && (
          <p className="text-sm text-green-600">
            OIDC config saved.
          </p>
        )}
      </div>
    </div>
  )
}

// ── Tab definitions ───────────────────────────────────────────────────────────

type Tab = 'general' | 'branding' | 'auth' | 'features' | 'attachments'

const TABS: { id: Tab; label: string }[] = [
  { id: 'general',     label: 'General' },
  { id: 'branding',    label: 'Branding' },
  { id: 'auth',        label: 'Authentication' },
  { id: 'features',    label: 'Features' },
  { id: 'attachments', label: 'Attachments' },
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
      <Section title="Tickets">
        <SettingRow
          label="Tracking number prefix"
          description="The GHD in GHD-2026-000001. One to eight uppercase letters or digits. Changing it affects only tickets created afterwards — numbers already issued are never rewritten, because they are in customers' inboxes and referenced in their replies. An instance that predates the rename from Open Help Desk can set this to OHD to keep one consistent series."
        >
          <Input
            value={str('ticket_prefix') || 'GHD'}
            onChange={(e) => setStr('ticket_prefix', e.target.value.toUpperCase())}
            maxLength={8}
            className="w-32 font-mono"
            aria-label="Tracking number prefix"
          />
        </SettingRow>
      </Section>

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

      <Section title="Ticket visibility">
        <SettingRow
          label="Limit staff to their group scope"
          description="When on, a staff member sees only tickets they reported, tickets assigned to them or to one of their groups, and tickets whose category and type a group of theirs covers. Admins always see everything. Off by default: with it off, every staff member can see every ticket, which is how earlier versions behaved. Turn it on once your groups and their category scopes are configured — staff in no group will see almost nothing."
        >
          <Toggle
            checked={bool('ticket_scope_enforced')}
            onChange={(v) => setBool('ticket_scope_enforced', v)}
          />
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
  /* A stored logo URL that will not load. On this branch that has one likely
     cause: handleServeLogo serves logo.png and nothing else, so an SVG left by
     a release that still accepted them 404s, the sidebar falls back to the
     site name, and nothing anywhere says why. The server's own comment says
     "the settings page says why" — this is that. */
  const [logoUnavailable, setLogoUnavailable] = useState(false)

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
              Target size: <span className="font-medium">320 × 64 px</span> · PNG, JPG, or GIF · Max 2 MB.
              Larger images are scaled proportionally to fit. The logo replaces the site name in the sidebar.
            </div>
            {/* One sentence, not a warning: an operator who used an SVG logo
                and finds the option gone should know it was deliberate. */}
            <div className="mt-0.5 text-sm text-gray-500">
              SVG is not accepted. The logo is the one uploaded file this application renders inside its own
              origin, and an SVG is a document that can carry script.
            </div>
          </div>

          {currentLogoURL && (
            <>
              <div className="flex items-center gap-3">
                <img
                  src={`${currentLogoURL}?v=${logoKey}`}
                  alt="Current logo"
                  className="h-8 max-w-[200px] rounded border object-contain p-1"
                  onLoad={() => setLogoUnavailable(false)}
                  onError={() => setLogoUnavailable(true)}
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
              {logoUnavailable && (
                <p className="text-sm text-amber-700">
                  This instance has a logo stored, but it cannot be loaded and is no longer served — an SVG
                  uploaded by an earlier version is the usual reason. The sidebar is showing the site name
                  instead. Upload a PNG, JPG or GIF to replace it.
                </p>
              )}
            </>
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
              accept=".png,.jpg,.jpeg,.gif"
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
  setBool, toggleStrArr, setStrArr,
  onSave, isPending, error, saved,
}: {
  bool: (k: string) => boolean
  strArr: (k: string) => string[]
  setBool: (k: string, v: boolean) => void
  toggleStrArr: (k: string, item: string, checked: boolean) => void
  setStrArr: (k: string, v: string[]) => void
  onSave: () => void
  isPending: boolean
  error: string
  saved: boolean
}) {
  const [confirmOpenReg, setConfirmOpenReg] = useState(false)
  const domainsFromParent = strArr('allowed_email_domains').join('\n')
  const [domainsText, setDomainsText] = useState(domainsFromParent)
  useEffect(() => { setDomainsText(domainsFromParent) }, [domainsFromParent])

  const domainsLocked = strArr('allowed_email_domains').length > 0

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

      <Section title="OIDC / OpenID Connect">
        <OIDCSection />
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

      <Section title="Registration">
        <SettingRow
          label="Allow self-service signup"
          description="Let users create their own accounts without an admin invitation. Requires email verification."
        >
          <Toggle checked={bool('self_signup_enabled')} onChange={(v) => setBool('self_signup_enabled', v)} />
        </SettingRow>
        {bool('self_signup_enabled') && (
          <>
            <div className="border-t px-5 py-4 space-y-2">
              <div className="text-sm font-medium text-gray-900">Allowed email domains</div>
              <div className="text-sm text-gray-500">
                One domain per line (e.g. <span className="font-mono">company.com</span>). Leave empty and enable open registration to allow any email.
              </div>
              <textarea
                rows={3}
                className="w-full max-w-xs rounded border border-gray-300 p-2 font-mono text-sm"
                placeholder={'company.com\nexample.org'}
                value={domainsText}
                onChange={(e) => setDomainsText(e.target.value)}
                onBlur={() =>
                  setStrArr(
                    'allowed_email_domains',
                    domainsText.split('\n').map((s) => s.trim()).filter(Boolean),
                  )
                }
              />
            </div>
            <SettingRow
              label="Allow open registration"
              description={
                domainsLocked
                  ? 'Clear the domain list above to enable open registration.'
                  : 'Anyone can sign up regardless of email domain.'
              }
            >
              <Toggle
                checked={bool('open_registration_enabled')}
                onChange={(v) => {
                  if (!v) { setBool('open_registration_enabled', false); return }
                  setConfirmOpenReg(true)
                }}
              />
            </SettingRow>
            <ConfirmDialog
              open={confirmOpenReg}
              onOpenChange={setConfirmOpenReg}
              title="Allow open registration?"
              description="Anyone with any email address will be able to create an account on this instance. Make sure this is intentional."
              confirmLabel="Enable open registration"
              onConfirm={() => { setBool('open_registration_enabled', true); setConfirmOpenReg(false) }}
            />
          </>
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
          <option value="">Any priority</option>
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
      priority: p.priority ?? '',
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
      priority: form.priority || undefined,
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
      priority: form.priority || undefined,
      clear_priority: !form.priority,
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
                        {p.priority ? (
                          <span className={cn('rounded-full px-2 py-0.5 text-xs font-medium capitalize', PRIORITY_COLORS[p.priority])}>
                            {p.priority}
                          </span>
                        ) : (
                          <span className="text-gray-600">Any priority</span>
                        )}
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

// ── Attachments panel ─────────────────────────────────────────────────────────

/**
 * The four reputation services, in the order the page shows them.
 *
 * Four independent toggles rather than one selected provider: they answer
 * different questions — VirusTotal counts engines, CIRCL says whether a
 * catalogue holds the file — and every enabled one is asked. Each carries its
 * own key, so configuring a second service does not destroy the key you pasted
 * for the first.
 *
 * `keySetting` is null for CIRCL alone, and the absence is the decision:
 * hashlookup authenticates nobody, so there is no key an operator could
 * supply, and an empty box beside the other three is a box somebody feels
 * obliged to fill and then goes looking for a fault when the feature works
 * without it.
 *
 * Every key here is write-only over the API — they are in secretSettingKeys
 * beside the OIDC client secret — so the dump returns `<setting>_set`, a
 * boolean saying only whether something is stored, and never the key itself.
 */
const REPUTATION_PROVIDERS: {
  id: string
  name: string
  description: string
  enabledSetting: string
  keySetting: string | null
  Terms: () => React.JSX.Element
}[] = [
  {
    id: 'virustotal',
    name: 'VirusTotal',
    description:
      'Seventy-odd engines on one file. The broadest answer of the four, and the page every analyst already knows.',
    enabledSetting: 'attachment_reputation_virustotal_enabled',
    keySetting: 'attachment_reputation_virustotal_key',
    Terms: VirusTotalTerms,
  },
  {
    id: 'metadefender',
    name: 'MetaDefender',
    description: "OPSWAT's cloud, with a smaller engine set and a larger daily allowance.",
    enabledSetting: 'attachment_reputation_metadefender_enabled',
    keySetting: 'attachment_reputation_metadefender_key',
    Terms: MetaDefenderTerms,
  },
  {
    id: 'polyswarm',
    name: 'PolySwarm',
    description:
      'The only one of the four that can say a file is a known legitimate binary, vouched for by a named vendor feed.',
    enabledSetting: 'attachment_reputation_polyswarm_enabled',
    keySetting: 'attachment_reputation_polyswarm_key',
    Terms: PolySwarmTerms,
  },
  {
    id: 'circl',
    name: 'CIRCL',
    description:
      'A catalogue rather than a scanner: it answers whether a hash set it re-publishes holds this file, never whether an engine flagged it. No key, and no account.',
    enabledSetting: 'attachment_reputation_circl_enabled',
    keySetting: null,
    Terms: CIRCLTerms,
  },
]

/**
 * What the server accepts when the operator has never set a list, from
 * admin.DefaultAllowedTypes().
 *
 * Duplicated here because the API does not send it: an instance that has never
 * set the list has no row, so the field would otherwise be empty — and an
 * empty list is itself a valid setting meaning "no attachments at all", which
 * is the opposite of what an unset instance does. Display only. An untouched
 * list is never written back, so an instance that never chose one keeps
 * following whatever the server's default becomes.
 */
const DEFAULT_ALLOWED_TYPES = ['.pdf', '.docx', '.xlsx', '.txt', '.log', '.jpg', '.jpeg', '.png', '.bmp']

/** Extensions out of the textarea: whitespace or commas, in any combination. */
function parseAllowedTypes(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

/**
 * What one provider says about their own free tier, in their words.
 *
 * Four rules from #168, all deliberate:
 *
 *   not dismissible      "a warning with an × becomes a warning nobody sees
 *                        twice". There is no control in here at all.
 *   always shown         including when the provider is switched off and when
 *                        no key is configured. Someone reading this page to
 *                        decide whether to switch it on is exactly the person
 *                        who needs it.
 *   quoted, attributed   we are not the licensing authority and must not read
 *                        as one. Quote them; let the operator judge their own
 *                        deployment. Anything we cannot find on the vendor's
 *                        own page does not go in quotation marks — the last
 *                        time this feature quoted a vendor's terms from
 *                        memory, the quote turned out not to exist and was
 *                        wrong in the permissive direction.
 *   one per provider     and no second warning elsewhere, and no confirmation
 *                        dialog on a toggle. That is reserved for downloading
 *                        an infected file, where the risk is immediate.
 *
 * Both halves are in each because they answer different questions: the limit
 * decides whether the feature works for them, the licence decides whether they
 * should switch it on at all.
 *
 * role="alert" follows InsecureConfigBanner, the one existing banner in this
 * codebase — consistency with the precedent, and the content does warrant
 * interrupting somebody who is about to paste a key in.
 */
function ReputationWarning({ children }: { children: React.ReactNode }) {
  return (
    <div
      role="alert"
      className="space-y-2 rounded border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900"
    >
      {children}
    </div>
  )
}

/** A link out of a warning. Never a navigation of ours. */
function TermsLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <a className="font-medium underline" href={href} target="_blank" rel="noreferrer noopener">
      {children}
    </a>
  )
}

/* The VirusTotal copy is specified word for word in #168 and is quoted rather
   than summarised. Softening it is the failure mode it exists to prevent: the
   penalty VirusTotal state is a ban on the operator's whole organisation, not
   on the key they pasted in here. */
function VirusTotalTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ VirusTotal's free API has limits, and is not licensed for commercial use
      </p>
      {/* #168 specifies this paragraph, and the first clause of it was written
          before verdicts expired: "looks up each infected attachment once and
          stores the result" stopped being true when the re-check interval
          below arrived. Reworded rather than left standing, because the
          difference is how much of the operator's allowance this spends. */}
      <p>
        The free public API allows <strong>4 requests per minute and 500 per day</strong>. Go Help Desk asks
        once for each quarantined attachment, stores the answer and asks again only when the interval below
        has passed, but on a busy instance some lookups will be skipped until the quota resets at 00:00 UTC.
        Skipped lookups show as <em>not checked</em> — never as clean.
      </p>
      <p>
        VirusTotal state that the public API “must not be used in commercial products or services”, and
        “must not be used in business workflows that do not contribute new files”. This feature only looks
        hashes up; it never uploads a file. If you are running Go Help Desk commercially, you need a paid
        VirusTotal API key. VirusTotal state the penalty for non-compliance as a permanent ban of the
        individual or organization.
      </p>
      <p>
        <TermsLink href="https://docs.virustotal.com/reference/public-vs-premium-api">
          VirusTotal: public vs premium API ↗
        </TermsLink>
      </p>
    </>
  )
}

/* MetaDefender's terms are not VirusTotal's, so the copy is not VirusTotal's
   either — the same shape, their numbers, their words.

   The quoted clause is from OPSWAT's current Terms of Service, section 6,
   read live rather than from #168: the issue quotes "personal, non-commercial
   use" with a carve-out for a "non-commercial personal or organizational
   capacity", and neither phrase appears in the published terms today. What
   they do say about free access is quoted here verbatim, and it is narrower
   than the issue's wording rather than broader, so the operator is not being
   told they have more room than they have. */
function MetaDefenderTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ MetaDefender's free API has limits, and is licensed for personal use
      </p>
      {/* The 4,000 is ours and not theirs, and the sentence has to say so.
          OPSWAT state only "a limited number of API calls per day"; the number
          is in budget.go, where we chose it. Printing it as their published
          limit attributes a figure to them they have never given, which is the
          same error as the invented quote above in a quieter form. */}
      <p>
        OPSWAT give no figure for the free tier — their API documentation says only “a limited number of API
        calls per day” — so <strong>this instance caps itself at 4,000 lookups a day</strong> and does not
        throttle them by the minute, because OPSWAT do not throttle single hash lookups. Go Help Desk asks
        once for each quarantined attachment, stores the answer and asks again only when the interval below
        has passed. Lookups beyond the cap show as <em>not checked</em> — never as clean.
      </p>
      <p>
        OPSWAT state that free access to their services is licensed “solely for Your personal use”. There is
        no carve-out for an organization. This feature only looks hashes up; it never uploads a file. A help
        desk is not a personal use of the service, so read their terms against your own deployment before
        switching this on; OPSWAT sell paid MetaDefender Cloud plans for organizational use.
      </p>
      <p className="flex flex-wrap gap-x-4">
        <TermsLink href="https://www.opswat.com/legal/terms-of-service">
          OPSWAT Terms of Service ↗
        </TermsLink>
        <TermsLink href="https://docs.opswat.com/mdcloud">
          MetaDefender Cloud documentation ↗
        </TermsLink>
      </p>
    </>
  )
}

/* PolySwarm is the one an operator reaches for when their lawyer has rejected
   the other two, so the licensing half is the half that matters — and what
   makes their position good is an ABSENCE: no field-of-use clause anywhere in
   the grant.

   The grant itself is quoted, read live from https://polyswarm.io/terms rather
   than taken from #168, which asserts the absence without ever quoting the
   clause that would carry a restriction. Same rule as the OPSWAT paragraph
   above and for the same reason: an operator told they may use this
   commercially should be able to read the sentence that says so, and a quote
   nobody checked is how this feature already shipped one that did not exist.

   What is NOT in quotation marks is the absence, because an absence has no
   words. It is stated as our reading of their document, with its date beside
   it. */
function PolySwarmTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ PolySwarm's free tier is one lookup a minute, and its published terms are from 2018
      </p>
      <p>
        The free tier allows <strong>60 lookups an hour</strong> — one a minute, with no daily figure
        published. A ticket carrying ten quarantined attachments spends ten minutes of that allowance in a
        burst. Go Help Desk asks once for each quarantined attachment, stores the answer and asks again only
        when the interval below has passed. Lookups beyond the allowance show as <em>not checked</em> —
        never as clean.
      </p>
      <p>
        PolySwarm grant “a non-exclusive, non-sublicenseable, non-transferable, revocable license to access
        and use the PolySwarm Products strictly in accordance with these Terms”, and attach no commercial-use
        restriction to it — no non-commercial, personal-use or internal-use clause appears anywhere in their
        terms. That makes them the only one of these four an organization can use without a licensing
        argument. The caveat is the date: their terms are dated 30 April 2018 and predate the API this
        instance calls, so the absence may be their policy or may be staleness. Read them before relying on
        it.
      </p>
      <p>
        <TermsLink href="https://polyswarm.io/terms">PolySwarm Terms of Service ↗</TermsLink>
      </p>
    </>
  )
}

/* CIRCL's warning is not a variation on the other three, and it must not read
   like one. Two facts are theirs alone:

     it publishes    /stats/top serves the most-queried hashes with filenames.
                     VirusTotal and OPSWAT log what they are asked; this one
                     puts it on a public page. An operator weighing whether
                     customers' file hashes may go there cannot get that from
                     shared copy.
     no terms        there is no terms-of-service page for hashlookup at all.
                     The licence is CC-BY, which permits commercial use, but
                     CIRCL nowhere say so in their own words — so that is
                     stated as the position it is rather than smoothed into a
                     permission. CC-BY asks for attribution, and the line it
                     asks for is here. */
function CIRCLTerms() {
  return (
    <>
      <p className="font-semibold">
        ⚠ CIRCL publishes what it is asked about, and has no terms of service to read
      </p>
      <p>
        hashlookup needs no key and no account, and it is the only one of the four that makes its traffic
        public: <span className="font-mono">/stats/top</span> serves the hundred most-queried hashes with
        their filenames and query counts, alongside the hashes it is most often asked about and has no
        record of. Each lookup is recorded against the caller's IP address and User-Agent. The other three
        log what they are asked; this one publishes it, so a customer's attachment hash — and whatever
        filename CIRCL holds for it — can appear on a public page.
      </p>
      <p>
        CIRCL state no rate limit, describing the service as “free and served as a best-effort basis”, so
        the <strong>300 lookups an hour</strong> this instance allows itself is a courtesy of ours rather
        than a limit of theirs.
      </p>
      <p>
        There is no terms-of-service page for hashlookup. The service declares its licence as CC-BY and the
        dataset is released as CC-BY-4.0, which permits commercial use — but CIRCL nowhere say so in their
        own words, which is why none of that is in quotation marks. CC-BY asks for attribution, and this
        instance gives it: Hash reputation data from CIRCL hashlookup, Computer Incident Response Center
        Luxembourg, CC-BY-4.0.
      </p>
      <p>
        <TermsLink href="https://hashlookup.circl.lu">CIRCL hashlookup ↗</TermsLink>
      </p>
    </>
  )
}

/**
 * One provider: its toggle, its key if it has one, and its warning.
 *
 * Grouped and named rather than laid out as three unrelated rows, because the
 * page now carries three password boxes all labelled "API key" and a reader
 * arriving at one — by tab, by screen reader, or by eye — has to be able to
 * tell whose account they are about to paste a key into.
 *
 * Nothing here pre-empts the server's rule that an enabled provider needs a
 * key. The server refuses that write with invalid_reputation_config and names
 * the provider; a second copy of the rule in here would be one more thing to
 * drift, and it would have to guess at a stored key it cannot read.
 */
function ReputationProviderRow({
  provider, enabled, setEnabled, keyStored, typedKey, setTypedKey, cleared, setCleared,
}: {
  provider: (typeof REPUTATION_PROVIDERS)[number]
  enabled: boolean
  setEnabled: (v: boolean) => void
  /** From the dump's "<setting>_set" flag — the only thing it says about a key. */
  keyStored: boolean
  typedKey: string
  setTypedKey: (v: string) => void
  cleared: boolean
  setCleared: (v: boolean) => void
}) {
  const [confirmClear, setConfirmClear] = useState(false)
  const fieldId = `reputation-key-${provider.id}`

  return (
    <div role="group" aria-label={provider.name} className="space-y-3 px-5 py-4">
      <div className="flex items-center justify-between gap-8">
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium text-gray-900">{provider.name}</div>
          <div className="mt-0.5 text-sm text-gray-500">{provider.description}</div>
        </div>
        <div className="shrink-0">
          <Toggle label={`Ask ${provider.name}`} checked={enabled} onChange={setEnabled} />
        </div>
      </div>

      {provider.keySetting && (
        <div className="space-y-2">
          <label htmlFor={fieldId} className="block text-sm font-medium text-gray-900">
            {provider.name} API key
          </label>
          <Input
            id={fieldId}
            type="password"
            autoComplete="off"
            className="max-w-lg font-mono text-sm"
            placeholder={cleared ? 'The stored key will be removed' : 'Paste a key to set or replace one'}
            value={typedKey}
            disabled={cleared}
            onChange={(e) => setTypedKey(e.target.value)}
          />
          <p className="text-xs text-gray-500">
            {keyStored ? 'A key is stored.' : 'No key is stored.'} Write-only: the server never sends a
            stored key back, so this field cannot be filled in for you. Leaving it blank leaves whatever is
            stored untouched — saving an empty field never removes a working key.
          </p>

          {cleared ? (
            <div className="flex items-center gap-3">
              <p className="text-sm text-amber-700">The stored key will be removed when you save.</p>
              <Button variant="outline" size="sm" onClick={() => setCleared(false)}>
                Keep the stored key
              </Button>
            </div>
          ) : (
            keyStored && (
              <Button variant="outline" size="sm" onClick={() => setConfirmClear(true)}>
                Remove the stored key
              </Button>
            )
          )}
          <ConfirmDialog
            open={confirmClear}
            onOpenChange={setConfirmClear}
            title={`Remove the stored ${provider.name} API key?`}
            description={`${provider.name} lookups stop until a new key is saved, and the save is refused outright while ${provider.name} is switched on above. Stored verdicts are kept, and nothing is removed until you save.`}
            confirmLabel="Remove the key"
            onConfirm={() => {
              setCleared(true)
              setConfirmClear(false)
            }}
          />
        </div>
      )}

      <ReputationWarning>
        <provider.Terms />
      </ReputationWarning>
    </div>
  )
}

function AttachmentsPanel({
  bool, str, strArr, has,
  setBool, setStr, setStrArr,
  apiKeys, setApiKey, clearedKeys, setClearKey,
  onSave, isPending, error, saved,
}: {
  bool: (k: string) => boolean
  str: (k: string) => string
  strArr: (k: string) => string[]
  has: (k: string) => boolean
  setBool: (k: string, v: boolean) => void
  setStr: (k: string, v: string) => void
  setStrArr: (k: string, v: string[]) => void
  apiKeys: Record<string, string>
  setApiKey: (k: string, v: string) => void
  clearedKeys: Record<string, boolean>
  setClearKey: (k: string, v: boolean) => void
  onSave: () => void
  isPending: boolean
  error: string
  saved: boolean
}) {
  const typesSet = has('attachment_allowed_types')
  const [typesText, setTypesText] = useState(
    (typesSet ? strArr('attachment_allowed_types') : DEFAULT_ALLOWED_TYPES).join('\n'),
  )

  const scanPolicy = str('attachment_scan_policy')
  const handling = str('attachment_infected_handling') || 'refuse'
  const mismatchHandling = str('attachment_mismatch_handling') || 'refuse'
  const refresh = str('attachment_reputation_refresh') || 'biweekly'

  function editTypes(v: string) {
    // Lowercased as it is typed, the way the tracking prefix is uppercased:
    // the server compares against strings.ToLower(filepath.Ext(name)) and
    // refuses an uppercase entry outright, so ".PDF" is only ever a refusal
    // waiting to happen.
    const lower = v.toLowerCase()
    setTypesText(lower)
    setStrArr('attachment_allowed_types', parseAllowedTypes(lower))
  }

  return (
    <div className="space-y-6">
      <Section title="Uploads">
        <div className="space-y-2 px-5 py-4">
          <label htmlFor="attachment-allowed-types" className="block text-sm font-medium text-gray-900">
            Allowed file types
          </label>
          <div className="text-sm text-gray-500">
            One extension per line, with the leading dot — <span className="font-mono">.pdf</span>. Anything
            else is refused at upload, before the file is read. An empty list means this instance accepts no
            attachments at all.
          </div>
          <textarea
            id="attachment-allowed-types"
            rows={9}
            className="w-full max-w-xs rounded border border-gray-300 p-2 font-mono text-sm"
            value={typesText}
            onChange={(e) => editTypes(e.target.value)}
          />
          {!typesSet && (
            <p className="text-xs text-gray-500">
              This instance has never set a list, so the built-in default is shown. It is saved only if you
              change it.
            </p>
          )}
        </div>

        {/* Here rather than beside the scanner settings, for the same reason
            the infected-handling select is beside those: a control belongs
            with the thing that produces the finding it acts on. This one acts
            on the content check every upload goes through, and the list above
            is what decides which of its findings are wrapped — a file is
            wrapped when what it turned out to be is not something this
            instance accepts.

            The wording deliberately shares the decision's shape with "When a
            scan finds malware" and none of its vocabulary. Nothing found
            anything wrong with a mislabelled file; it is named wrongly, the
            archive carries no password, and borrowing the other tier's words
            would say something false about what is stored and make both tiers
            mean less. */}
        <SettingRow
          label="When a file’s content contradicts its name"
          description="Every upload is compared against the extension it claims. A file that turns out to be a type this instance does not accept is either turned away or stored wrapped in a ZIP named suspicious-<crc32>.zip and flagged on the ticket. One whose real type is on the list above is flagged and stored under its own name whichever you choose."
        >
          <Select
            className="w-72"
            aria-label="When a file’s content contradicts its name"
            value={mismatchHandling}
            onChange={(e) => setStr('attachment_mismatch_handling', e.target.value)}
          >
            {/* Two values, and "quarantine" is not one of them however much it
                looks like it should be: it is the other setting's word, and
                the server refuses it rather than reading it as "wrap". */}
            <option value="refuse">Refuse the upload</option>
            <option value="wrap">Store it in a renamed archive</option>
          </Select>
        </SettingRow>
      </Section>

      <Section title="Malware scanning">
        <SettingRow
          label="Scan attachments for malware"
          description="Uploads are passed to the configured ClamAV daemon before they are stored."
        >
          <Select
            className="w-72"
            aria-label="Scan attachments for malware"
            value={scanPolicy}
            onChange={(e) => setStr('attachment_scan_policy', e.target.value)}
          >
            {/* Unset is not one of the three values: with no setting the
                server scans if a scanner address is configured and does not
                if one is not, and this page cannot see the address when it
                comes from the environment. Showing "Do not scan" for that
                would be a guess, and half the time a wrong one. */}
            {!has('attachment_scan_policy') && (
              <option value="" disabled>
                Not set — follows the scanner address
              </option>
            )}
            <option value="off">Do not scan</option>
            <option value="required">Scan, and refuse uploads while the scanner is down</option>
            <option value="permissive">Scan, but accept uploads while the scanner is down</option>
          </Select>
        </SettingRow>

        <SettingRow
          label="When a scan finds malware"
          description="Quarantined files are stored inside a password-protected ZIP (password: infected), labelled on the ticket, and confirmed before download. For an IT security team triaging a reported sample; an ordinary help desk should leave this on Refuse."
        >
          <Select
            className="w-72"
            aria-label="When a scan finds malware"
            value={handling}
            onChange={(e) => setStr('attachment_infected_handling', e.target.value)}
          >
            <option value="refuse">Refuse the upload</option>
            <option value="quarantine">Store it in a password-protected archive</option>
          </Select>
        </SettingRow>

        {/* The combination reads as though it does something, which is exactly
            why it has to be said: with no scan there is never an Infected
            verdict for this setting to act on. */}
        {scanPolicy === 'off' && handling === 'quarantine' && (
          <div className="px-5 py-3 text-sm text-gray-600">
            Nothing is scanned on this instance, so nothing is ever identified as malware and nothing is
            ever quarantined. Turn scanning on for this setting to do anything.
          </div>
        )}
      </Section>

      <Section title="Reputation lookup">
        {/* What the toggles below do, and — the part that has to be said out
            loud — what all of them off means. That is the state every instance
            upgrades into, it is a legitimate choice for an operator who cannot
            send customer file hashes anywhere, and "nothing configured" is the
            easiest thing in a settings page to render as though something were
            wrong. One statement, not a prompt. */}
        <div className="space-y-2 px-5 py-4 text-sm text-gray-500">
          <p>
            A quarantined attachment's SHA-256 can be looked up at these services. They answer different
            questions — how many engines flagged the file, whether a catalogue has it on record — so every
            service switched on is asked, and staff see the most serious answer with all of them one click
            away. A hash is not the file: nothing here ever uploads one.
          </p>
          <p>
            With none enabled, attachments are judged by this instance's own scanner alone.
          </p>
          {/* A link is not a lookup, and without this sentence an operator who
              switched VirusTotal off and still sees VirusTotal links will
              reasonably conclude the setting does not work. */}
          <p>
            A hash on a ticket always links to VirusTotal, whatever these toggles say. That link opens in the
            reader's own browser and this instance sends nothing there unless VirusTotal is switched on
            below; the toggles govern what this server discloses, not where staff may look.
          </p>
        </div>

        {REPUTATION_PROVIDERS.map((p) => (
          <ReputationProviderRow
            key={p.id}
            provider={p}
            enabled={bool(p.enabledSetting)}
            setEnabled={(v) => setBool(p.enabledSetting, v)}
            keyStored={p.keySetting ? bool(`${p.keySetting}_set`) : false}
            typedKey={p.keySetting ? (apiKeys[p.keySetting] ?? '') : ''}
            setTypedKey={(v) => { if (p.keySetting) setApiKey(p.keySetting, v) }}
            cleared={p.keySetting ? Boolean(clearedKeys[p.keySetting]) : false}
            setCleared={(v) => { if (p.keySetting) setClearKey(p.keySetting, v) }}
          />
        ))}

        <SettingRow
          label="Re-check stored verdicts"
          description="How long a clean, never-seen or unscanned verdict is trusted before it is looked up again. A detection is never re-checked — engines do not un-flag a file — and staff can ask again by hand once every seven days whatever this says."
        >
          <Select
            className="w-64"
            aria-label="Re-check stored verdicts"
            value={refresh}
            onChange={(e) => setStr('attachment_reputation_refresh', e.target.value)}
          >
            <option value="weekly">After 7 days</option>
            <option value="biweekly">After 14 days</option>
            <option value="monthly">After 30 days</option>
            <option value="quarterly">After 90 days</option>
            <option value="never">Never re-check automatically</option>
          </Select>
        </SettingRow>
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

  /* The reputation API keys are write-only, so none of them is ever in `local`
     and none travels with an ordinary save. One is sent only when the operator
     typed it or asked for the stored one to be removed — three blank fields
     PATCHed as "" would wipe three working keys on an unrelated change, and
     nothing would report it: the lookups would simply stop.

     Keyed by settings key rather than held as one value, which is the whole
     point of the change: a shared field would paste one service's key into
     another's account. */
  const [apiKeys, setApiKeys] = useState<Record<string, string>>({})
  const [clearedKeys, setClearedKeys] = useState<Record<string, boolean>>({})

  const { data: settings, isLoading } = useQuery({
    queryKey: ['admin', 'settings'],
    queryFn: getSettings,
  })

  /* The effects below seed local form state from a react-query result. That is
     the cascading-render pattern react-hooks/set-state-in-effect warns about
     (https://react.dev/learn/you-might-not-need-an-effect). Suppressed here,
     not refactored: switching the lint gate on should not also change how the
     admin forms behave. TODO: derive the state, or remount the form with a key.
     eslint-disable-next-line is not enough — the rule reports at each setState
     call, so the whole effect is wrapped. */
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    if (settings) setLocal(settings)
  }, [settings])
  /* eslint-enable react-hooks/set-state-in-effect */

  const saveMutation = useMutation({
    mutationFn: () => {
      /* The dump's "<key>_set" entries are the server describing secrets it
         will not send, not settings. Sending one back writes a settings row by
         that name which nothing ever reads. */
      const patch: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(local)) {
        if (!k.endsWith('_set')) patch[k] = v
      }
      for (const p of REPUTATION_PROVIDERS) {
        if (!p.keySetting) continue
        if (clearedKeys[p.keySetting]) patch[p.keySetting] = ''
        else if ((apiKeys[p.keySetting] ?? '').trim() !== '') {
          patch[p.keySetting] = apiKeys[p.keySetting].trim()
        }
      }
      return updateSettings(patch)
    },
    onSuccess: () => {
      setSaved(true)
      setSaveError('')
      // Only on success: a refused save leaves every edit where it was,
      // including a key typed but not yet accepted.
      setApiKeys({})
      setClearedKeys({})
      setTimeout(() => setSaved(false), 2500)
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
      qc.invalidateQueries({ queryKey: ['site-config'] })
    },
    onError: (err) => setSaveError(extractError(err)),
  })

  function bool(key: string) { return Boolean(local[key]) }
  // Whether this instance has ever stored a value for the key, as opposed to
  // what the value is. The difference matters where an unset setting does not
  // mean the zero value — see the allowed attachment types.
  function has(key: string) { return key in local }
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
  function setStrArr(key: string, v: string[]) { setLocal((s) => ({ ...s, [key]: v })) }
  function toggleStrArr(key: string, item: string, checked: boolean) {
    setLocal((s) => {
      const current = strArr(key)
      const next = checked ? [...new Set([...current, item])] : current.filter((x) => x !== item)
      return { ...s, [key]: next }
    })
  }

  function setApiKey(key: string, v: string) { setApiKeys((s) => ({ ...s, [key]: v })) }
  function setClearKey(key: string, v: boolean) { setClearedKeys((s) => ({ ...s, [key]: v })) }

  const panelProps = {
    bool, num, str, strArr, has,
    setBool, setNum, setStr, setStrArr, toggleStrArr,
    apiKeys, setApiKey, clearedKeys, setClearKey,
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
          {activeTab === 'attachments' && <AttachmentsPanel {...panelProps} />}
        </div>
      </div>
    </Layout>
  )
}
