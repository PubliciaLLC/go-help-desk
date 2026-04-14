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
            className="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2.5 py-0.5 text-xs font-medium text-blue-800"
          >
            {t.name}
            {!readonly && (
              <button
                onClick={() => removeMutation.mutate(t.id)}
                className="ml-0.5 rounded-full hover:bg-blue-200 p-0.5"
                aria-label={`Remove tag ${t.name}`}
              >
                <XIcon className="h-3 w-3" />
              </button>
            )}
          </span>
        ))}
        {ticketTags.length === 0 && readonly && (
          <span className="text-xs text-gray-400">No tags</span>
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
            className="h-8 w-full rounded-md border border-gray-200 bg-white px-3 text-xs text-gray-800 placeholder-gray-400 focus:border-blue-400 focus:outline-none focus:ring-1 focus:ring-blue-400"
          />

          {/* Dropdown */}
          {open && filtered.length > 0 && (
            <ul className="absolute z-10 mt-1 w-full rounded-md border bg-white py-1 shadow-lg text-xs">
              {filtered.map((s) => (
                <li
                  key={s.id}
                  className="cursor-pointer px-3 py-1.5 hover:bg-blue-50 text-gray-700"
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
            <div className="absolute z-10 mt-1 w-full rounded-md border bg-white px-3 py-2 shadow-lg text-xs text-gray-500">
              Press Enter to create &ldquo;{input.trim().toLowerCase()}&rdquo;
            </div>
          )}
        </div>
      )}

      {error && <p className="text-xs text-red-600">{error}</p>}
    </div>
  )
}
