import { AttachmentSizeLimitSection } from '@/components/admin/AttachmentSizeLimitSection'
import { attachmentLimits } from '@/pages/admin/settings/sections/attachmentLimits'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

// The per-file limit section (#673), fed from the draft. Its guest figure is
// the stored one, which is what the page shows and the server clamps to.
export function AttachmentSizeSection({ draft }: { draft: SettingsDraft }) {
  const { has, num, setNum, str } = draft
  const { guestMB } = attachmentLimits(draft)
  return (
    <AttachmentSizeLimitSection
      value={has('attachment_max_mb') ? num('attachment_max_mb') : undefined}
      onChange={(mb) => setNum('attachment_max_mb', mb)}
      scanning={str('attachment_scan_policy') !== 'off'}
      guestMB={guestMB}
    />
  )
}
