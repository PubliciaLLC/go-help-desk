import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { requestPasswordReset } from '@/api/auth'
import { extractError, extractErrorCode } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

// One confirmation, whatever the address (#451): the server answers 202 whether
// or not there is an account, and a page that worded the two differently would
// give that away.
export function ForgotPasswordPage() {
  const [email, setEmail] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [done, setDone] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await requestPasswordReset(email)
      setDone(true)
    } catch (err) {
      setError(
        extractErrorCode(err) === 'email_not_configured'
          ? 'This server cannot send email, so it cannot send a reset link. Ask an administrator to reset your password.'
          : extractError(err),
      )
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-canvas">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">Forgot your password?</CardTitle>
        </CardHeader>
        <CardContent>
          {done ? (
            <div className="space-y-3 text-sm text-fg-soft">
              <p role="status">
                If that address has an account, we have emailed it a link to choose a new
                password. The link works for 30 minutes.
              </p>
              <p>
                <Link to="/login" className="text-link hover:underline">
                  Back to sign in
                </Link>
              </p>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-4">
              <p className="text-sm text-fg-muted">
                Enter the email address of your account and we will send you a link to
                choose a new password.
              </p>
              <div className="space-y-1">
                <Label htmlFor="email">Email</Label>
                <Input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                  autoComplete="email"
                />
              </div>
              {error && <p role="alert" className="text-sm text-danger-fg">{error}</p>}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? 'Sending…' : 'Send reset link'}
              </Button>
              <p className="text-center text-sm text-fg-faint">
                <Link to="/login" className="text-link hover:underline">
                  Back to sign in
                </Link>
              </p>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
