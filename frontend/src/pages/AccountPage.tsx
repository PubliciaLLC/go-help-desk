import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRoundIcon, SmartphoneIcon, LockIcon, TrashIcon } from 'lucide-react'
import { changePassword, enrollMFAStart, enrollMFAConfirm } from '@/api/auth'
import {
  addPasskey,
  listPasskeys,
  removePasskey,
  browserSupportsPasskeys,
  wasCancelled,
  type Passkey,
} from '@/api/passkeys'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'
import { useAuthStore } from '@/store/auth'

// Everything a signed-in person can change about their own sign-in, in one
// place. Before this existed there was nowhere to do any of it: enrolment
// happened only as a forced step mid-login, and `changePassword` had sat in
// the API client with no caller at all, against a working endpoint.

function Section({
  icon,
  title,
  description,
  children,
}: {
  icon: React.ReactNode
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <section className="rounded-lg border bg-white">
      <div className="flex items-start gap-3 border-b p-4">
        <div className="mt-0.5 text-gray-400">{icon}</div>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-gray-900">{title}</h2>
          <p className="mt-0.5 text-xs text-gray-500">{description}</p>
        </div>
      </div>
      <div className="p-4">{children}</div>
    </section>
  )
}

function Notice({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  if (!children) return null
  return (
    <p
      role={kind === 'error' ? 'alert' : 'status'}
      className={
        kind === 'error'
          ? 'mt-3 text-sm text-red-600'
          : 'mt-3 text-sm text-green-700'
      }
    >
      {children}
    </p>
  )
}

// ── Password ──────────────────────────────────────────────────────────────────

function PasswordSection() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [done, setDone] = useState(false)

  const change = useMutation({
    mutationFn: () => changePassword(current, next),
    onSuccess: () => {
      setDone(true)
      setCurrent('')
      setNext('')
      setConfirm('')
    },
    onError: (err) => setError(extractError(err)),
  })

  function submit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setDone(false)
    // Caught here rather than at the server, which has no way to know what
    // was typed the second time.
    if (next !== confirm) {
      setError('The new passwords do not match.')
      return
    }
    change.mutate()
  }

  return (
    <Section
      icon={<LockIcon className="h-4 w-4" />}
      title="Password"
      description="Only applies to accounts that sign in with a password. If you sign in through your organisation, change it there."
    >
      <form onSubmit={submit} className="max-w-sm space-y-3">
        <div>
          <label htmlFor="current-password" className="mb-1 block text-xs font-medium text-gray-700">
            Current password
          </label>
          <Input
            id="current-password"
            type="password"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            required
          />
        </div>
        <div>
          <label htmlFor="new-password" className="mb-1 block text-xs font-medium text-gray-700">
            New password
          </label>
          <Input
            id="new-password"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            required
          />
        </div>
        <div>
          <label htmlFor="confirm-password" className="mb-1 block text-xs font-medium text-gray-700">
            Confirm new password
          </label>
          <Input
            id="confirm-password"
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            required
          />
        </div>
        <Button type="submit" disabled={change.isPending}>
          {change.isPending ? 'Changing…' : 'Change password'}
        </Button>
        <Notice kind="error">{error}</Notice>
        {done && <Notice kind="ok">Password changed.</Notice>}
      </form>
    </Section>
  )
}

// ── Passkeys ──────────────────────────────────────────────────────────────────

/**
 * How a key is described when its owner did not name it.
 *
 * Its transports and its age, never its AAGUID: the model is recorded at
 * registration and putting it on screen would turn "a key" into "a YubiKey 5
 * NFC", which is a detail about the person's hardware that the list does not
 * need to disclose.
 */
function describe(p: Passkey): string {
  if (p.name) return p.name
  const t = p.transports?.length ? p.transports.join(', ') : 'unknown'
  return `Unnamed key (${t})`
}

function PasskeyRow({ p, onRemove }: { p: Passkey; onRemove: (p: Passkey) => void }) {
  return (
    <li className="flex flex-wrap items-center gap-x-3 gap-y-1 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-gray-900">{describe(p)}</span>
          {/* backup_eligible, not backup_state: a key that CAN sync and
              currently is not is still a synced credential, and that is the
              distinction the phishing-resistance claim turns on. */}
          <Badge variant={p.backup_eligible ? 'secondary' : 'success'}>
            {p.backup_eligible ? 'Synced' : 'This device only'}
          </Badge>
        </div>
        <p className="mt-0.5 text-xs text-gray-500">
          Added {new Date(p.created_at).toLocaleDateString()}
          {' · '}
          {p.last_used_at ? `last used ${new Date(p.last_used_at).toLocaleDateString()}` : 'never used'}
        </p>
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="text-red-600 hover:bg-red-50 hover:text-red-700"
        onClick={() => onRemove(p)}
        aria-label={`Remove ${describe(p)}`}
      >
        <TrashIcon className="h-4 w-4" />
      </Button>
    </li>
  )
}

