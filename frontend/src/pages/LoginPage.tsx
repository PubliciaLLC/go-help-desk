import { useEffect, useState } from 'react'
import { useNavigate, Link } from '@tanstack/react-router'
import { login, logout, verifyMFA, getMe, changePassword, getSignupStatus, getAuthProviders } from '@/api/auth'
import type { LoginResponse } from '@/api/auth'
import { getSiteConfig } from '@/api/admin'
import { useAuthStore } from '@/store/auth'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { useSiteBranding } from '@/hooks/useSiteBranding'
import { signInWithPasskey, wasCancelled } from '@/api/passkeys'
import { MFAEnrollForm } from '@/components/MFAEnrollForm'
import { PasskeyEnrollForm } from '@/components/PasskeyEnrollForm'

type Step = 'credentials' | 'verify' | 'enroll' | 'passkey' | 'passkey-enroll' | 'newpassword'

// Refusal codes the server puts in /login?error= after SAML or OIDC sends the
// browser back. Only these strings are ever shown: the query string is
// attacker-controlled, so the code is a key and never text (#401). A Map, not
// an object literal, so "constructor" or "__proto__" cannot find anything.
const SSO_ERRORS = new Map<string, string>([
  ['email_taken', 'An account with the email address your identity provider sent already exists here and is not linked to single sign-on. Ask an administrator.'],
  ['saml_nameid_format', 'Your identity provider cannot issue the persistent identifier this help desk asks for. Ask an administrator to check the single sign-on setup.'],
  ['domain_not_allowed', 'Accounts from your email domain cannot sign in here.'],
  ['account_disabled', 'This account is disabled. Ask an administrator if you need access.'],
  ['account_link_refused', 'Your single sign-on identity could not be linked to the existing account with that email address. Ask an administrator.'],
  ['invalid_assertion', 'Your identity provider did not send the details this help desk needs. Ask an administrator to check the single sign-on setup.'],
  ['email_not_verified', 'Your identity provider did not send a verified email address, so you could not be signed in.'],
  ['invalid_email', 'Your identity provider sent an email address this help desk cannot use. Ask an administrator.'],
  ['sso_session_used', 'That sign-in has already been used. Please sign in again.'],
  ['invalid_session', 'Your sign-in expired or was started in another window. Please try again.'],
  ['invalid_state', 'Your sign-in expired or was started in another window. Please try again.'],
  ['idp_error', 'Your identity provider did not allow the sign-in.'],
  ['missing_code', 'The response from your identity provider could not be checked. Please try again.'],
  ['missing_id_token', 'The response from your identity provider could not be checked. Please try again.'],
  ['invalid_id_token', 'The response from your identity provider could not be checked. Please try again.'],
  ['internal_error', 'Something went wrong while signing you in. Please try again.'],
])
const SSO_ERROR_FALLBACK = 'Single sign-on did not finish. Please try again, or ask an administrator.'

// The step the server wants shown after a single sign-on that still owes a
// passkey (#616): /login?sso_step=<code>. Like the error codes this is a key
// into an allowlist and never text, and an unknown value is ignored. It only
// chooses which screen to show; the server enforces the step whatever it says.
// `thenPasskeyEnrol` is true when an authenticator code comes first and a key
// is registered after it.
const SSO_STEPS = new Map<string, { step: Step; thenPasskeyEnrol: boolean }>([
  ['passkey', { step: 'passkey', thenPasskeyEnrol: false }],
  ['passkey_enroll', { step: 'passkey-enroll', thenPasskeyEnrol: false }],
  ['verify_then_passkey_enroll', { step: 'verify', thenPasskeyEnrol: true }],
])

function ssoStep(search: string) {
  const code = new URLSearchParams(search).get('sso_step')
  return code === null ? undefined : SSO_STEPS.get(code)
}

function ssoErrorMessage(search: string): string {
  const code = new URLSearchParams(search).get('error')
  if (code === null) return ''
  return SSO_ERRORS.get(code) ?? SSO_ERROR_FALLBACK
}

