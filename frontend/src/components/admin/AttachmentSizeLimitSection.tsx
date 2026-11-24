import { BoundedNumberInput } from '@/components/admin/BoundedNumberInput'
import { Section } from '@/components/admin/Section'
import {
  ATTACHMENT_MAX_MB_DEFAULT,
  ATTACHMENT_MAX_MB_MAX,
  ATTACHMENT_MAX_MB_MIN,
  shownAttachmentMaxMB,
} from '@/lib/attachmentLimit'

// The largest file anyone may attach (#673): 1 to 100 MB, 25 by default.
//
// It takes only what it shows, and reads no query and no page state, so moving
// it to another page is moving one line of JSX. `value` is the stored
// attachment_max_mb, absent when unset; `guestMB` is the guest's own limit as
// the page shows it (see SettingsPage for the unsaved-draft rule).

export function AttachmentSizeLimitSection({ value, onChange, scanning, guestMB }: {
  value?: number
  onChange: (mb: number) => void
  scanning: boolean
  guestMB: number
}) {
  const shown = shownAttachmentMaxMB(value)
  const guestHeld = Math.min(guestMB, shown)

  return (
    <Section title="File size">
      <div className="space-y-2 px-5 py-4">
        <div className="flex flex-wrap items-end gap-2">
          <BoundedNumberInput
            label="Largest file anyone may attach"
            hint="1 to 100 MB. Default 25."
            min={ATTACHMENT_MAX_MB_MIN}
            max={ATTACHMENT_MAX_MB_MAX}
            value={shown}
            onChange={onChange}
          />
          <span className="pb-6 text-sm text-gray-500">MB</span>
        </div>
        <p className="text-sm text-gray-500">
          Applies to every upload: staff, requesters and guests. Guests are held to the lower of this and their own limit.
        </p>
        <p data-testid="guest-limit-line" className="text-sm text-gray-500">
          Guests: up to {guestHeld} MB.
          {guestMB > shown && ` Their own limit is ${guestMB} MB; this one is lower, so it applies.`}
        </p>
        {scanning && shown > ATTACHMENT_MAX_MB_DEFAULT && (
          <p className="text-xs text-gray-500">
            Your virus scanner must accept files this large. clamd 0.105 and later accept up to 100 MB unless
            StreamMaxLength or MaxFileSize was lowered; an older clamd stops at 25 MB. A file the scanner cannot
            take is refused while scanning is required, and stored unscanned while it is permissive.
          </p>
        )}
      </div>
    </Section>
  )
}