function PasskeySection() {
  const qc = useQueryClient()
  const [name, setName] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState<Passkey | null>(null)
  const supported = browserSupportsPasskeys()

  const passkeys = useQuery({ queryKey: ['passkeys'], queryFn: listPasskeys, enabled: supported })

  const add = useMutation({
    mutationFn: () => addPasskey(name.trim()),
    onSuccess: () => {
      setName('')
      qc.invalidateQueries({ queryKey: ['passkeys'] })
    },
    onError: (err) => {
      // Dismissing the browser's prompt is a choice, not a failure. Showing
      // "The operation either timed out or was not allowed" for it reads as
      // something being broken.
      if (wasCancelled(err)) return
      setError(extractError(err))
    },
  })

  const remove = useMutation({
    mutationFn: (p: Passkey) => removePasskey(p.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['passkeys'] }),
    onError: (err) => setError(extractError(err)),
  })

  if (!supported) {
    return (
      <Section
        icon={<KeyRoundIcon className="h-4 w-4" />}
        title="Passkeys"
        description="Sign in with your fingerprint, face, screen lock or a security key."
      >
        <p className="text-sm text-gray-500">
          This browser does not support passkeys.
        </p>
      </Section>
    )
  }

  const list = passkeys.data ?? []

  return (
    <Section
      icon={<KeyRoundIcon className="h-4 w-4" />}
      title="Passkeys"
      description="Sign in with your fingerprint, face, screen lock or a security key."
    >
      {passkeys.isLoading ? (
        <Spinner />
      ) : list.length === 0 ? (
        <p className="text-sm text-gray-500">No passkeys yet.</p>
      ) : (
        <ul className="divide-y">
          {list.map((p) => (
            <PasskeyRow key={p.id} p={p} onRemove={setPending} />
          ))}
        </ul>
      )}

      <form
        className="mt-4 flex flex-wrap items-end gap-2 border-t pt-4"
        onSubmit={(e) => {
          e.preventDefault()
          setError('')
          add.mutate()
        }}
      >
        <div className="min-w-[12rem] flex-1">
          <label htmlFor="passkey-name" className="mb-1 block text-xs font-medium text-gray-700">
            Name this key
          </label>
          <Input
            id="passkey-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Work laptop"
          />
        </div>
        <Button type="submit" disabled={add.isPending}>
          {add.isPending ? 'Waiting for your key…' : 'Add a passkey'}
        </Button>
      </form>
      <Notice kind="error">{error}</Notice>

      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(open) => !open && setPending(null)}
        title="Remove this passkey?"
        description={
          pending
            ? `"${describe(pending)}" will stop working for signing in. This cannot be undone.`
            : undefined
        }
        confirmLabel="Remove"
        destructive
        onConfirm={() => {
          if (pending) remove.mutate(pending)
          setPending(null)
        }}
      />
    </Section>
  )
}

// ── Authenticator app ─────────────────────────────────────────────────────────

function AuthenticatorSection() {
  const user = useAuthStore((s) => s.user)
  const setUser = useAuthStore((s) => s.setUser)
  const [starting, setStarting] = useState(false)
  const [secret, setSecret] = useState('')
  const [qr, setQR] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')

  const confirm = useMutation({
    mutationFn: () => enrollMFAConfirm(code),
    onSuccess: () => {
      setSecret('')
      setQR('')
      setCode('')
      if (user) setUser({ ...user, mfa_enabled: true })
    },
    onError: (err) => setError(extractError(err)),
  })

  async function start() {
    setError('')
    setStarting(true)
    try {
      const { secret, qr_data_url } = await enrollMFAStart()
      setSecret(secret)
      setQR(qr_data_url)
    } catch (err) {
      setError(extractError(err))
    } finally {
      setStarting(false)
    }
  }

  return (
    <Section
      icon={<SmartphoneIcon className="h-4 w-4" />}
      title="Authenticator app"
      description="A six-digit code from an app on your phone."
    >
      {user?.mfa_enabled ? (
        <div className="flex flex-wrap items-center gap-3">
          <Badge variant="success">Set up</Badge>
          {/* There is deliberately no button here. Nothing in the API removes
              an authenticator, because an account's second factor should not
              be strippable by whoever is holding the session. An
              administrator resets it with `go-help-desk reset-factors`. */}
          <p className="text-xs text-gray-500">
            To replace it, ask an administrator to reset your second factor.
          </p>
        </div>
      ) : secret ? (
        <form
          className="max-w-sm space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            setError('')
            confirm.mutate()
          }}
        >
          <p className="text-sm text-gray-600">Scan this with your authenticator app:</p>
          {qr && <img src={qr} alt="Scan this code with your authenticator app" className="h-44 w-44" />}
          <p className="text-xs text-gray-500">
            Or enter this code by hand: <code className="font-mono">{secret}</code>
          </p>
          <div>
            <label htmlFor="totp-code" className="mb-1 block text-xs font-medium text-gray-700">
              Enter the six-digit code to finish
            </label>
            <Input
              id="totp-code"
              inputMode="numeric"
              autoComplete="one-time-code"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              required
            />
          </div>
          <Button type="submit" disabled={confirm.isPending}>
            {confirm.isPending ? 'Checking…' : 'Finish setup'}
          </Button>
        </form>
      ) : (
        <Button variant="outline" onClick={start} disabled={starting}>
          {starting ? 'Starting…' : 'Set up an authenticator app'}
        </Button>
      )}
      <Notice kind="error">{error}</Notice>
    </Section>
  )
}

export function AccountPage() {
  const user = useAuthStore((s) => s.user)

  return (
    <Layout>
      <div className="space-y-6">
        <div>
          <h1 className="text-xl font-semibold text-gray-900">Your account</h1>
          <p className="mt-1 text-sm text-gray-500">{user?.email}</p>
        </div>
        <PasswordSection />
        <PasskeySection />
        <AuthenticatorSection />
      </div>
    </Layout>
  )
}
