import { useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deleteSiteIcon, getSiteIcon, setSiteIconBackground, uploadSiteIcon } from '@/api/admin'
import type { SiteIconStatus } from '@/api/admin'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { SettingRow, Section } from '@/pages/admin/settings/primitives'
import { cn } from '@/lib/utils'

// The site icon (#708): one square image the administrator uploads, and the
// files the server makes from it. Unlike the rest of the Branding page this has
// no draft: each action is its own request and takes effect at once, as the
// logo does.

const ICON_KEY = ['admin', 'site-icon']
const DEFAULT_BACKGROUND = '#ffffff'
const HEX = /^#[0-9a-fA-F]{6}$/

// How each generated file is drawn: the caption that names it, the CSS pixels
// it is shown at, and the frame a device puts around it. Keyed by the file
// name the server lists; a name not here is not previewed.
interface Preview {
  caption: string
  size: number
  frame: string
  /** Transparent on purpose, so the checkerboard behind it is the point. */
  checker?: boolean
}
const PREVIEWS: Record<string, Preview> = {
  'favicon.ico': { caption: 'favicon.ico, 16, 32 and 48', size: 48, frame: '', checker: true },
  'favicon-16.png': { caption: 'Browser tab, 16 × 16', size: 16, frame: '', checker: true },
  'favicon-32.png': { caption: 'Browser tab, 32 × 32', size: 32, frame: '', checker: true },
  'apple-touch-icon.png': { caption: 'iPhone and iPad, 180 × 180', size: 90, frame: 'rounded-[22%]' },
  'icon-192.png': { caption: 'Android and desktop, 192 × 192', size: 96, frame: '' },
  'icon-512.png': { caption: 'Android and desktop, 512 × 512', size: 96, frame: '' },
  // Android crops this one to a circle, a squircle or a rounded square. The
  // circle is the harshest, so it is what the administrator sees.
  'icon-maskable-512.png': { caption: 'Android, masked, 512 × 512', size: 96, frame: 'rounded-full' },
}

const CHECKERBOARD = 'conic-gradient(#e5e7eb 25%, #ffffff 0 50%, #e5e7eb 0 75%, #ffffff 0) 0 0 / 8px 8px'

