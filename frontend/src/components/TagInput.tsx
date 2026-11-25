import { useState, useRef, useEffect } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { listTicketTags, addTicketTag, removeTicketTag, searchTags } from '@/api/tickets'
import { XIcon } from 'lucide-react'
import type { Tag } from '@/api/types'

interface TagInputProps {
  ticketId: string
  readonly?: boolean
}

export function TagInput({ ticketId, readonly = false }: TagInputProps) {
  const qc = useQueryClient()
  const [input, setInput] = useState('')
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const containerRef = useRef<HTMLDivElement>(null)

  const { data: ticketTags = [] } = useQuery({
    queryKey: ['ticket-tags', ticketId],
    queryFn: () => listTicketTags(ticketId),
  })

  const { data: suggestions = [] } = useQuery({
    queryKey: ['tag-search', input],
    queryFn: () => searchTags(input),
    enabled: input.length > 0,
  })

  const addMutation = useMutation({
    mutationFn: (name: string) => addTicketTag(ticketId, name),
    onSuccess: () => {
      setInput('')
      setOpen(false)
      setError('')
      qc.invalidateQueries({ queryKey: ['ticket-tags', ticketId] })
    },
    onError: (err: { message?: string; response?: { data?: { error?: { message?: string } } } }) => {
      const msg = err?.response?.data?.error?.message ?? err?.message ?? 'Failed to add tag'
      setError(msg)
    },
  })

  const removeMutation = useMutation({
    mutationFn: (tagId: string) => removeTicketTag(ticketId, tagId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['ticket-tags', ticketId] }),
  })

  // Close dropdown on outside click
  useEffect(() => {
    function handle(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handle)
    return () => document.removeEventListener('mousedown', handle)
  }, [])

  const existingIds = new Set(ticketTags.map((t) => t.id))
  const filtered = suggestions.filter((s) => !existingIds.has(s.id))

  function submit(name: string) {
    const n = name.trim().toLowerCase()
    if (!n) return
    addMutation.mutate(n)
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') {
      e.preventDefault()
      submit(input)
    } else if (e.key === 'Escape') {
      setOpen(false)
      setInput('')
    }
  }

  return (
    <div className="space-y-2">
      {/* Existing tags */}
      <div className="flex flex-wrap gap-1.5">
        {ticketTags.map((t: Tag) => (
          <span
            key={t.id}
            className="inline-flex max-w-full items-center gap-1 rounded-full bg-selected-strong px-2.5 py-0.5 text-xs font-medium text-info-fg-strong"
          >
            <span className="min-w-0 wrap-anywhere">{t.name}</span>
            {!readonly && (
              <button
                onClick={() => removeMutation.mutate(t.id)}
                className="ml-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full hover:bg-primary/20"
                aria-label={`Remove tag ${t.name}`}
              >
                <XIcon className="h-3 w-3" />
              </button>
            )}
          </span>
        ))}
        {ticketTags.length === 0 && readonly && (
          <span className="text-xs text-fg-faint">No tags</span>
        )}
      </div>

      {/* Input */}
      {!readonly && (
        <div ref={containerRef} className="relative">
          <input
            type="text"
            value={input}
            onChange={(e) => {
              setInput(e.target.value)
              setOpen(true)
              setError('')
            }}
            onFocus={() => setOpen(true)}
            onKeyDown={handleKeyDown}
            placeholder="Add tag…"
            className="h-8 w-full rounded-md border border-border bg-background px-3 text-xs text-fg placeholder:text-fg-faint focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
          />

          {/* Dropdown */}
          {open && filtered.length > 0 && (
            <ul className="absolute z-10 mt-1 w-full rounded-md border bg-card py-1 shadow-lg text-xs">
              {filtered.map((s) => (
                <li
                  key={s.id}
                  className="cursor-pointer px-3 py-1.5 hover:bg-selected text-fg-soft"
                  onMouseDown={(e) => {
                    e.preventDefault()
                    submit(s.name)
                  }}
                >
                  {s.name}
                </li>
              ))}
            </ul>
          )}

          {/* "Press Enter to create" hint */}
          {open && input.trim() && filtered.length === 0 && (
            <div className="absolute z-10 mt-1 w-full rounded-md border bg-card px-3 py-2 shadow-lg text-xs text-fg-faint">
              Press Enter to create &ldquo;{input.trim().toLowerCase()}&rdquo;
            </div>
          )}
        </div>
      )}

      {error && <p role="alert" className="text-xs text-danger-fg">{error}</p>}
    </div>
  )
}
