import axios from 'axios'
import type { ApiError } from './types'

export const api = axios.create({
  baseURL: '/api/v1',
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

api.interceptors.response.use(
  (res) => res,
  (err) => {
    if (err.response?.status === 401 && !isRefusedProof(err)) {
      // Redirect to login unless already there.
      if (!window.location.pathname.startsWith('/login')) {
        window.location.href = '/login'
      }
    }
    return Promise.reject(err)
  }
)

// 401s that answer something the person just tried to prove, on a session that
// is still good: a wrong second-factor code, a passkey the server refused. The
// account page asks for these mid-session (#336), and sending someone to the
// login page for a mistyped digit loses what they were doing. The login page
// never needed this because it is already where they would be sent.
const REFUSED_PROOF = ['invalid_mfa_code', 'assertion_refused']

function isRefusedProof(err: unknown): boolean {
  return REFUSED_PROOF.includes(extractErrorCode(err))
}

/**
 * The API error CODE from a failed call — `token_expired`, `email_taken` and
 * so on — or an empty string when there is not one.
 *
 * Separate from `extractError`, which returns the human message. Comparing
 * that message to a code is the mistake this exists to stop: the verify-email
 * page did exactly that, so its "your link has expired" branch could never
 * fire and an expired link was always reported as invalid or already used.
 */
export function extractErrorCode(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const data = err.response?.data as ApiError | undefined
    return data?.error?.code ?? ''
  }
  return ''
}

export function extractError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const data = err.response?.data as ApiError | undefined
    return data?.error?.message ?? err.message
  }
  return String(err)
}

/**
 * The status and the server's own message from a failed API call.
 *
 * Both empty for anything that is not an axios failure, and `message` empty
 * when the response carried no API error body — a 503 from a proxy in front of
 * the app is the case that matters, because "Request failed with status code
 * 503" is not a sentence to show a reader. A caller that can say something
 * better for a given status then knows that it should.
 */
export function apiRefusal(err: unknown): { status?: number; message?: string } {
  if (!axios.isAxiosError(err)) return {}
  const data = err.response?.data as ApiError | undefined
  return { status: err.response?.status, message: data?.error?.message }
}
