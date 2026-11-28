import { Section } from '@/components/admin/Section'
import { SettingRow } from '@/pages/admin/settings/primitives'
import { useId, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { deleteLogo, getSiteConfig, uploadLogo } from '@/api/admin'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

// Home screens show about this many characters of a name before cutting it
// short. A display limit that varies by device, so a note and not a refusal;
// the hard limit is the server's (30).
const SHORT_NAME_SOFT_LIMIT = 12
const SHORT_NAME_MAX = 30

const runes = (s: string) => [...s].length

export function IdentitySection({ draft }: { draft: SettingsDraft }) {
  const { str, setStr } = draft
  // The checkbox is not a setting: the setting is the name, and an empty one
  // means "use the site name". Ticked when a name is stored; the page shows a
  // loaded draft, so the first render already knows.
  const [useShortName, setUseShortName] = useState(() => str('site_short_name').trim() !== '')
  const shortName = str('site_short_name')
  // The server trims, then counts characters. So does everything here: the
  // counter, the note and the over-limit mark all read this one number. There
  // is no maxLength on the field, because that counts UTF-16 units and would
  // stop an emoji name the server accepts.
  const shortLen = runes(shortName.trim())
  const longName = shortLen > SHORT_NAME_SOFT_LIMIT
  const overLimit = shortLen > SHORT_NAME_MAX
  const counterId = useId()
  const noteId = useId()
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
    <Section title="Identity">
      <SettingRow
        label="Site name"
        description="Displayed in the sidebar header and browser tab when no logo is set. Also the name used when someone installs the site or adds it to a home screen, unless you set a different home screen name below."
      >
        <Input
          className="w-56"
          placeholder="Go Help Desk"
          value={str('site_name')}
          onChange={(e) => setStr('site_name', e.target.value)}
        />
      </SettingRow>

      <div className="px-5 py-4">
        <div className="flex items-center gap-2">
          <input
            type="checkbox"
            id="use-short-name"
            className="h-4 w-4 rounded border-border-strong"
            checked={useShortName}
            onChange={(e) => {
              setUseShortName(e.target.checked)
              if (!e.target.checked) setStr('site_short_name', '')
            }}
          />
          <label htmlFor="use-short-name" className="text-sm font-medium text-fg">
            Use a different name on the home screen
          </label>
        </div>
        {!useShortName && (
          <div className="mt-1 text-sm text-fg-faint">The home screen uses the site name.</div>
        )}
      </div>
      {useShortName && (
        <SettingRow
          label="Home screen name"
          description="Shown under the icon when someone adds this site to a phone's home screen."
        >
          <div className="space-y-1.5">
            <Input
              className="w-56"
              aria-label="Home screen name"
              aria-describedby={longName ? `${counterId} ${noteId}` : counterId}
              aria-invalid={overLimit}
              placeholder={str('site_name') || 'Go Help Desk'}
              value={shortName}
              onChange={(e) => setStr('site_short_name', e.target.value)}
            />
            <div id={counterId} className={overLimit ? 'text-xs text-danger-fg' : 'text-xs text-fg-faint'}>
              {shortLen}/{SHORT_NAME_MAX}
            </div>
            {longName && (
              <p id={noteId} role="note" className="max-w-56 text-sm text-warning-fg">
                Most home screens show about {SHORT_NAME_SOFT_LIMIT} characters and cut longer names short.
              </p>
            )}
          </div>
        </SettingRow>
      )}

      <div className="px-5 py-4 space-y-3">
        <div>
          <div className="text-sm font-medium text-fg">Logo</div>
          <div className="mt-0.5 text-sm text-fg-faint">
            Target size: <span className="font-medium">320 × 64 px</span> · PNG, JPG, or GIF · Max 2 MB.
            Larger images are scaled proportionally to fit. The logo replaces the site name in the sidebar.
          </div>
          {/* One sentence, not a warning: an operator who used an SVG logo
              and finds the option gone should know it was deliberate. */}
          <div className="mt-0.5 text-sm text-fg-faint">
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
              <p className="text-sm text-warning-fg">
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

        {logoError && <p role="alert" className="text-sm text-danger-fg">{logoError}</p>}
      </div>
    </Section>
  )
}
