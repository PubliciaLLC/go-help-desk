import { api } from './client'
import type { User } from './types'

export interface LoginResponse {
  user: User
  mfa_needed: boolean
  /**
   * The account holds passkeys and no authenticator app, so the second factor
   * is a key rather than a code. Never true at the same time as `mfa_needed`:
   * the server picks one gate.
   */
  passkey_needed: boolean
  mfa_enrollment_needed: boolean
  /**
   * The account was unlocked from the server console and still holds the
   * password it was handed (#497). The session may only change the password;
   * every other route answers 403 password_change_required until it has.
   */
  password_change_required?: boolean
}

export async function login(email: string, password: string): Promise<LoginResponse> {
  const res = await api.post<LoginResponse>('/auth/local/login', { email, password })
  return res.data
}

export async function logout(): Promise<void> {
  await api.post('/auth/local/logout')
}

export async function verifyMFA(code: string): Promise<void> {
  await api.post('/auth/local/mfa/verify', { code })
}

export async function getMe(): Promise<User> {
  const res = await api.get<User>('/me')
  return res.data
}

export async function changePassword(current: string, next: string): Promise<void> {
  await api.patch('/me/password', { current_password: current, new_password: next })
}

export async function enrollMFAStart(): Promise<{ secret: string; qr_url: string; qr_data_url: string }> {
  const res = await api.post('/me/mfa/enroll')
  return res.data
}

export async function enrollMFAConfirm(code: string): Promise<void> {
  await api.post('/me/mfa/enroll/confirm', { code })
}

export interface SignupStatus {
  enabled: boolean
  open_registration: boolean
  saml_enabled: boolean
}

export async function getSignupStatus(): Promise<SignupStatus> {
  const res = await api.get<SignupStatus>('/auth/signup/status')
  return res.data
}

// The address only (#360, #374): the name and password are chosen on the
// verification page, so a second signup for the same address cannot choose
// them for the owner.
export async function signup(email: string): Promise<void> {
  await api.post('/auth/signup', { email })
}

// What a verification link is for, without using it (#370).
export async function lookupVerification(token: string): Promise<{ email: string }> {
  const res = await api.get<{ email: string }>('/auth/verify-email', { params: { token } })
  return res.data
}

export async function verifyEmail(token: string, displayName: string, password: string): Promise<LoginResponse> {
  const res = await api.post<LoginResponse>('/auth/verify-email', { token, display_name: displayName, password })
  return res.data
}

// Asks for a link to choose a new password (#451). The server answers 202
// whether or not the address has an account, so there is nothing to return.
export async function requestPasswordReset(email: string): Promise<void> {
  await api.post('/auth/forgot-password', { email })
}

// Chooses the new password a mailed link was for. It signs nobody in: the
// person goes back to the sign-in page (#451).
export async function resetPassword(token: string, password: string): Promise<void> {
  await api.post('/auth/reset-password', { token, password })
}

export interface AuthProvider {
  name: string
  enabled: boolean
}

export interface AuthProviders {
  providers: AuthProvider[]
  /**
   * False when the server has passkeys off because BASE_URL cannot be a
   * passkey domain (#494). Absent from a server that predates it: read as on.
   */
  passkeys?: boolean
  /**
   * True when the server can email a password reset link (#451), so the
   * sign-in page offers "Forgot password?". Absent from a server that predates
   * it: read as off.
   */
  password_reset?: boolean
}

export async function getAuthProviders(): Promise<AuthProviders> {
  const res = await api.get<AuthProviders>('/auth/providers')
  return res.data
}

