import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getSAMLConfig, saveSAMLConfig } from '@/api/admin'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

// A value to read out to someone else, with a button that puts it on the
// clipboard. The result is said in a status line, which screen readers announce:
// "Copied" only once the write has gone through, and a short instruction when it
// has not (navigator.clipboard does not exist on a plain-http site that is not
// localhost, and a refused permission rejects).
function CopyValue({ label, value }: { label: string; value: string }) {
  const [result, setResult] = useState<'copied' | 'failed' | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  if (!value) return null
  async function copy() {
    clearTimeout(timer.current)
    try {
      await navigator.clipboard.writeText(value)
      setResult('copied')
    } catch {
      setResult('failed')
    }
    timer.current = setTimeout(() => setResult(null), 4000)
  }
  return (
    <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:gap-3">
      <span className="text-xs font-medium text-gray-500 sm:w-20 sm:shrink-0">{label}</span>
      <code className="min-w-0 flex-1 font-mono text-xs text-gray-700 wrap-anywhere">{value}</code>
      <div className="flex items-center gap-2">
        <Button type="button" variant="outline" size="sm" aria-label={`Copy ${label}`} onClick={copy}>
          Copy
        </Button>
        <span role="status" className={cn('text-xs', result === 'failed' ? 'text-red-600' : 'text-green-600')}>
          {result === 'copied' && 'Copied'}
          {result === 'failed' && 'Copy failed, select the text'}
        </span>
      </div>
    </div>
  )
}

