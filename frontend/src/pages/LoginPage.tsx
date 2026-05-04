import { useEffect, useState } from 'react'
import { useNavigate, Link } from '@tanstack/react-router'
import { login, verifyMFA, getMe, enrollMFAStart, enrollMFAConfirm, getSignupStatus } from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

type Step = 'credentials' | 'verify' | 'enroll'

export function LoginPage() {
  const navigate = useNavigate()
  const { setUser } = useAuthStore()
  const [step, setStep] = useState<Step>('credentials')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [mfaCode, setMfaCode] = useState('')
  const [enrollSecret, setEnrollSecret] = useState('')
  const [enrollQRDataURL, setEnrollQRDataURL] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [signupEnabled, setSignupEnabled] = useState(false)

  useEffect(() => {
    getSignupStatus().then(({ enabled }) => setSignupEnabled(enabled)).catch(() => {})
  }, [])

  async function completeLogin() {
    const user = await getMe()
    setUser(user)
    navigate({ to: '/dashboard' })
  }

  async function handleLogin(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const { user, mfa_needed, mfa_enrollment_needed } = await login(email, password)
      if (mfa_enrollment_needed) {
        setStep('enroll')
      } else if (mfa_needed) {
        setStep('verify')
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

  async function handleVerify(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await verifyMFA(mfaCode)
      await completeLogin()
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  // Kick off enrollment when we reach the enroll step.
  useEffect(() => {
    if (step !== 'enroll' || enrollSecret) return
    enrollMFAStart()
      .then(({ secret, qr_data_url }) => {
        setEnrollSecret(secret)
        setEnrollQRDataURL(qr_data_url)
      })
      .catch((err) => setError(extractError(err)))
  }, [step, enrollSecret])

  async function handleEnroll(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await enrollMFAConfirm(mfaCode)
      await completeLogin()
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  const title =
    step === 'verify' ? 'Two-factor authentication'
    : step === 'enroll' ? 'Set up two-factor authentication'
    : 'Sign in to Go Help Desk'

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">{title}</CardTitle>
        </CardHeader>
        <CardContent>
          {step === 'credentials' && (
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
              {signupEnabled && (
                <p className="text-center text-sm text-gray-500">
                  Don't have an account?{' '}
                  <Link to="/signup" className="text-blue-600 hover:underline">
                    Create one
                  </Link>
                </p>
              )}
            </form>
          )}

          {step === 'verify' && (
            <form onSubmit={handleVerify} className="space-y-4">
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

          {step === 'enroll' && (
            <form onSubmit={handleEnroll} className="space-y-4">
              <p className="text-sm text-gray-600">
                Your administrator requires two-factor authentication for your role. Scan the QR
                code with an authenticator app (Google Authenticator, Authy, 1Password), or enter
                the secret manually, then confirm with a code.
              </p>
              {enrollQRDataURL ? (
                <div className="flex flex-col items-center gap-2">
                  <img
                    alt="TOTP QR code"
                    className="h-44 w-44 rounded border bg-white p-2"
                    src={enrollQRDataURL}
                  />
                  <code className="max-w-full truncate text-[11px] text-gray-500" title={enrollSecret}>
                    Secret: {enrollSecret}
                  </code>
                </div>
              ) : (
                <p className="text-sm text-gray-400">Generating setup code…</p>
              )}
              <div className="space-y-1">
                <Label htmlFor="enroll">Verification code</Label>
                <Input
                  id="enroll"
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
              <Button type="submit" className="w-full" disabled={loading || !enrollSecret}>
                {loading ? 'Confirming…' : 'Confirm & sign in'}
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
