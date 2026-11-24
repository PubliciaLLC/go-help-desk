import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import { useEffect, useState } from 'react'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { EmailStatusNote } from '@/pages/admin/settings/primitives'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function RegistrationSection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool, strArr, setStrArr } = draft
  const [confirmOpenReg, setConfirmOpenReg] = useState(false)
  const domainsFromParent = strArr('allowed_email_domains').join('\n')
  const [domainsText, setDomainsText] = useState(domainsFromParent)
  useEffect(() => { setDomainsText(domainsFromParent) }, [domainsFromParent])

  const domainsLocked = strArr('allowed_email_domains').length > 0
  return (
    <Section title="Registration">
      <SettingRow
        label="Allow self-service signup"
        description="Let users create their own accounts without an admin invitation. Requires email verification."
      >
        <Toggle checked={bool('self_signup_enabled')} onChange={(v) => setBool('self_signup_enabled', v)} />
      </SettingRow>
      <EmailStatusNote on={bool('self_signup_enabled')} />
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
  )
}
