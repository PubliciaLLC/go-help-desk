import { useEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'
import { noticeKey, readDismissed, storeDismissal } from '@/lib/noticeDismissal'

/**
 * failure: broken now (red). config: needs setting up (amber). info: good news
 * or housekeeping (neutral blue): never turns the bar red, is not counted as a
 * problem, and is announced politely.
 */
export type Severity = 'failure' | 'config' | 'info'

export interface Notice {
  id: string
  severity: Severity
  title: string
  /** What the notice says, in a few characters. A change brings a hidden notice back. */
  signature: string
  body: React.ReactNode
  /**
   * Called when the admin hides this notice, after the session dismissal. For
   * a notice that should stay hidden longer than the session (information, not
   * a problem), which keeps its own record.
   */
  onHide?: () => void
}

interface ServerNoticeBarProps {
  notices: Notice[]
  /** Called when Details is opened: the moment an admin checks whether a fix landed. */
  onOpen?: () => void
}

const TINT: Record<Severity, string> = {
  failure: 'border-red-300 bg-red-50 text-red-900',
  config: 'border-amber-300 bg-amber-50 text-amber-950',
  info: 'border-sky-300 bg-sky-50 text-sky-950',
}
const ROW_TINT: Record<Severity, string> = { failure: 'bg-red-50', config: 'bg-amber-50', info: 'bg-sky-50' }

/**
 * One slim bar for every server notice: a summary line, details on request,
 * and a per-session Hide. Hidden notices live in sessionStorage keyed by
 * `noticeKey`; nothing goes to the server.
 */
export function ServerNoticeBar({ notices, onOpen }: ServerNoticeBarProps) {
  const [open, setOpen] = useState(false)
  const [dismissed, setDismissed] = useState<Set<string>>(readDismissed)
  // Index in the visible list to focus once the hidden row is gone.
  const focusIndex = useRef<number | null>(null)
  const regionRef = useRef<HTMLElement>(null)
  const detailsRef = useRef<HTMLButtonElement>(null)

  const visible = notices.filter((n) => !dismissed.has(noticeKey(n)))

  useEffect(() => {
    if (focusIndex.current === null) return
    const hides = regionRef.current?.querySelectorAll<HTMLButtonElement>('[data-hide]')
    // The next Hide button, or Details when there is none after the hidden one.
    ;(hides?.[focusIndex.current] ?? detailsRef.current)?.focus()
    focusIndex.current = null
  }, [dismissed])

  if (visible.length === 0) return null

  const problems = visible.filter((n) => n.severity !== 'info')
  const infos = visible.filter((n) => n.severity === 'info')
  const anyFailure = problems.some((n) => n.severity === 'failure')
  const tint = anyFailure ? 'failure' : problems.length > 0 ? 'config' : 'info'

  let summary: string
  if (problems.length > 1) summary = `${problems.length} server problems need attention`
  else if (problems.length === 1) summary = problems[0].title
  else summary = infos.length === 1 ? infos[0].title : `${infos.length} notices`

  function dismiss(n: Notice, index: number) {
    storeDismissal(noticeKey(n))
    n.onHide?.()
    setDismissed((prev) => new Set(prev).add(noticeKey(n)))
    focusIndex.current = index
  }

  function toggle() {
    if (!open) onOpen?.()
    setOpen(!open)
  }

  return (
    <section
      ref={regionRef}
      aria-label="Server notices"
      className={cn('min-w-0 max-w-full border-b text-sm [overflow-wrap:anywhere]', TINT[tint])}
    >
      <div className="flex min-w-0 items-center gap-2 px-3 py-1.5">
        {/* Only this line is a live region: the details are read on request. */}
        <span
          role={problems.length > 0 ? 'alert' : 'status'}
          className="flex min-w-0 flex-1 items-center gap-2"
        >
          <span aria-hidden="true">{problems.length > 0 ? '⚠' : 'ℹ'}</span>
          <span className="min-w-0 flex-1 truncate font-medium">{summary}</span>
        </span>
        <button
          ref={detailsRef}
          type="button"
          aria-expanded={open}
          aria-controls="server-notices"
          onClick={toggle}
          className="shrink-0 rounded px-2 py-1 font-medium underline focus-visible:outline focus-visible:outline-2"
        >
          Details
        </button>
      </div>
      <ul id="server-notices" hidden={!open} className="max-h-[60dvh] min-w-0 overflow-y-auto border-t border-black/10">
        {visible.map((n, i) => (
          <li
            key={noticeKey(n)}
            className={cn(
              'flex min-w-0 items-start gap-2 border-b border-black/10 px-3 py-2 last:border-b-0',
              ROW_TINT[n.severity],
            )}
          >
            <div className="min-w-0 flex-1">{n.body}</div>
            <button
              type="button"
              data-hide
              aria-label={`Hide for this session: ${n.title}`}
              onClick={() => dismiss(n, i)}
              className="shrink-0 rounded px-2 py-1 text-xs font-medium underline focus-visible:outline focus-visible:outline-2 pointer-coarse:min-h-11 pointer-coarse:min-w-11"
            >
              Hide
            </button>
          </li>
        ))}
      </ul>
    </section>
  )
}
