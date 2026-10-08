import { useEffect, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { verifyEmail, lookupVerification, getMe } from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import { extractError, extractErrorCode } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { MFAEnrollForm } from '@/components/MFAEnrollForm'

// The password is chosen here, not on the signup form (#360): only whoever
// reads the inbox reaches this page, so only they choose it.
export function VerifyEmailPage() {
  const navigate = useNavigate()
  const { setUser } = useAuthStore()
  const token = new URLSearchParams(window.location.search).get('token') ?? ''
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState(token ? '' : 'No verification token found in the URL.')
  // A link that cannot work replaces the form; a refused password keeps it,
  // because the link is still good.
  const [linkDead, setLinkDead] = useState(!token)
  const [loading, setLoading] = useState(false)
  const [account, setAccount] = useState<{ email: string } | null>(null)
  // The verified session owes MFA enrolment (#369); nothing else will answer
  // it until that is done.
  const [mustEnrol, setMustEnrol] = useState(false)

  // Look the link up first (#370): to show which address this is, to hand a
  // password manager the address, and to say a dead link is dead before a
  // password is typed. Only a verdict on the link closes the form; a network
  // error or a server fault leaves it, and the submit gives the real answer.
  useEffect(() => {
    if (!token) return
    lookupVerification(token)
      .then(setAccount)
      .catch((err) => {
        const code = extractErrorCode(err)
        if (code === 'token_expired' || code === 'token_invalid') refuseLink(code)
      })
  }, [token])

  function refuseLink(code: string) {
    setLinkDead(true)
    setError(
      code === 'token_expired'
        ? 'This verification link has expired. Please sign up again to receive a new one.'
        : 'This verification link is invalid or has already been used.',
    )
  }

  async function finishSignIn() {
    setUser(await getMe())
    navigate({ to: '/dashboard' })
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (password !== confirm) {
      setError('Passwords do not match.')
      return
    }
    setLoading(true)
    try {
      const { user, mfa_enrollment_needed } = await verifyEmail(token, password)
      if (mfa_enrollment_needed) {
        setMustEnrol(true)
      } else {
        setUser(user)
        navigate({ to: '/dashboard' })
      }
    } catch (err) {
      // The code, not the message. Comparing the message to a code meant
      // the expired branch never fired: an expired link was always reported
      // as invalid or already used, and the advice to sign up again — the one
      // thing that would have helped — was unreachable.
      const code = extractErrorCode(err)
      if (code === 'password_too_short') {
        setError(extractError(err))
      } else {
        refuseLink(code)
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">
            {mustEnrol ? 'Set up two-factor authentication' : 'Choose your password'}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {mustEnrol ? (
            <MFAEnrollForm onEnrolled={finishSignIn} />
          ) : linkDead ? (
            <div className="space-y-3 text-sm text-gray-700">
              <p role="alert" className="text-red-600">{error}</p>
              <p>
                <Link to="/signup" className="text-blue-600 hover:underline">
                  Back to sign up
                </Link>
              </p>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-4">
              {/* The address only, not the display name: whoever signed up
                  first chose the name, and that may not be this inbox's owner. */}
              {account && (
                <div className="space-y-1">
                  <Label htmlFor="email">Email</Label>
                  <Input id="email" type="email" value={account.email} readOnly autoComplete="username" />
                </div>
              )}
              <div className="space-y-1">
                <Label htmlFor="password">Password</Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                  autoComplete="new-password"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="confirm">Confirm password</Label>
                <Input
                  id="confirm"
                  type="password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  required
                  autoComplete="new-password"
                />
              </div>
              {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? 'Creating account…' : 'Create account'}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
