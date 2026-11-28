import { shownAttachmentMaxMB } from '@/lib/attachmentLimit'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

/**
 * The attachment limits as the Attachments and Guest Access pages show them (#673).
 * The per-file limit is the draft when it is an integer 1 to 100, else 25.
 * An unsaved guest value is shown and saved as min(draft, overallMB), so the
 * page never shows, and a save never sends, a guest figure the server would refuse.
 */
export function attachmentLimits(draft: SettingsDraft) {
  const { has, num } = draft
  const overallMB = shownAttachmentMaxMB(has('attachment_max_mb') ? num('attachment_max_mb') : undefined)
  const guestDraft = num('guest_attachment_max_mb')
  const guestDirty = has('guest_attachment_max_mb')
    && JSON.stringify(draft.stored?.guest_attachment_max_mb) !== JSON.stringify(guestDraft)
  const guestValue = has('guest_attachment_max_mb')
    ? (guestDirty ? Math.min(guestDraft, overallMB) : guestDraft)
    : undefined
  // The guest reader's rule: none, or outside 1 to 100, is 5.
  const guestMB = guestValue !== undefined && guestValue >= 1 && guestValue <= 100 ? guestValue : 5
  return { overallMB, guestValue, guestMB }
}