export function IconSection() {
  const qc = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)
  // What the administrator typed; null until they do, so the field follows the
  // stored colour (after an upload, an apply, or when the page first loads).
  const [typed, setTyped] = useState<string | null>(null)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [removeError, setRemoveError] = useState('')
  const [confirmRemove, setConfirmRemove] = useState(false)

  const { data: status, isPending: loading, error: loadError } = useQuery({
    queryKey: ICON_KEY,
    queryFn: getSiteIcon,
  })
  const set = status?.set === true
  const stored = status?.background ?? DEFAULT_BACKGROUND
  const value = typed ?? stored
  const valid = HEX.test(value)
  const changed = valid && value.toLowerCase() !== stored.toLowerCase()
  // The colour input only ever holds a #rrggbb: it turns anything else black.
  const pickerValue = valid ? value.toLowerCase() : stored

  function saved(next: SiteIconStatus, text: string) {
    qc.setQueryData(ICON_KEY, next)
    qc.invalidateQueries({ queryKey: ICON_KEY })
    setTyped(null)
    setMessage(text)
  }

  const upload = useMutation({
    mutationFn: (file: File) => uploadSiteIcon(file, value),
    onSuccess: (next) => saved(next, 'Icon saved. Reload the page to see it in this tab.'),
    onError: (err) => setError(extractError(err)),
    onSettled: () => {
      if (fileInputRef.current) fileInputRef.current.value = ''
    },
  })
  const recolour = useMutation({
    mutationFn: () => setSiteIconBackground(value),
    onSuccess: (next) => saved(next, 'Background colour applied.'),
    onError: (err) => setError(extractError(err)),
  })
  const remove = useMutation({
    mutationFn: deleteSiteIcon,
    onSuccess: () => {
      qc.setQueryData<SiteIconStatus>(ICON_KEY, (old) => ({
        set: false,
        background: old?.background ?? DEFAULT_BACKGROUND,
        version: '',
        source_size: 0,
        updated_at: null,
        files: [],
      }))
      qc.invalidateQueries({ queryKey: ICON_KEY })
      setTyped(null)
      setMessage('Icon removed.')
      setConfirmRemove(false)
    },
    onError: (err) => setRemoveError(extractError(err)),
  })
  const busy = upload.isPending || recolour.isPending || remove.isPending

  function begin() {
    setError('')
    setMessage('')
  }
  function handleFile(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    begin()
    upload.mutate(file)
  }

  if (loading) {
    return (
      <Section title="Icon">
        <p className="px-5 py-4 text-sm text-gray-500">Loading…</p>
      </Section>
    )
  }

  return (
    <Section title="Icon">
      <div className="px-5 py-4 space-y-3">
        <div>
          <div className="text-sm text-gray-500">
            The icon in the browser tab, and on a phone or computer&apos;s home screen when someone adds this site
            to it. Upload one square PNG, JPG or GIF, at least 512 × 512 px and at most 5 MB; the server makes
            every size from it.
          </div>
          <div className="mt-0.5 text-sm text-gray-500">SVG is not accepted, for the same reason as the logo.</div>
        </div>

        {!set && <p className="text-sm text-gray-900">No icon uploaded. The built-in icon is used.</p>}

        {set && status && (
          <div role="group" aria-label="Generated icons" className="flex flex-wrap gap-4">
            {status.files.map((f) => {
              const p = PREVIEWS[f.name]
              if (!p) return null
              return (
                <figure key={f.name} className="flex w-28 flex-col items-center gap-1.5 text-center">
                  <div
                    className={cn('shrink-0 overflow-hidden', p.frame)}
                    style={{
                      width: p.size,
                      height: p.size,
                      ...(p.checker ? { background: CHECKERBOARD } : {}),
                    }}
                  >
                    <img src={f.url} alt="" width={p.size} height={p.size} />
                  </div>
                  <figcaption className="text-xs text-gray-500">{p.caption}</figcaption>
                </figure>
              )
            })}
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => fileInputRef.current?.click()} disabled={busy || !valid}>
            {upload.isPending ? 'Uploading…' : set ? 'Replace icon' : 'Upload icon'}
          </Button>
          {set && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                begin()
                setRemoveError('')
                setConfirmRemove(true)
              }}
              disabled={busy}
            >
              {remove.isPending ? 'Removing…' : 'Remove icon'}
            </Button>
          )}
          <input
            ref={fileInputRef}
            type="file"
            accept=".png,.jpg,.jpeg,.gif"
            className="hidden"
            onChange={handleFile}
          />
        </div>

        {message && (
          <p role="status" className="text-sm text-green-600">
            {message}
          </p>
        )}
        {(error || loadError) && (
          <p role="alert" className="text-sm text-red-600">
            {error || extractError(loadError)}
          </p>
        )}
      </div>

      <SettingRow
        label="Home screen background"
        description="Fills the transparent parts of the home screen icons; iPhones would otherwise fill them with black. The browser tab icon stays transparent."
      >
        <div className="space-y-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="color"
              aria-label="Home screen background colour"
              value={pickerValue}
              onChange={(e) => setTyped(e.target.value)}
              disabled={busy}
              className="h-10 w-12 shrink-0 cursor-pointer rounded-md border border-input bg-background p-1 disabled:cursor-not-allowed disabled:opacity-50"
            />
            <Input
              className="w-28 font-mono"
              aria-label="Home screen background colour, hex"
              aria-invalid={!valid}
              spellCheck={false}
              autoComplete="off"
              value={value}
              onChange={(e) => setTyped(e.target.value)}
              disabled={busy}
            />
            {set && (
              <Button
                variant="outline"
                size="sm"
                disabled={!changed || busy}
                onClick={() => {
                  begin()
                  recolour.mutate()
                }}
              >
                {recolour.isPending ? 'Applying…' : 'Apply colour'}
              </Button>
            )}
          </div>
          {!valid && (
            <p role="alert" className="text-sm text-red-600">
              Use a colour such as #ffffff.
            </p>
          )}
          {!set && valid && <p className="text-sm text-gray-500">Used when you upload an icon.</p>}
        </div>
      </SettingRow>

      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={setConfirmRemove}
        title="Remove the icon?"
        description="The browser tab and new home screen icons go back to the built-in icon. Icons already on someone's home screen may keep the old picture."
        confirmLabel="Remove icon"
        isPending={remove.isPending}
        error={removeError}
        onConfirm={() => remove.mutate()}
      />
    </Section>
  )
}
