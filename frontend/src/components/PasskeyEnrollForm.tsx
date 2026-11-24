import { useState } from 'react'
import { addPasskey, browserSupportsPasskeys, wasCancelled } from '@/api/passkeys'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

// Compelled passkey registration, for a session whose role must use a passkey
// (#616): after a password login, after verifying a signup, and after single
// sign-on that was not passkey-grade. onEnrolled runs once the key is stored;
// it finishes signing in. Registering the key is what passes MFA, so there is
// nothing else to do afterwards.
export function PasskeyEnrollForm({ onEnrolled }: { onEnrolled: () => Promise<void> }) {
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  if (!browserSupportsPasskeys()) {
    return (
      <p role="alert" className="text-sm text-red-600">
        Your administrator requires a passkey for your role, and this browser cannot create one.
        Use a current version of Chrome, Edge, Firefox or Safari, over HTTPS.
      </p>
    )
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await addPasskey(name.trim() || 'My passkey')
      await onEnrolled()
    } catch (err) {
      // Dismissing the browser prompt is a choice, not a failure.
      if (!wasCancelled(err)) setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <p className="text-sm text-gray-600">
        Your administrator requires a passkey for your role. Register one with your
        fingerprint, face, screen lock or a security key; you will use it each time you
        sign in.
      </p>
      <div className="space-y-1">
        <Label htmlFor="passkey-name">Name for this passkey</Label>
        <Input
          id="passkey-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="My laptop"
          maxLength={100}
          autoComplete="off"
        />
      </div>
      {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      <Button type="submit" className="w-full" disabled={loading}>
        {loading ? 'Waiting for your key…' : 'Register a passkey & sign in'}
      </Button>
    </form>
  )
}
