import { useEffect, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { verifyEmail } from '@/api/auth'
import { useAuthStore } from '@/store/auth'
import { extractErrorCode } from '@/api/client'
import { Spinner } from '@/components/ui/spinner'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

export function VerifyEmailPage() {
  const navigate = useNavigate()
  const { setUser } = useAuthStore()
  const [error, setError] = useState('')

  useEffect(() => {
    const token = new URLSearchParams(window.location.search).get('token') ?? ''
    if (!token) {
      setError('No verification token found in the URL.')
      return
    }
    verifyEmail(token)
      .then(({ user }) => {
        setUser(user)
        navigate({ to: '/dashboard' })
      })
      .catch((err) => {
        // The code, not the message. Comparing the message to a code meant
        // this branch never fired: an expired link was always reported as
        // invalid or already used, and the advice to sign up again — the one
        // thing that would have helped — was unreachable.
        const code = extractErrorCode(err)
        setError(
          code === 'token_expired'
            ? 'This verification link has expired. Please sign up again to receive a new one.'
            : 'This verification link is invalid or has already been used.',
        )
      })
  // Run once on mount only.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl">Verifying your email</CardTitle>
        </CardHeader>
        <CardContent>
          {error ? (
            <div className="space-y-3 text-sm text-gray-700">
              <p role="alert" className="text-red-600">{error}</p>
              <p>
                <Link to="/signup" className="text-blue-600 hover:underline">
                  Back to sign up
                </Link>
              </p>
            </div>
          ) : (
            <div className="flex items-center gap-3 text-sm text-gray-600">
              <Spinner />
              <span>Please wait…</span>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
