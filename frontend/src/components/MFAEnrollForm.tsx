import { useEffect, useState } from 'react'
import { enrollMFAStart, enrollMFAConfirm } from '@/api/auth'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

// The QR code is dark modules on a light ground in every theme: a scanner reads
// that polarity only, so the box around it stays white.
const QR_BOX = 'h-44 w-44 rounded border bg-white p-2' // theme-ok: a QR code must stay dark on white to scan

// Compelled TOTP enrolment, for a session that owes it: after a password
// login, and after verifying a signup (#369). onEnrolled runs once the code is
// confirmed; it finishes signing in.
export function MFAEnrollForm({ onEnrolled }: { onEnrolled: () => Promise<void> }) {
  const [secret, setSecret] = useState('')
  const [qrDataURL, setQRDataURL] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    enrollMFAStart()
      .then(({ secret, qr_data_url }) => {
        setSecret(secret)
        setQRDataURL(qr_data_url)
      })
      .catch((err) => setError(extractError(err)))
  }, [])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await enrollMFAConfirm(code)
      await onEnrolled()
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <p className="text-sm text-fg-muted">
        Your administrator requires two-factor authentication for your role. Scan the QR
        code with an authenticator app (Google Authenticator, Authy, 1Password), or enter
        the secret manually, then confirm with a code.
      </p>
      {qrDataURL ? (
        <div className="flex flex-col items-center gap-2">
          <img
            alt="TOTP QR code"
            className={QR_BOX}
            src={qrDataURL}
          />
          <code className="max-w-full truncate text-[11px] text-fg-faint" title={secret}>
            Secret: {secret}
          </code>
        </div>
      ) : (
        <p className="text-sm text-fg-faint">Generating setup code…</p>
      )}
      <div className="space-y-1">
        <Label htmlFor="enroll">Verification code</Label>
        <Input
          id="enroll"
          type="text"
          inputMode="numeric"
          maxLength={6}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          required
          autoComplete="one-time-code"
        />
      </div>
      {error && <p role="alert" className="text-sm text-danger-fg">{error}</p>}
      <Button type="submit" className="w-full" disabled={loading || !secret}>
        {loading ? 'Confirming…' : 'Confirm & sign in'}
      </Button>
    </form>
  )
}
