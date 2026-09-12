import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import type { AxiosError } from 'axios'
import { api, extractError } from './client'

// The first tests in this codebase. client.ts is the deliberate starting point:
// every API call in the app passes through it, it is 29 lines, and both pieces
// of logic in it fail silently when broken — a mangled error message looks like
// a backend problem, and a broken redirect guard looks like a hung page.

describe('extractError', () => {
  // The server's error envelope is { error: { code, message } } (ApiError).
  // This is the only place the app unwraps it, so every user-visible failure
  // message depends on this function picking the right field.
  function axiosErrorWith(data: unknown): AxiosError {
    const err = new Error('Request failed') as AxiosError
    err.isAxiosError = true
    err.toJSON = () => ({})
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    err.response = { data, status: 400, statusText: 'Bad Request', headers: {}, config: {} as any }
    return err
  }

  it('prefers the server message from the error envelope', () => {
    const err = axiosErrorWith({ error: { code: 'domain_not_allowed', message: 'this email domain is not allowed' } })
    expect(extractError(err)).toBe('this email domain is not allowed')
  })

  it('falls back to the axios message when the body has no envelope', () => {
    // A proxy error or a 502 from something in front of the app returns HTML
    // or nothing at all, and must not surface as "undefined".
    expect(extractError(axiosErrorWith({ unexpected: true }))).toBe('Request failed')
    expect(extractError(axiosErrorWith(undefined))).toBe('Request failed')
  })

  it('falls back when the envelope is present but the message is missing', () => {
    expect(extractError(axiosErrorWith({ error: { code: 'internal_error' } }))).toBe('Request failed')
  })

  it('stringifies a non-axios error rather than throwing', () => {
    // Anything thrown inside a component reaches here too.
    expect(extractError(new Error('boom'))).toBe('Error: boom')
    expect(extractError('plain string')).toBe('plain string')
    expect(extractError(null)).toBe('null')
  })
})

describe('401 response interceptor', () => {
  // window.location is replaced wholesale: jsdom's real one cannot be assigned
  // to, and the interceptor both reads pathname and writes href.
  let assigned: string | undefined
  const original = window.location

  beforeEach(() => {
    assigned = undefined
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', { value: original, writable: true, configurable: true })
  })

  function stubLocation(pathname: string) {
    Object.defineProperty(window, 'location', {
      configurable: true,
      writable: true,
      value: {
        pathname,
        get href() {
          return 'http://localhost' + pathname
        },
        set href(v: string) {
          assigned = v
        },
      },
    })
  }

  // Reach the interceptor by invoking the handler axios registered, rather than
  // making a real request — the behaviour under test is the handler itself.
  function rejectionHandler(): (err: unknown) => Promise<unknown> {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const handlers = (api.interceptors.response as any).handlers as Array<{ rejected: (e: unknown) => Promise<unknown> }>
    const h = handlers.find((x) => x && typeof x.rejected === 'function')
    if (!h) throw new Error('no response rejection interceptor is registered')
    return h.rejected
  }

  function unauthorized() {
    const err = new Error('Unauthorized') as AxiosError
    err.isAxiosError = true
    err.toJSON = () => ({})
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    err.response = { data: {}, status: 401, statusText: 'Unauthorized', headers: {}, config: {} as any }
    return err
  }

  it('redirects to /login on 401', async () => {
    stubLocation('/tickets')
    await expect(rejectionHandler()(unauthorized())).rejects.toBeDefined()
    expect(assigned).toBe('/login')
  })

  // The guard that matters. Without it a 401 raised BY the login page redirects
  // to the login page, which requests again, 401s again — a reload loop that
  // presents as a blank flickering screen with no error anywhere.
  it('does not redirect when already on the login page', async () => {
    stubLocation('/login')
    await expect(rejectionHandler()(unauthorized())).rejects.toBeDefined()
    expect(assigned).toBeUndefined()
  })

  it('does not redirect from a nested login path', async () => {
    stubLocation('/login/reset')
    await expect(rejectionHandler()(unauthorized())).rejects.toBeDefined()
    expect(assigned).toBeUndefined()
  })

  it('leaves non-401 failures alone', async () => {
    stubLocation('/tickets')
    const err = new Error('Server error') as AxiosError
    err.isAxiosError = true
    err.toJSON = () => ({})
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    err.response = { data: {}, status: 500, statusText: 'Server Error', headers: {}, config: {} as any }

    await expect(rejectionHandler()(err)).rejects.toBeDefined()
    expect(assigned).toBeUndefined()
  })

  it('re-rejects so callers still see the failure', async () => {
    // The interceptor must not swallow the error: every caller's catch block
    // and every error toast depends on the rejection propagating.
    stubLocation('/tickets')
    const err = unauthorized()
    await expect(rejectionHandler()(err)).rejects.toBe(err)
  })
})

describe('api instance', () => {
  it('is configured for the versioned API and sends cookies', () => {
    // withCredentials is load-bearing: the session cookie is HttpOnly, so
    // dropping it silently logs every request out.
    expect(api.defaults.baseURL).toBe('/api/v1')
    expect(api.defaults.withCredentials).toBe(true)
  })
})

// Guard against the suite passing because nothing ran.
describe('vitest wiring', () => {
  it('runs in a DOM environment', () => {
    expect(typeof window).toBe('object')
    expect(vi).toBeDefined()
  })
})
