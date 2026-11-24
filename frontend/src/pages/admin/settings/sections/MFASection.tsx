import { Section } from '@/components/admin/Section'
import { SettingRow, Toggle } from '@/pages/admin/settings/primitives'
import type { SettingsDraft } from '@/pages/admin/settings/useSettingsDraft'

export function MFASection({ draft }: { draft: SettingsDraft }) {
  const { bool, setBool, strArr, toggleStrArr } = draft
  return (
    <Section title="Multi-factor authentication">
      <SettingRow
        label="Enable MFA"
        description="Allow users to opt in to TOTP (Google Authenticator, Authy, 1Password). Users can enable MFA from their profile; enrolled users are prompted for a one-time code at each sign-in."
      >
        <Toggle checked={bool('mfa_enabled')} onChange={(v) => setBool('mfa_enabled', v)} />
      </SettingRow>
      {bool('mfa_enabled') && (
        <SettingRow
          label="Require MFA for roles"
          description="Users in the selected roles must enroll in MFA to sign in. Unenrolled users are forced through setup on their next login; leave all unchecked to keep MFA opt-in."
        >
          <div className="flex gap-4">
            {(['admin', 'staff', 'user'] as const).map((r) => {
              // A role that must use a passkey is MFA-enforced by
              // implication (#616). Shown checked and dimmed; the stored
              // list is not touched, so unticking the passkey requirement
              // gives the role back whatever it was set to before.
              const viaPasskey = strArr('passkey_required_roles').includes(r)
              return (
                <label
                  key={r}
                  className={`flex items-center gap-1.5 select-none ${viaPasskey ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'}`}
                  title={viaPasskey ? 'Required because this role must use a passkey' : undefined}
                >
                  <input
                    type="checkbox"
                    className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                    checked={viaPasskey || strArr('mfa_enforced_roles').includes(r)}
                    disabled={viaPasskey}
                    onChange={(e) => toggleStrArr('mfa_enforced_roles', r, e.target.checked)}
                  />
                  <span className="text-sm capitalize text-gray-700">{r}</span>
                </label>
              )
            })}
          </div>
        </SettingRow>
      )}
      {bool('mfa_enabled') && (
        <SettingRow
          label="Require passkeys for roles"
          description="Users in the selected roles must finish every sign-in with a passkey. An authenticator app code still proves a factor, but is not enough on its own: a user with no passkey registers one right after signing in, and a single sign-on user is let in only when the identity provider reports a passkey sign-in (Microsoft Entra reports it; other providers are not recognised and ask for the passkey here). Applies at each person's next sign-in; sessions already open are not ended. These roles are also required to use MFA. While passkeys are unavailable on this server (BASE_URL) the requirement is paused and authenticator codes are accepted."
        >
          <div className="flex gap-4">
            {(['admin', 'staff', 'user'] as const).map((r) => (
              <label key={r} className="flex items-center gap-1.5 cursor-pointer select-none">
                <input
                  type="checkbox"
                  className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  aria-label={`Require passkeys for ${r}`}
                  checked={strArr('passkey_required_roles').includes(r)}
                  onChange={(e) => toggleStrArr('passkey_required_roles', r, e.target.checked)}
                />
                <span className="text-sm capitalize text-gray-700">{r}</span>
              </label>
            ))}
          </div>
        </SettingRow>
      )}
    </Section>
  )
}
