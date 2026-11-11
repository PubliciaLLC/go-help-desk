import { LockIcon, MessageSquareIcon } from 'lucide-react'
import type { ReplyMatch } from '@/api/types'
import { splitSnippet } from '@/lib/replySnippet'

// "3 Nov", with the year once it is not this one. The viewer's own locale and
// timezone, like every other date on the page.
function shortDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const sameYear = d.getFullYear() === new Date().getFullYear()
  return d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', ...(sameYear ? {} : { year: 'numeric' }) })
}

// The line under a subject on a ticket found through its conversation: where the
// words were, when, and the words highlighted. Never who wrote it.
export function ReplyMatchLine({ match }: { match: ReplyMatch }) {
  const when = shortDate(match.created_at)
  const label = `${match.internal ? 'Internal note' : 'Public reply'}${when ? ` · ${when}` : ''}`
  const Icon = match.internal ? LockIcon : MessageSquareIcon
  return (
    <div className="mt-1 flex items-baseline gap-1.5 text-xs font-normal text-gray-600">
      <Icon aria-hidden="true" className="relative top-0.5 h-3 w-3 shrink-0 text-gray-400" />
      <span
        className={
          'whitespace-nowrap ' +
          (match.internal ? 'rounded border border-amber-300 bg-amber-50 px-1 text-amber-800' : 'text-gray-500')
        }
      >
        {label}
      </span>
      <span className="min-w-0 wrap-anywhere">
        {splitSnippet(match.snippet).map((p, i) =>
          p.match ? (
            <mark key={i} className="rounded-sm bg-yellow-200 px-px text-inherit">
              {p.text}
            </mark>
          ) : (
            <span key={i}>{p.text}</span>
          ),
        )}
      </span>
    </div>
  )
}
