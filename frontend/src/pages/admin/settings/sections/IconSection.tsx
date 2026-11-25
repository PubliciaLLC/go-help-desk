import { useId, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { deleteSiteIcon, getSiteIcon, setSiteIconBackground, uploadSiteIcon } from '@/api/admin'
import type { SiteIconStatus } from '@/api/admin'
import { extractError, extractErrorCode } from '@/api/client'
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

// A button that is busy or has nothing to do says so with aria-disabled and
// ignores the click, instead of becoming `disabled`: a disabled button cannot
// hold focus, so the keyboard user who just pressed it would be thrown to the
// top of the page.
const INERT = 'aria-disabled:cursor-not-allowed aria-disabled:opacity-50'

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
// The order here is the order shown, small to large, whatever order the server
// lists the files in.
const PREVIEWS: Record<string, Preview> = {
  'favicon-16.png': { caption: 'Browser tab, 16 × 16', size: 16, frame: '', checker: true },
  'favicon-32.png': { caption: 'Browser tab, 32 × 32', size: 32, frame: '', checker: true },
  'favicon.ico': { caption: 'favicon.ico, 16, 32 and 48', size: 48, frame: '', checker: true },
  'apple-touch-icon.png': { caption: 'iPhone and iPad, 180 × 180', size: 90, frame: 'rounded-[22%]' },
  // 64 and 96, so the two Android sizes are told apart by looking.
  'icon-192.png': { caption: 'Android and desktop, 192 × 192', size: 64, frame: '' },
  'icon-512.png': { caption: 'Android and desktop, 512 × 512', size: 96, frame: '' },
  // Android crops this one to a circle, a squircle or a rounded square. The
  // circle is the harshest, so it is what the administrator sees.
  'icon-maskable-512.png': { caption: 'Android, masked, 512 × 512', size: 96, frame: 'rounded-full' },
}

const PREVIEW_ORDER = Object.keys(PREVIEWS)
// The tallest thumbnail (96 px) plus its 1 px border either side: every
// thumbnail sits at the bottom of a box this high, so the captions line up.
const THUMB_AREA = 98

const CHECKERBOARD =
  'bg-[conic-gradient(#e5e7eb_25%,#ffffff_0_50%,#e5e7eb_0_75%,#ffffff_0)] bg-[length:8px_8px]'

export function IconSection() {
  const qc = useQueryClient()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const uploadRef = useRef<HTMLButtonElement>(null)
  const removeRef = useRef<HTMLButtonElement>(null)
  // Set when a removal succeeds: the button that opened the dialog is gone by
  // the time the dialog hands focus back, so focus goes to Upload icon instead.
  const focusUpload = useRef(false)
  // What the administrator typed; null until they do, so the field follows the
  // stored colour (after an upload, an apply, or when the page first loads).
  const [typed, setTyped] = useState<string | null>(null)
  // The hex hint is not shown on every keystroke: once the field is left, or
  // once the text is as long as a colour and still wrong.
  const [left, setLeft] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [removeError, setRemoveError] = useState('')
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const hintId = useId()
  const captionId = useId()

  const {
    data: status,
    isPending: loading,
    error: loadError,
    refetch,
  } = useQuery({
    queryKey: ICON_KEY,
    queryFn: getSiteIcon,
  })
  const set = status?.set === true
  const stored = status?.background ?? DEFAULT_BACKGROUND
  const value = typed ?? stored
  const valid = HEX.test(value)
  const showHint = !valid && (left || value.length === 7)
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
    onError: (err) => {
      setError(extractError(err))
      // Another administrator removed the icon: say so, and stop showing it.
      if (extractErrorCode(err) === 'no_icon') qc.invalidateQueries({ queryKey: ICON_KEY })
    },
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
      focusUpload.current = true
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
  function typeColour(v: string) {
    setTyped(v)
    setLeft(false)
  }
  // The dialog has no trigger element of its own, so left alone it would give
  // focus back to nothing. After a removal that is Upload icon; after Cancel
  // it is the Remove icon button that opened it.
  function afterDialog(e: Event) {
    e.preventDefault()
    const removed = focusUpload.current
    focusUpload.current = false
    ;(removed ? uploadRef : removeRef).current?.focus()
  }
  function retry() {
    setRetrying(true)
    void refetch().finally(() => setRetrying(false))
  }

  // The status could not be read, so what is stored is unknown: say that and
  // offer to try again, and offer nothing that would write. An upload from here
  // would send the fallback #ffffff over a stored icon's colour. A refetch that
  // fails while a status is already on screen leaves the status where it is.
  if (!status && (loadError || retrying)) {
    return (
      <Section title="Icon">
        <div className="px-5 py-4 space-y-3">
          {loadError ? (
            <p role="alert" className="text-sm text-red-600">
              {extractError(loadError)}
            </p>
          ) : (
            <p className="text-sm text-gray-500">Trying again…</p>
          )}
          <Button
            variant="outline"
            size="sm"
            className={INERT}
            aria-disabled={retrying}
            onClick={() => {
              if (!retrying) retry()
            }}
          >
            {retrying ? 'Retrying…' : 'Retry'}
          </Button>
        </div>
      </Section>
    )
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
          <div className="mt-0.5 text-sm text-gray-500">
            Icons already added to a home screen keep the old picture on iPhone and update later on Android.
          </div>
        </div>
      </div>

      {/* Above the previews, because it decides how four of them look. */}
      <SettingRow
        label="Home screen background"
        description="Fills the transparent parts of the home screen icons; iPhones would otherwise fill them with black. The browser tab icon stays transparent. This colour is also the browser's theme colour: mobile browser toolbars and the installed app's title bar take it, not only the icon fill."
      >
        <div className="space-y-1.5">
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="color"
              aria-label="Home screen background colour"
              value={pickerValue}
              onChange={(e) => typeColour(e.target.value)}
              disabled={busy}
              className="h-10 w-12 shrink-0 cursor-pointer rounded-md border border-input bg-background p-1 disabled:cursor-not-allowed disabled:opacity-50"
            />
            <Input
              className="w-28 font-mono"
              aria-label="Home screen background colour, hex"
              aria-invalid={showHint}
              aria-describedby={showHint ? hintId : undefined}
              spellCheck={false}
              autoComplete="off"
              value={value}
              onChange={(e) => typeColour(e.target.value)}
              onBlur={() => setLeft(true)}
              disabled={busy}
            />
            {set && (
              <Button
                variant="outline"
                size="sm"
                className={INERT}
                aria-disabled={!changed || busy}
                onClick={() => {
                  if (!changed || busy) return
                  begin()
                  recolour.mutate()
                }}
              >
                {recolour.isPending ? 'Applying…' : 'Apply colour'}
              </Button>
            )}
          </div>
          {showHint && (
            <p id={hintId} role="alert" className="text-sm text-red-600">
              Use a colour such as #ffffff.
            </p>
          )}
          {!set && valid && <p className="text-sm text-gray-500">Used when you upload an icon.</p>}
        </div>
      </SettingRow>

      <div className="px-5 py-4 space-y-3">
        {!set && <p className="text-sm text-gray-900">No icon uploaded. The built-in icon is used.</p>}

        {set && status && (
          <>
            <div
              role="group"
              aria-label="Generated icons"
              aria-describedby={captionId}
              className="flex flex-wrap gap-4"
            >
              {status.files
                .filter((f) => PREVIEWS[f.name])
                .sort((a, b) => PREVIEW_ORDER.indexOf(a.name) - PREVIEW_ORDER.indexOf(b.name))
                .map((f) => {
                  const p = PREVIEWS[f.name]
                  return (
                    <figure key={f.name} className="flex w-28 flex-col items-center gap-1.5 text-center">
                      <div className="flex items-end justify-center" style={{ height: THUMB_AREA }}>
                        {/* A hairline edge on every one: a white icon on the white card has none of its own. */}
                        <div
                          className={cn(
                            'box-content shrink-0 overflow-hidden border border-gray-300',
                            p.checker && CHECKERBOARD,
                            p.frame,
                          )}
                          style={{ width: p.size, height: p.size }}
                        >
                          <img src={f.url} alt="" width={p.size} height={p.size} />
                        </div>
                      </div>
                      <figcaption className="text-xs text-gray-500">{p.caption}</figcaption>
                    </figure>
                  )
                })}
            </div>
            <p id={captionId} className="text-xs text-gray-500">
              A checkerboard behind a preview shows which parts are transparent. &ldquo;Masked&rdquo; is the circle
              crop Android applies to home screen icons; some phones use another shape.
            </p>
          </>
        )}

        <div className="flex flex-wrap gap-2">
          <Button
            ref={uploadRef}
            variant="outline"
            size="sm"
            className={INERT}
            aria-disabled={busy || !valid}
            onClick={() => {
              if (!busy && valid) fileInputRef.current?.click()
            }}
          >
            {upload.isPending ? 'Uploading…' : set ? 'Replace icon' : 'Upload icon'}
          </Button>
          {set && (
            <Button
              ref={removeRef}
              variant="outline"
              size="sm"
              className={INERT}
              aria-disabled={busy}
              onClick={() => {
                if (busy) return
                begin()
                setRemoveError('')
                setConfirmRemove(true)
              }}
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

        {/* Always in the page, so a screen reader is already watching it when
            its text changes; a status mounted together with its text is
            missed by several of them. */}
        <div role="status" className="text-sm text-green-600">
          {message}
        </div>
        {error && (
          <p role="alert" className="text-sm text-red-600">
            {error}
          </p>
        )}
      </div>

      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={setConfirmRemove}
        title="Remove the icon?"
        description="The browser tab and new home screen icons go back to the built-in icon. Icons already on someone's home screen may keep the old picture."
        confirmLabel="Remove icon"
        isPending={remove.isPending}
        error={removeError}
        onCloseAutoFocus={afterDialog}
        onConfirm={() => remove.mutate()}
      />
    </Section>
  )
}