export function LoginPage() {
  const navigate = useNavigate()
  const { name: siteName } = useSiteBranding()
  const { setUser } = useAuthStore()
  const [sso] = useState(() => ssoStep(window.location.search))
  const [step, setStep] = useState<Step>(sso?.step ?? 'credentials')
  // The authenticator code is checked first and a passkey is registered after
  // it (#616): TOTP proves a factor for this role but does not pass MFA.
  const [passkeyEnrolAfterVerify, setPasskeyEnrolAfterVerify] = useState(sso?.thenPasskeyEnrol ?? false)
  // True while the page is on a step it was sent to by ?sso_step=. That step
  // needs a live pending session; with none (a bookmark, an expired window)
  // it cannot succeed, so it carries a way back to the ordinary form.
  const [viaSSOStep, setViaSSOStep] = useState(sso !== undefined)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [mfaCode, setMfaCode] = useState('')
  // The unlock command's password has to be replaced before the rest of the
  // sign-in (#497). What the sign-in answered is kept so it can carry on.
  const [afterPasswordChange, setAfterPasswordChange] = useState<LoginResponse | null>(null)
  // True from a sign-in that owes a password change until the change is made:
  // an account that holds a factor proves it first (see handleLogin), and the
  // change follows the factor step instead of replacing it.
  const [passwordChangeOwed, setPasswordChangeOwed] = useState(false)
  // True once the factor step has been passed while a change is still owed, so
  // the page knows what follows the new password.
  const [factorProved, setFactorProved] = useState(false)
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  // Seeded from ?error= so a refused single sign-on says why; handleLogin
  // clears it when the person tries again.
  const [error, setError] = useState(() => ssoErrorMessage(window.location.search))
  const [loading, setLoading] = useState(false)
  const [signupEnabled, setSignupEnabled] = useState(false)
  const [guestEnabled, setGuestEnabled] = useState(false)
  // True when the server can email a reset link (#451); the link is offered
  // only then, because it would lead to a page that cannot work.
  const [resetOffered, setResetOffered] = useState(false)
  const [providers, setProviders] = useState({
    password: true,
    saml: false,
    oidc: false,
  })

  useEffect(() => {
    getSignupStatus().then(({ enabled }) => setSignupEnabled(enabled)).catch(() => {})

    // Only offered when the instance actually accepts guest tickets. Linking
    // to /submit unconditionally would send visitors to a form that answers
    // 404 on submit, which is worse than not offering it.
    getSiteConfig()
      .then((c) => setGuestEnabled(c.guest_submission_enabled ?? false))
      .catch(() => {})

    getAuthProviders()
      .then((data) => {
        const enabled = Object.fromEntries(
          data.providers.map((provider) => [provider.name, provider.enabled])
        )

        setResetOffered(data.password_reset ?? false)
        setProviders({
          password: enabled.local ?? false,
          saml: enabled.saml ?? false,
          oidc: enabled.oidc ?? false,
        })
      })
      .catch(() => {})
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
      const answer = await login(email, password)
      setPasswordChangeOwed(!!answer.password_change_required)
      setFactorProved(false)
      // The server will not change the password of an account that holds a
      // factor until the factor is proved (403 mfa_required, #498): a held
      // authenticator (mfa_needed) or key (passkey_needed) comes first, and the
      // new password after it. An account that holds none has nothing to prove
      // and changes it first, as enrolment waits behind it (#497).
      if (answer.password_change_required && !answer.mfa_needed && !answer.passkey_needed) {
        setAfterPasswordChange(answer)
        setStep('newpassword')
      } else {
        proceed(answer)
      }
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  // Where a sign-in goes once the password is settled.
  function proceed({
    user, mfa_needed, passkey_needed, mfa_enrollment_needed, passkey_enrollment_needed,
  }: LoginResponse) {
    if (mfa_enrollment_needed) {
      setStep('enroll')
    } else if (mfa_needed) {
      setPasskeyEnrolAfterVerify(!!passkey_enrollment_needed)
      setStep('verify')
    } else if (passkey_enrollment_needed) {
      // A role that must use a passkey, and the account has none (#616).
      setStep('passkey-enroll')
    } else if (passkey_needed) {
      // An account holding passkeys and no authenticator app. The password
      // has been accepted and the session already names the account; this
      // step only has to prove the key.
      setStep('passkey')
    } else {
      setUser(user)
      navigate({ to: '/dashboard' })
    }
  }

  // Back to the ordinary form from a step that ?sso_step= opened (#616). Any
  // partial session is ended, but a failure to do that is not a reason to stay
  // stuck: with no session there is nothing to end.
  async function startOver() {
    await logout().catch(() => {})
    const params = new URLSearchParams(window.location.search)
    params.delete('sso_step')
    const qs = params.toString()
    window.history.replaceState(
      {}, '', window.location.pathname + (qs ? `?${qs}` : '') + window.location.hash,
    )
    setViaSSOStep(false)
    setPasskeyEnrolAfterVerify(false)
    setMfaCode('')
    setError('')
    setStep('credentials')
  }

  async function handleNewPassword(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (newPassword !== confirmPassword) {
      setError('The two passwords do not match.')
      return
    }
    setLoading(true)
    try {
      // The password just typed to sign in is the current one.
      await changePassword(password, newPassword)
      setPasswordChangeOwed(false)
      if (factorProved) {
        // The factor came first: what is left is the key a passkey-required
        // role registers, or nothing.
        if (passkeyEnrolAfterVerify) setStep('passkey-enroll')
        else await completeLogin()
      } else if (afterPasswordChange) {
        proceed(afterPasswordChange)
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
      if (passwordChangeOwed) {
        setFactorProved(true)
        setStep('newpassword')
      } else if (passkeyEnrolAfterVerify) {
        // The code proved a factor, which is what lets a key be registered;
        // the key is what finishes the sign-in.
        setStep('passkey-enroll')
      } else {
        await completeLogin()
      }
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  async function handlePasskey() {
    setError('')
    setLoading(true)
    try {
      await signInWithPasskey()
      if (passwordChangeOwed) {
        setFactorProved(true)
        setStep('newpassword')
      } else {
        await completeLogin()
      }
    } catch (err) {
      // Dismissing the browser prompt is a choice, not a failure, and the
      // person is still on this screen with the button in front of them.
      if (!wasCancelled(err)) setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  // Previously hardcoded "Sign in to Go Help Desk", so a branded instance
  // showed its own name everywhere except the first screen anyone sees (#56).
  const title =
    step === 'newpassword' ? 'Choose a new password'
    : step === 'verify' ? 'Two-factor authentication'
    : step === 'enroll' ? 'Set up two-factor authentication'
    : step === 'passkey' ? 'Confirm it is you'
    : step === 'passkey-enroll' ? 'Register a passkey'
    : `Sign in to ${siteName}`

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle className="text-xl wrap-anywhere">{title}</CardTitle>
        </CardHeader>
        <CardContent>
          {step === 'credentials' && (
            <>
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
              {resetOffered && (
                <p className="text-right text-sm">
                  <Link to="/forgot-password" className="text-blue-600 hover:underline">
                    Forgot password?
                  </Link>
                </p>
              )}
              {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
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
              {guestEnabled && (
                <p className="text-center text-sm text-gray-500">
                  No account?{' '}
                  <Link to="/submit" className="text-blue-600 hover:underline">
                    Submit a ticket
                  </Link>
                  {' · '}
                  <Link to="/track" className="text-blue-600 hover:underline">
                    Find an existing one
                  </Link>
                </p>
              )}

            </form>
	    {providers.oidc && (
              <Button
                type="button"
                className="w-full mt-3 bg-green-600 text-white hover:bg-green-700"
                onClick={() => window.location.href = '/api/v1/auth/oidc/login'}
               >
                Sign in with OIDC
               </Button>
             )}
            {/* A plain link, not a button with an onClick: a full-page
                navigation the router leaves alone (#393). Classes match the
                OIDC button; Button has no asChild. */}
            {providers.saml && (
              <a
                href="/api/v1/auth/saml/login"
                className="mt-3 inline-flex h-10 w-full items-center justify-center rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-green-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2"
              >
                Sign in with SAML
              </a>
            )}
            </>
          )}

          {step === 'newpassword' && (
            <form onSubmit={handleNewPassword} className="space-y-4">
              <p className="text-sm text-gray-600">
                You signed in with a password an administrator generated for you. Choose a
                new one to continue.
              </p>
              <div className="space-y-1">
                <Label htmlFor="new-password">New password</Label>
                <Input
                  id="new-password"
                  type="password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  required
                  minLength={8}
                  autoComplete="new-password"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="confirm-password">Confirm new password</Label>
                <Input
                  id="confirm-password"
                  type="password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
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
              {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? 'Verifying…' : 'Verify'}
              </Button>
            </form>
          )}

          {step === 'passkey' && (
            <div className="space-y-4">
              <p className="text-sm text-gray-600">
                Use your fingerprint, face, screen lock or security key to finish signing in.
              </p>
              {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
              <Button type="button" className="w-full" disabled={loading} onClick={handlePasskey}>
                {loading ? 'Waiting for your key…' : 'Continue with a passkey'}
              </Button>
            </div>
          )}

          {step === 'enroll' && <MFAEnrollForm onEnrolled={completeLogin} />}

          {step === 'passkey-enroll' && <PasskeyEnrollForm onEnrolled={completeLogin} />}

          {viaSSOStep && step !== 'credentials' && (
            <Button type="button" variant="link" className="mt-3 w-full" onClick={startOver}>
              Start over
            </Button>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
