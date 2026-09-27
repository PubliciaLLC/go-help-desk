import { api } from './client'

/**
 * One registered authenticator, as GET /me/passkeys returns it.
 *
 * The credential id, public key, sign count and AAGUID are all marked
 * `json:"-"` on the server and deliberately never reach here: none of them
 * says anything to the person who owns the key. What is left is what a
 * management list can actually show.
 */
export interface Passkey {
  id: string
  name: string
  transports: string[] | null
  /** True for a key that syncs through a cloud keychain rather than living on one device. */
  backup_eligible: boolean
  backup_state: boolean
  created_at: string
  last_used_at: string | null
}

// ── The byte boundary ─────────────────────────────────────────────────────────
//
// The browser's credentials API speaks ArrayBuffer; the server speaks
// base64url inside JSON. These two functions are the entire translation, and
// they are exported so they can be tested directly — a mistake here surfaces
// only as "the ceremony was refused", because a challenge that decoded wrong
// simply doesn't match and the library declines to explain further.

/**
 * Decodes base64url to bytes.
 *
 * Only the alphabet is translated, not the padding. Go's encoder emits
 * base64url with the padding stripped, and `atob` accepts that: it implements
 * the forgiving-base64 decode, which strips any padding present and refuses
 * only a length leaving remainder 1 — never a valid base64 length. Restoring
 * the `=` first is a line that cannot be made to matter, so it is not here.
 */
export function b64urlToBytes(s: string): Uint8Array<ArrayBuffer> {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(b64)
  const out = new Uint8Array(new ArrayBuffer(bin.length))
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

/** Encodes bytes as unpadded base64url, which is what the server will parse. */
export function bytesToB64url(buf: ArrayBuffer): string {
  const b = new Uint8Array(buf)
  let bin = ''
  for (let i = 0; i < b.length; i++) bin += String.fromCharCode(b[i])
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/**
 * Whether a failed ceremony was the person dismissing the browser's prompt
 * rather than something going wrong.
 *
 * Every browser reports a dismissed prompt, and a prompt that timed out
 * waiting for a key, as NotAllowedError. Showing that as an error message is
 * wrong: they chose to stop.
 */
export function wasCancelled(err: unknown): boolean {
  return err instanceof DOMException && (err.name === 'NotAllowedError' || err.name === 'AbortError')
}

/** Whether this browser can do WebAuthn at all. */
export function browserSupportsPasskeys(): boolean {
  return typeof window !== 'undefined' && typeof window.PublicKeyCredential !== 'undefined'
}

// The server's options, before the byte fields are decoded. Only the fields
// that hold bytes are named; everything else is handed to the browser as it
// arrived, so a server-side change to timeout or authenticatorSelection needs
// no change here.
interface DescriptorJSON {
  id: string
  type: 'public-key'
  transports?: AuthenticatorTransport[]
}
interface CreationOptionsJSON {
  publicKey: {
    challenge: string
    user: { id: string; name: string; displayName: string }
    excludeCredentials?: DescriptorJSON[]
    [k: string]: unknown
  }
}
interface RequestOptionsJSON {
  publicKey: {
    challenge: string
    allowCredentials?: DescriptorJSON[]
    [k: string]: unknown
  }
}

/**
 * The fields of an options object that hold no bytes and are forwarded
 * untouched — everything except the ones decoded by hand below.
 */
type PassThrough<T> = Omit<T, 'challenge' | 'user' | 'excludeCredentials' | 'allowCredentials'>

function decodeDescriptors(ds?: DescriptorJSON[]): PublicKeyCredentialDescriptor[] | undefined {
  return ds?.map((d) => ({ ...d, id: b64urlToBytes(d.id) }))
}

// ── Managing your own passkeys ────────────────────────────────────────────────

export async function listPasskeys(): Promise<Passkey[]> {
  const res = await api.get<Passkey[] | null>('/me/passkeys')
  // An account with no passkeys comes back as JSON null, not [].
  return res.data ?? []
}

/**
 * Runs the registration ceremony and stores the result under `name`.
 *
 * The name goes in the query string because that is where the handler reads
 * it from: the request body is the attestation, which the WebAuthn library
 * parses whole and would reject with an extra field in it.
 */
export async function addPasskey(name: string): Promise<void> {
  const start = await api.post<CreationOptionsJSON>('/me/passkeys/register/start')
  const { challenge, user, excludeCredentials, ...rest } = start.data.publicKey
  const options: PublicKeyCredentialCreationOptions = {
    // Everything that is not bytes is handed over as it arrived, so a
    // server-side change to timeout or authenticatorSelection needs no
    // change here.
    ...(rest as PassThrough<PublicKeyCredentialCreationOptions>),
    challenge: b64urlToBytes(challenge),
    user: { ...user, id: b64urlToBytes(user.id) },
    excludeCredentials: decodeDescriptors(excludeCredentials),
  }

  const cred = (await navigator.credentials.create({ publicKey: options })) as PublicKeyCredential | null
  if (!cred) throw new Error('no passkey was created')
  const res = cred.response as AuthenticatorAttestationResponse

  await api.post(`/me/passkeys/register/finish?name=${encodeURIComponent(name)}`, {
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bytesToB64url(res.clientDataJSON),
      attestationObject: bytesToB64url(res.attestationObject),
      // Not every authenticator implements it, and calling it unconditionally
      // throws — losing the credential after the person has already touched
      // their key, with nothing to show for it.
      transports: typeof res.getTransports === 'function' ? res.getTransports() : [],
    },
  })
}

export async function removePasskey(id: string): Promise<void> {
  await api.delete(`/me/passkeys/${id}`)
}

// ── Signing in with one ───────────────────────────────────────────────────────

/**
 * Proves the key, for a session that has already passed the password.
 *
 * This is the second factor, not a way in: the session already names the
 * account, and all this does is satisfy the gate in front of it.
 */
export async function signInWithPasskey(): Promise<void> {
  const start = await api.post<RequestOptionsJSON>('/auth/local/passkey/start')
  const { challenge, allowCredentials, ...rest } = start.data.publicKey
  const options: PublicKeyCredentialRequestOptions = {
    ...(rest as PassThrough<PublicKeyCredentialRequestOptions>),
    challenge: b64urlToBytes(challenge),
    allowCredentials: decodeDescriptors(allowCredentials),
  }

  const cred = (await navigator.credentials.get({ publicKey: options })) as PublicKeyCredential | null
  if (!cred) throw new Error('no passkey was offered')
  const res = cred.response as AuthenticatorAssertionResponse

  await api.post('/auth/local/passkey/finish', {
    id: cred.id,
    rawId: bytesToB64url(cred.rawId),
    type: cred.type,
    response: {
      clientDataJSON: bytesToB64url(res.clientDataJSON),
      authenticatorData: bytesToB64url(res.authenticatorData),
      signature: bytesToB64url(res.signature),
      // Null when the authenticator sent none, which is not the same claim as
      // a handle of zero length.
      userHandle: res.userHandle ? bytesToB64url(res.userHandle) : null,
    },
  })
}