function SAMLSection() {
  const qc = useQueryClient()
  const certFileRef = useRef<HTMLInputElement>(null)
  const keyFileRef = useRef<HTMLInputElement>(null)
  const metadataFileRef = useRef<HTMLInputElement>(null)

  const [metadataSource, setMetadataSource] = useState<'url' | 'xml'>('url')
  const [metadataURL, setMetadataURL] = useState('')
  const [metadataXML, setMetadataXML] = useState('')
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
      setMetadataXML(saml.metadata_xml)
      setMetadataSource(saml.metadata_xml ? 'xml' : 'url')
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
    // key_pem is only included when this session actually uploaded or typed
    // one: getSAMLConfig never returns the stored private key at all (see its
    // own comment), so the local keyPEM state starts blank and stays that way
    // until the admin replaces it. Sending it as "" whenever it was merely
    // left alone used to backfill from the stored value on the server side —
    // that backfill no longer happens (see #300), so doing so now would
    // clear the working key on every ordinary save that isn't also a key
    // rotation.
    mutationFn: () => saveSAMLConfig({
      // One source or the other, never both: the server refuses a save that
      // leaves both set, so the unchosen one is cleared explicitly.
      metadata_url: metadataSource === 'url' ? metadataURL : '',
      metadata_xml: metadataSource === 'xml' ? metadataXML : '',
      cert_pem: certPEM,
      ...(keyPEM ? { key_pem: keyPEM } : {}),
    }),
    onSuccess: (res) => {
      setSaved(true)
      setSaveError('')
      // The key is write-only: once saved it does not stay in the page, and the
      // next save leaves it out (the server keeps the stored one).
      setKeyPEM('')
      setWarning(res.warning ?? '')
      setTimeout(() => setSaved(false), 3000)
      qc.invalidateQueries({ queryKey: ['admin', 'saml'] })
      // The same save changed the saml_* settings the page's dump carries.
      qc.invalidateQueries({ queryKey: ['admin', 'settings'] })
    },
    onError: (err) => setSaveError(extractError(err)),
  })

  if (isLoading) return <div className="py-4 text-center text-sm text-gray-400">Loading…</div>

  return (
    <div className="space-y-4 px-5 py-4">
      <span className={cn(
        'inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium',
        saml?.configured ? 'bg-green-100 text-green-800' : 'bg-gray-100 text-gray-600'
      )}>
        {saml?.configured ? 'Configured' : 'Not configured'}
      </span>

      {/* What the identity provider is told about this server. Shown before
          SAML is configured too: the IdP is usually set up first, and these are
          what it asks for. */}
      <div className="space-y-2">
        <p className="text-sm font-medium text-gray-700">Give these to your identity provider</p>
        <CopyValue label="Entity ID" value={saml?.sp_metadata_url ?? ''} />
        <CopyValue label="ACS URL" value={saml?.sp_acs_url ?? ''} />
      </div>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium text-gray-700">IdP metadata</legend>
        <div className="flex items-center gap-4 text-sm text-gray-700">
          <label className="flex items-center gap-1.5">
            <input type="radio" name="saml-metadata-source" checked={metadataSource === 'url'}
              onChange={() => setMetadataSource('url')} />
            URL
          </label>
          <label className="flex items-center gap-1.5">
            <input type="radio" name="saml-metadata-source" checked={metadataSource === 'xml'}
              onChange={() => setMetadataSource('xml')} />
            XML
          </label>
        </div>
        {metadataSource === 'url' ? (
          <div className="space-y-1">
            <label htmlFor="saml-metadata-url" className="block text-sm text-gray-600">IdP metadata URL</label>
            <Input
              id="saml-metadata-url"
              placeholder="https://idp.example.com/saml/metadata"
              value={metadataURL}
              onChange={(e) => setMetadataURL(e.target.value)}
              className="max-w-lg font-mono text-sm"
            />
          </div>
        ) : (
          <div className="space-y-1">
            <label htmlFor="saml-metadata-xml" className="block text-sm text-gray-600">IdP metadata XML</label>
            <textarea
              id="saml-metadata-xml"
              rows={10}
              placeholder="<EntityDescriptor …>"
              className="w-full max-w-lg rounded border border-gray-300 p-2 font-mono text-xs text-gray-600"
              value={metadataXML}
              onChange={(e) => setMetadataXML(e.target.value)}
            />
            <div>
              <Button variant="outline" size="sm" onClick={() => metadataFileRef.current?.click()}>Upload .xml</Button>
            </div>
            <input ref={metadataFileRef} type="file" accept=".xml,application/xml,text/xml"
              aria-label="IdP metadata XML file" className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0]
                if (f) readFile(f, setMetadataXML)
                // So the same file can be chosen again after an edit.
                e.target.value = ''
              }} />
            <p className="max-w-lg text-xs text-gray-500">
              Paste the metadata XML, or upload the file your identity provider gave you. It is checked when you save,
              and shown here again afterwards. It is not refreshed: when your identity provider&apos;s signing
              certificate changes, paste its new metadata.
            </p>
          </div>
        )}
      </fieldset>

      <div className="space-y-1">
        <label className="block text-sm font-medium text-gray-700">
          SP certificate (PEM)
          {saml?.configured && !certPEM && <span className="ml-2 text-xs font-normal text-gray-400">already configured</span>}
        </label>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => certFileRef.current?.click()}>Upload .pem / .crt</Button>
          {certPEM && <span title={certPEM} className="text-xs text-gray-500 truncate max-w-xs font-mono">{certPEM.split('\n')[0]}…</span>}
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
        {saveError && <p role="alert" className="text-sm text-red-600">{saveError}</p>}
        {saved && !warning && <p className="text-sm text-green-600">SAML config saved.</p>}
        {warning && <p className="text-sm text-amber-600">{warning}</p>}
      </div>
    </div>
  )
}

export function SAMLSettingsSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool } = draft
  return (
    <Section title="SAML">
      <div>
        <div className="bg-gray-50">
          <div className="px-5 pt-3 pb-0">
            <p className="text-xs font-medium uppercase tracking-wider text-gray-400">SAML configuration</p>
          </div>
          <SAMLSection />
        </div>
        <div className="border-t">
          <SettingRow
            label="Require SAML for staff and users"
            description='SAML is switched on by "Save SAML config" above, not by this switch. This switch only turns off password sign-in for staff and users (administrators keep password sign-in). Saved by Save changes, not by Save SAML config.'
          >
            <Toggle
              label="Require SAML for staff and users"
              checked={bool('saml_enabled')}
              onChange={(v) => setBool('saml_enabled', v)}
            />
          </SettingRow>
        </div>
      </div>
    </Section>
  )
}
