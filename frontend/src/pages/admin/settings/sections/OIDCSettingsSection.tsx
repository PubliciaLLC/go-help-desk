import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getOIDCConfig, saveOIDCConfig } from '@/api/admin'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { Section } from '@/components/admin/Section'
import { Toggle } from '@/pages/admin/settings/primitives'

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
    return <div className="py-4 text-center text-sm text-fg-faint">Loading…</div>
  }

  const redirectURL = oidc?.redirect_url ?? ''
  const configured = oidc?.configured ?? false

  return (
    <div className="space-y-4 px-5 py-4">
      <div className="flex flex-wrap items-center gap-3">
        <span className={cn(
          'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
          configured ? 'bg-success-soft text-success-fg-strong' : 'bg-muted text-fg-muted'
        )}>
          {configured ? 'Configured' : 'Not configured'}
        </span>

        {configured && (
          <span className="text-xs text-fg-faint">
            Redirect URL:{' '}
            <button
              type="button"
              className="font-mono text-left text-link underline decoration-dotted hover:decoration-solid wrap-anywhere"
              onClick={() => navigator.clipboard.writeText(redirectURL)}
              title="Copy to clipboard"
            >
              {redirectURL}
            </button>
          </span>
        )}
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-fg-soft">
          Enable OIDC login
        </label>
        <Toggle checked={enabled} onChange={setEnabled} />
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-fg-soft">
          Issuer URL
        </label>
        <Input
          placeholder="https://login.example.com"
          value={issuerURL}
          onChange={(e) => setIssuerURL(e.target.value)}
          className="max-w-lg font-mono text-sm"
        />
        <p className="text-xs text-fg-faint">
          The OpenID Connect issuer URL for your identity provider.
        </p>
      </div>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-fg-soft">
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
        <label className="block text-sm font-medium text-fg-soft">
          Client secret
          {configured && !clientSecret && (
            <span className="ml-2 text-xs font-normal text-fg-faint">
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
          <label className="block text-sm font-medium text-fg-soft">
            Redirect URL
          </label>
          <div className="flex items-center gap-2">
            <div className="min-w-0 w-full max-w-lg rounded border border-border bg-subtle px-3 py-2 font-mono text-sm text-fg-muted wrap-anywhere">
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
          <p className="text-xs text-fg-faint">
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
          <p role="alert" className="text-sm text-danger-fg">{saveError}</p>
        )}

        {saved && (
          <p className="text-sm text-success-fg">
            OIDC config saved.
          </p>
        )}
      </div>
    </div>
  )
}

// Reads nothing from the draft: its own query and save (#674).
export function OIDCSettingsSection() {
  return (
    <Section title="OIDC / OpenID Connect">
      <OIDCSection />
    </Section>
  )
}
