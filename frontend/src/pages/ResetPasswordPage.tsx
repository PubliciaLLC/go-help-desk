import { useEffect, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { resetPassword } from '@/api/auth'
import { extractError, extractErrorCode } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

// How long the confirmation is on screen before the sign-in page replaces it.
const REDIRECT_AFTER_MS = 2000

const DEAD_LINK = 'This link is invalid, has expired or has already been used.'

// The page a reset link opens (#451). It never signs anybody in: the link is a
// credential sitting in an inbox, and it must not be a way past the second
// factor. On success the person goes to the sign-in page and signs in with the
// new password like anyone else.
export function ResetPasswordPage() {
  const navigate = useNavigate()
  // Read once, into state: the effect below removes it from the address, and a
  // later render would otherwise find none.
  const [token] = useState(() => new URLSearchParams(window.location.search).get('token') ?? '')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState(token ? '' : DEAD_LINK)
  // A link that cannot work replaces the form; a refused password, or a fault,
  // keeps it, because the link may still be good.
  const [linkDead, setLinkDead] = useState(!token)
  const [loading, setLoading] = useState(false)
  const [done, setDone] = useState(false)

  // The link is a credential. Once read it does not stay in the address bar,
  // the browser's history or a Referer header.
  useEffect(() => {
    const { pathname, search, hash } = window.location
    const params = new URLSearchParams(search)
    if (!params.has('token')) return
    params.delete('token')
    const rest = params.toString()
    window.history.replaceState(window.history.state, '', pathname + (rest ? `?${rest}` : '') + hash)
  }, [])

  useEffect(() => {
    if (!done) return
    const t = setTimeout(() => navigate({ to: '/login' }), REDIRECT_AFTER_MS)
    return () => clearTimeout(t)
  }, [done, navigate])

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (password !== confirm) {
      setError('The two passwords do not match.')
      return
    }
    setLoading(true)
    try {
      await resetPassword(token, password)
      setDone(true)
    } catch (err) {
      const code = extractErrorCode(err)
      if (code === 'token_invalid') {
        setLinkDead(true)
        setError(DEAD_LINK)
      } else if (code === 'password_too_short' || code === 'password_too_long') {
        setError(extractError(err))
      } else {
        setError('Something went wrong. Please try again.')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">Choose a new password</CardTitle>
        </CardHeader>
        <CardContent>
          {done ? (
            <div className="space-y-3 text-sm text-gray-700">
              <p role="status">
                Your password has been changed. Sign in with your new password.
              </p>
              <p>
                <Link to="/login" className="text-blue-600 hover:underline">
                  Go to sign in
                </Link>
              </p>
            </div>
          ) : linkDead ? (
            <div className="space-y-3 text-sm text-gray-700">
              <p role="alert" className="text-red-600">{error}</p>
              <p>
                <Link to="/forgot-password" className="text-blue-600 hover:underline">
                  Request a new link
                </Link>
              </p>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-4">
              <div className="space-y-1">
                <Label htmlFor="new-password">New password</Label>
                <Input
                  id="new-password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                  autoComplete="new-password"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="confirm-password">Confirm new password</Label>
                <Input
                  id="confirm-password"
                  type="password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  required
                  autoComplete="new-password"
                />
              </div>
              {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? 'Saving…' : 'Change password'}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
