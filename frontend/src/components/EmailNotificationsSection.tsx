import { useId, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { MailIcon } from 'lucide-react'
import { updateEmailPreferences } from '@/api/preferences'
import { extractError } from '@/api/client'
import type { EmailPreferencesPatch } from '@/api/types'
import { useAuthStore } from '@/store/auth'

// The four staff-email switches: the three of #441 and SLA alerts (#670). Each
// saves the moment it is flipped, and only the flipped one is sent, so two tabs
// cannot undo each other.
//
// An administrator can force any switch on or off for everyone from
// System -> Email. The server reports each switch's effective value and
// whether it is forced (email_notifications); a forced switch is shown as it
// applies, locked, and says who decided.

type Key = keyof EmailPreferencesPatch

// Exported so the administrator's override rows can be pinned to these names and
// order (#674, staff-email-mirror.test.ts). Fast refresh is not affected in practice:
// nothing here is edited while the page is open.
// eslint-disable-next-line react-refresh/only-export-components
export const EMAIL_SWITCHES:{ key: Key; state: 'assigned' | 'activity' | 'group' | 'sla'; label: string; help: string }[] = [
  {
    key: 'email_notify_assigned',
    state: 'assigned',
    label: 'Tickets assigned to me',
    help: 'Email me when a ticket is assigned to me.',
  },
  {
    key: 'email_notify_activity',
    state: 'activity',
    label: "Activity on tickets I'm assigned to or follow",
    help: 'Email me when a requester replies on a ticket that is assigned to me or it is reopened, and when a ticket I follow is assigned, its requester replies or it is reopened.',
  },
  {
    key: 'email_notify_group',
    state: 'group',
    label: "My groups' tickets",
    help: 'Email me when a ticket is assigned to a group I belong to, when a requester replies on one of its tickets that nobody has taken yet, and when one of its tickets that nobody has taken yet is reopened.',
  },
  {
    key: 'email_notify_sla',
    state: 'sla',
    label: 'SLA alerts',
    help: 'Email me when a ticket assigned to me, a ticket in a group I belong to that nobody has taken yet, or a ticket I follow has just breached an SLA target or is at risk of breaching one. The three switches above do not apply to these alerts: only this one decides.',
  },
]

export function EmailNotificationsSection() {
  const user = useAuthStore((s) => s.user)
  const setUser = useAuthStore((s) => s.setUser)
  const [error, setError] = useState('')
  const baseId = useId()

  const save = useMutation({
    mutationFn: updateEmailPreferences,
    onSuccess: (saved) => {
      setError('')
      setUser(saved)
    },
    onError: (err) => setError(extractError(err)),
  })

  // A requester is never sent staff mail, so there is nothing to choose.
  if (!user || user.role === 'user') return null

  return (
    <section className="rounded-lg border bg-card">
      <div className="flex items-start gap-3 border-b p-4">
        <div className="mt-0.5 text-fg-faint">
          <MailIcon className="h-4 w-4" />
        </div>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-fg">Email about tickets</h2>
          <p className="mt-0.5 text-xs text-fg-faint">
            Choose which emails you get. Nobody is emailed about something they did themselves. An administrator can
            turn any of these on or off for everyone.
          </p>
        </div>
      </div>
      <ul className="divide-y">
        {EMAIL_SWITCHES.map(({ key, state, label, help }) => {
          const id = `${baseId}-${key}`
          // A profile from before the switches existed has none of them set,
          // and the server's default for each is on.
          const on = user[key] ?? true
          const forced = user.email_notifications?.[state].forced ?? false
          const shown = forced ? (user.email_notifications?.[state].enabled ?? on) : on
          return (
            <li key={key} className="flex items-start gap-3 p-4">
              <input
                id={id}
                type="checkbox"
                className="mt-1 h-4 w-4 rounded border-border-strong"
                checked={shown}
                disabled={save.isPending || forced}
                onChange={(e) => save.mutate({ [key]: e.target.checked })}
              />
              <label htmlFor={id} className="min-w-0 cursor-pointer">
                <span className="block text-sm font-medium text-fg">{label}</span>
                <span className="block text-xs text-fg-faint">{help}</span>
                {forced && <span className="mt-0.5 block text-xs font-medium text-warning-fg">Set by your administrator</span>}
              </label>
            </li>
          )
        })}
      </ul>
      {error && (
        <p role="alert" className="border-t p-4 text-sm text-danger-fg">
          {error}
        </p>
      )}
    </section>
  )
}
