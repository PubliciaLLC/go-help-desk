import { useId, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { MailIcon } from 'lucide-react'
import { updateEmailPreferences } from '@/api/preferences'
import { extractError } from '@/api/client'
import type { EmailPreferencesPatch } from '@/api/types'
import { useAuthStore } from '@/store/auth'

// The three staff-email switches (#441). Each saves the moment it is flipped,
// and only the flipped one is sent, so two tabs cannot undo each other.
//
// An administrator can force any switch on or off for everyone from
// Settings -> Email. Staff cannot read those settings, so this page cannot show
// when one is forced; it says so in general instead.

type Key = keyof EmailPreferencesPatch

const SWITCHES: { key: Key; label: string; help: string }[] = [
  {
    key: 'email_notify_assigned',
    label: 'Tickets assigned to me',
    help: 'Email me when a ticket is assigned to me.',
  },
  {
    key: 'email_notify_activity',
    label: 'Activity on my tickets',
    help: 'Email me when a requester replies on a ticket that is assigned to me.',
  },
  {
    key: 'email_notify_group',
    label: "My groups' tickets",
    help: 'Email me when a ticket is assigned to a group I belong to, and when a requester replies on one of its tickets that nobody has taken yet.',
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
    <section className="rounded-lg border bg-white">
      <div className="flex items-start gap-3 border-b p-4">
        <div className="mt-0.5 text-gray-400">
          <MailIcon className="h-4 w-4" />
        </div>
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-gray-900">Email about tickets</h2>
          <p className="mt-0.5 text-xs text-gray-500">
            Choose which emails you get. Nobody is emailed about something they did themselves. An administrator can
            turn any of these on or off for everyone.
          </p>
        </div>
      </div>
      <ul className="divide-y">
        {SWITCHES.map(({ key, label, help }) => {
          const id = `${baseId}-${key}`
          // A profile from before the switches existed has none of them set,
          // and the server's default for each is on.
          const on = user[key] ?? true
          return (
            <li key={key} className="flex items-start gap-3 p-4">
              <input
                id={id}
                type="checkbox"
                className="mt-1 h-4 w-4 rounded border-gray-300"
                checked={on}
                disabled={save.isPending}
                onChange={(e) => save.mutate({ [key]: e.target.checked })}
              />
              <label htmlFor={id} className="min-w-0 cursor-pointer">
                <span className="block text-sm font-medium text-gray-900">{label}</span>
                <span className="block text-xs text-gray-500">{help}</span>
              </label>
            </li>
          )
        })}
      </ul>
      {error && (
        <p role="alert" className="border-t p-4 text-sm text-red-600">
          {error}
        </p>
      )}
    </section>
  )
}
