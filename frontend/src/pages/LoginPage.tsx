import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { login, verifyMFA, getMe } from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export function LoginPage() {
  const navigate = useNavigate()
  const { setUser, mfaPending, setMFAPending } = useAuthStore()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [mfaCode, setMfaCode] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const { user, mfa_needed } = await login(email, password)
      if (mfa_needed) {
        setMFAPending(true)
      } else {
        setUser(user)
        navigate({ to: '/dashboard' })
      }
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  async function handleMFA(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await verifyMFA(mfaCode)
      const user = await getMe()
      setUser(user)
      navigate({ to: '/dashboard' })
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">
            {mfaPending ? 'Two-factor authentication' : 'Sign in to Open Help Desk'}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {!mfaPending ? (
            <form onSubmit={handleLogin} className="space-y-4">
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
              <div className="space-y-1">
                <Label htmlFor="password">Password</Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                  autoComplete="current-password"
                />
              </div>
              {error && <p className="text-sm text-red-600">{error}</p>}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? 'Signing in…' : 'Sign in'}
              </Button>
            </form>
          ) : (
            <form onSubmit={handleMFA} className="space-y-4">
              <p className="text-sm text-gray-600">Enter the 6-digit code from your authenticator app.</p>
              <div className="space-y-1">
                <Label htmlFor="mfa">Verification code</Label>
                <Input
                  id="mfa"
                  type="text"
                  inputMode="numeric"
                  maxLength={6}
                  value={mfaCode}
                  onChange={(e) => setMfaCode(e.target.value)}
                  required
                  autoComplete="one-time-code"
                />
              </div>
              {error && <p className="text-sm text-red-600">{error}</p>}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? 'Verifying…' : 'Verify'}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
