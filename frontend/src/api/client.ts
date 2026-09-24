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
    if (err.response?.status === 401) {
      // Redirect to login unless already there.
      if (!window.location.pathname.startsWith('/login')) {
        window.location.href = '/login'
      }
    }
    return Promise.reject(err)
  }
)

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
