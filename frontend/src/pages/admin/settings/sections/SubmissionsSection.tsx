import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { EmailStatusNote } from '@/pages/admin/settings/primitives'
import { GuestSizeSlider } from '@/components/admin/GuestSizeSlider'
import { BoundedNumberInput } from '@/components/admin/BoundedNumberInput'
import { attachmentLimits } from '@/pages/admin/settings/sections/attachmentLimits'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function SubmissionsSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool, num, setNum, has } = draft
  const { overallMB, guestValue } = attachmentLimits(draft)
  // Why a guest level is unavailable, said in words and tied to its switch.
  const submissionOff = !bool('guest_submission_enabled')
  const typesOff = !bool('guests_show_types')
  const itemsWhy = submissionOff
    ? 'Unavailable while guest submission is off.'
    : typesOff
      ? 'Unavailable while Show Types to guests is off.'
      : undefined
  return (
    <Section title="Submissions">
      <SettingRow
        label="Guest submission"
        description="Allow unauthenticated users to submit a ticket using only their email address. They receive a tracking number to follow up without creating an account. Switching this off also suspends all active guest links until the setting is enabled again."
      >
        <Toggle checked={bool('guest_submission_enabled')} onChange={(v) => setBool('guest_submission_enabled', v)} />
      </SettingRow>
      <EmailStatusNote on={bool('guest_submission_enabled')} />
      {/* Dependent on guest submission (#445): shown under it, unavailable
          while it is off, and the server ignores it in that state too. */}
      <SettingRow
        label="Guests may attach files"
        description="Let a guest add files to their ticket from the link they were emailed. Off by default, and it only takes effect while guest submission is on. Uploads are scanned and held to the same rules as everyone else's, and are download-only."
      >
        <Toggle
          label="Guests may attach files"
          checked={bool('guest_attachments_enabled')}
          disabled={!bool('guest_submission_enabled')}
          onChange={(v) => setBool('guest_attachments_enabled', v)}
        />
      </SettingRow>
      {/* Dependent on guest submission, like the row above, and Items on Types
          (#711): the server computes the effective values, so the stored ones
          are kept while a toggle is unavailable. */}
      <SettingRow
        label="Show Types to guests"
        description="Show guests a Type selector under Category on the guest form, with the same descriptions. Off by default: guests choose a Category only. It only takes effect while guest submission is on, and it makes the names and descriptions of every active type public to anyone who can open the form."
      >
        <Toggle
          label="Show Types to guests"
          checked={bool('guests_show_types')}
          disabled={submissionOff}
          describedBy={submissionOff ? 'guests-show-types-why' : undefined}
          onChange={(v) => setBool('guests_show_types', v)}
        />
      </SettingRow>
      {submissionOff && (
        <p id="guests-show-types-why" className="px-5 pb-3 text-xs text-gray-500">Unavailable while guest submission is off.</p>
      )}
      <SettingRow
        label="Show Items to guests"
        description="Show guests an Item selector under Type on the guest form. Needs guest submission on and Show Types to guests, and makes the names and descriptions of every active item public too. This is separate from Show Items to registered users: with this on and that off, a registered user can sign out and read the item list."
      >
        <Toggle
          label="Show Items to guests"
          checked={bool('guests_show_items')}
          disabled={Boolean(itemsWhy)}
          describedBy={itemsWhy ? 'guests-show-items-why' : undefined}
          onChange={(v) => setBool('guests_show_items', v)}
        />
      </SettingRow>
      {itemsWhy && <p id="guests-show-items-why" className="px-5 pb-3 text-xs text-gray-500">{itemsWhy}</p>}
      <div className="border-t px-5 py-4 space-y-2">
        <div className="text-sm text-gray-500">
          The largest file a guest may attach. It can never exceed the per-file limit for every upload, {overallMB} MB, set under System → File Attachments.
        </div>
        <GuestSizeSlider
          value={guestValue}
          onChange={(v) => setNum('guest_attachment_max_mb', v)}
          instanceMaxMB={overallMB}
          disabled={!bool('guest_submission_enabled') || !bool('guest_attachments_enabled')}
        />
        {!bool('guest_submission_enabled') && (
          <p className="text-xs text-gray-500">Unavailable while guest submission is off.</p>
        )}
      </div>
      <div className="border-t px-5 py-4 space-y-4">
        <div className="text-sm text-gray-500">
          How much one guest can put on a ticket. The cap counts only files guests attached; staff files do not count
          toward it. The hourly figure is per guest link, so one guest does not use up another&apos;s.
        </div>
        <div className="flex flex-wrap gap-8">
          <BoundedNumberInput
            label="Most files a guest can attach to one ticket"
            hint="1 to 50. Default 5."
            min={1}
            max={50}
            value={has('guest_attachment_max_per_ticket') ? num('guest_attachment_max_per_ticket') : 5}
            onChange={(v) => setNum('guest_attachment_max_per_ticket', v)}
            disabled={!bool('guest_submission_enabled') || !bool('guest_attachments_enabled')}
          />
          <BoundedNumberInput
            label="Uploads per guest link per hour"
            hint="1 to 30. Default 3."
            min={1}
            max={30}
            value={has('guest_attachment_uploads_per_hour') ? num('guest_attachment_uploads_per_hour') : 3}
            onChange={(v) => setNum('guest_attachment_uploads_per_hour', v)}
            disabled={!bool('guest_submission_enabled') || !bool('guest_attachments_enabled')}
          />
        </div>
      </div>
    </Section>
  )
}
