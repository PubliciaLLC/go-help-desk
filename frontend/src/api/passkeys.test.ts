import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { api } from './client'
import { addPasskey, signInWithPasskey, b64urlToBytes, bytesToB64url, wasCancelled } from './passkeys'

// The browser's credentials API speaks ArrayBuffer and the server speaks
// base64url in JSON. Everything here is about that boundary, because a
// mistake in it fails as "the ceremony was refused" with no clue which side
// got the bytes wrong — the challenge simply doesn't match and the library
// declines to say more (see passkeyCeremonyError in handler_passkeys.go).

const CHALLENGE = 'AQIDBA' //  bytes 1,2,3,4, base64url, unpadded as Go emits it
const USER_ID = 'BQYHCA' //   bytes 5,6,7,8

function bytes(...n: number[]) {
  return new Uint8Array(n)
}

/** A credential shaped like the one navigator.credentials returns. */
function fakeCredential(over: Record<string, unknown> = {}) {
  return {
    id: 'Y3JlZC1pZA',
    rawId: bytes(9, 10).buffer,
    type: 'public-key',
    response: {
      clientDataJSON: bytes(11, 12).buffer,
      attestationObject: bytes(13, 14).buffer,
      getTransports: () => ['internal', 'hybrid'],
    },
    ...over,
  }
}

let create: ReturnType<typeof vi.fn>
let get: ReturnType<typeof vi.fn>

beforeEach(() => {
  create = vi.fn().mockResolvedValue(fakeCredential())
  get = vi.fn()
  vi.stubGlobal('navigator', { ...navigator, credentials: { create, get } })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('base64url', () => {
  it('decodes what Go emits, which has no padding', () => {
    expect(Array.from(b64urlToBytes(CHALLENGE))).toEqual([1, 2, 3, 4])
    // One byte and two bytes are the lengths that need one and two pad
    // characters respectively; atob rejects both unpadded.
    expect(Array.from(b64urlToBytes('_w'))).toEqual([255])
    expect(Array.from(b64urlToBytes('__4'))).toEqual([255, 254])
  })

  it('uses the url alphabet, not the standard one', () => {
    // 0xFF 0xFE is "//4=" in standard base64 and "__4" in base64url. A
    // decoder that forgot the substitution throws on the first; an encoder
    // that forgot it emits characters the server will not accept in JSON.
    expect(Array.from(b64urlToBytes('__4'))).toEqual([255, 254])
    expect(bytesToB64url(bytes(255, 254).buffer)).toBe('__4')
    expect(bytesToB64url(bytes(255, 254).buffer)).not.toContain('=')
  })

  it('round-trips every byte value', () => {
    const all = new Uint8Array(256)
    for (let i = 0; i < 256; i++) all[i] = i
    expect(Array.from(b64urlToBytes(bytesToB64url(all.buffer)))).toEqual(Array.from(all))
  })
})

describe('registering a passkey', () => {
  it('hands the browser real bytes, and sends the name in the query string', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {
      publicKey: {
        challenge: CHALLENGE,
        rp: { id: 'localhost', name: 'Help Desk' },
        user: { id: USER_ID, name: 'sam@example.com', displayName: 'Sam' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        excludeCredentials: [{ type: 'public-key', id: CHALLENGE }],
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } } as any)

    await addPasskey('Work laptop')

    // What went to the browser: bytes, not the strings the server sent.
    const opts = create.mock.calls[0][0].publicKey
    expect(Array.from(new Uint8Array(opts.challenge))).toEqual([1, 2, 3, 4])
    expect(Array.from(new Uint8Array(opts.user.id))).toEqual([5, 6, 7, 8])
    expect(Array.from(new Uint8Array(opts.excludeCredentials[0].id))).toEqual([1, 2, 3, 4])
    // Everything else is passed through untouched.
    expect(opts.rp).toEqual({ id: 'localhost', name: 'Help Desk' })
    expect(opts.pubKeyCredParams).toEqual([{ type: 'public-key', alg: -7 }])

    // What went back to the server: base64url strings, and the name as a
    // query parameter because that is where the handler reads it from.
    const [url, body] = post.mock.calls[1]
    expect(url).toBe('/me/passkeys/register/finish?name=Work%20laptop')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const b = body as any
    expect(b.rawId).toBe('CQo')
    expect(b.type).toBe('public-key')
    expect(b.response.clientDataJSON).toBe('Cww')
    expect(b.response.attestationObject).toBe('DQ4')
    expect(b.response.transports).toEqual(['internal', 'hybrid'])
  })

  it('survives an authenticator with no getTransports', async () => {
    // Older authenticators and some browsers do not implement it. Calling it
    // unconditionally throws, and the passkey is then lost after the person
    // has already touched their key.
    const noTransports = fakeCredential({
      response: {
        clientDataJSON: bytes(11).buffer,
        attestationObject: bytes(13).buffer,
      },
    })
    create.mockResolvedValue(noTransports)
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {
      publicKey: { challenge: CHALLENGE, rp: {}, user: { id: USER_ID }, pubKeyCredParams: [] },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } } as any)

    await addPasskey('Key')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    expect((post.mock.calls[1][1] as any).response.transports).toEqual([])
  })
})

describe('signing in with a passkey', () => {
  it('sends the assertion, including a null user handle', async () => {
    get.mockResolvedValue({
      id: 'Y3JlZC1pZA',
      rawId: bytes(9, 10).buffer,
      type: 'public-key',
      response: {
        clientDataJSON: bytes(11, 12).buffer,
        authenticatorData: bytes(15, 16).buffer,
        signature: bytes(17, 18).buffer,
        userHandle: null,
      },
    })
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {
      publicKey: {
        challenge: CHALLENGE,
        allowCredentials: [{ type: 'public-key', id: USER_ID, transports: ['usb'] }],
      },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } } as any)

    await signInWithPasskey()

    const opts = get.mock.calls[0][0].publicKey
    expect(Array.from(new Uint8Array(opts.challenge))).toEqual([1, 2, 3, 4])
    expect(Array.from(new Uint8Array(opts.allowCredentials[0].id))).toEqual([5, 6, 7, 8])
    expect(opts.allowCredentials[0].transports).toEqual(['usb'])

    const [url, body] = post.mock.calls[1]
    expect(url).toBe('/auth/local/passkey/finish')
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const b = body as any
    expect(b.response.authenticatorData).toBe('DxA')
    expect(b.response.signature).toBe('ERI')
    // Null, not the string "null" and not an empty string: the server
    // unmarshals this into a byte slice and "" is a zero-length handle,
    // which is a different claim from "the authenticator sent none".
    expect(b.response.userHandle).toBeNull()
  })
})

describe('wasCancelled', () => {
  it('recognises the person dismissing the browser prompt', () => {
    // Chrome, Safari and Firefox all report a dismissed or timed-out prompt
    // as NotAllowedError. It is not a failure worth showing as an error.
    expect(wasCancelled(new DOMException('', 'NotAllowedError'))).toBe(true)
    expect(wasCancelled(new DOMException('', 'AbortError'))).toBe(true)
    expect(wasCancelled(new DOMException('', 'InvalidStateError'))).toBe(false)
    expect(wasCancelled(new Error('network'))).toBe(false)
  })
})
