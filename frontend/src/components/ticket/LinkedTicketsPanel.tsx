import { useState, useRef, useEffect, useMemo } from 'react'
import { useQuery, useMutation, useQueryClient, useQueries } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { listLinks, addLink, removeLink, listTickets, getTicket, duplicateResolutionNotes, addDuplicateLinkAndResolve } from '@/api/tickets'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Select } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { XIcon } from 'lucide-react'
import type { TicketLink, LinkType, Ticket } from '@/api/types'

interface LinkedTicketsPanelProps {
  ticketId: string
}

// Keyed on LinkType so a missing value fails TypeScript compilation.
const LABELS: Record<LinkType, { asSource: string; asTarget: string }> = {
  related_to: { asSource: 'Related to', asTarget: 'Related to' },
  parent_child: { asSource: 'Child', asTarget: 'Parent' },
  caused_by: { asSource: 'Caused by', asTarget: 'Cause of' },
  duplicate_of: { asSource: 'Duplicate of', asTarget: 'Duplicated by' },
}

// Options for the relation select: UI labels and their backend mapping
const RELATION_OPTIONS = [
  { key: 'related_to', label: 'Related to', link_type: 'related_to' as const, reversed: false },
  { key: 'parent_of', label: 'Parent of', link_type: 'parent_child' as const, reversed: false },
  { key: 'child_of', label: 'Child of', link_type: 'parent_child' as const, reversed: true },
  { key: 'caused_by', label: 'Caused by', link_type: 'caused_by' as const, reversed: false },
  { key: 'duplicate_of', label: 'Duplicate of', link_type: 'duplicate_of' as const, reversed: false },
] as const

function getLabel(link: TicketLink, viewedTicketId: string): string {
  const isSource = link.source_id === viewedTicketId
  return LABELS[link.link_type][isSource ? 'asSource' : 'asTarget']
}

export function LinkedTicketsPanel({ ticketId }: LinkedTicketsPanelProps) {
  const qc = useQueryClient()
  const [showForm, setShowForm] = useState(false)
  const [selectedRelation, setSelectedRelation] = useState<string>('related_to')
  const [searchInput, setSearchInput] = useState('')
  const [selectedTicket, setSelectedTicket] = useState<Ticket | null>(null)
  const [error, setError] = useState('')
  const pickerRef = useRef<HTMLDivElement>(null)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [resolveAsResolve, setResolveAsResolve] = useState(false)
  const [resolutionNotes, setResolutionNotes] = useState<string | null>(null)

  // Fetch existing links
  const { data: links = [] } = useQuery({
    queryKey: ['links', ticketId],
    queryFn: () => listLinks(ticketId),
  })

  // Fetch the other end of each link
  const otherIds = useMemo(
    () => [...new Set(links.map(l => l.source_id === ticketId ? l.target_id : l.source_id))],
    [links, ticketId]
  )

  const otherTickets = useQueries({
    queries: otherIds.map(id => ({
      queryKey: ['ticket', id],
      queryFn: () => getTicket(id),
      retry: false,
    })),
  })

  // Build a map of ticket id to ticket data or error
  const otherTicketMap = new Map<string, { ticket: Ticket } | { error: true }>()
  otherIds.forEach((id, idx) => {
    const query = otherTickets[idx]
    if (query.data) {
      otherTicketMap.set(id, { ticket: query.data })
    } else if (query.isError) {
      otherTicketMap.set(id, { error: true })
    }
  })

  // Debounced search for ticket picker
  const [debouncedSearch, setDebouncedSearch] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedSearch(searchInput), 300)
    return () => clearTimeout(timer)
  }, [searchInput])

  const { data: suggestions = [] } = useQuery({
    queryKey: ['search-tickets', debouncedSearch],
    queryFn: () => listTickets({ q: debouncedSearch, limit: 8 }),
    enabled: debouncedSearch.length >= 2,
  })

  // Filter out viewed ticket and already-linked tickets
  const linkedIds = new Set([ticketId, ...links.flatMap(l => [l.source_id, l.target_id])])
  const filteredSuggestions = suggestions.filter(t => !linkedIds.has(t.id))


  // Close picker on outside click
  useEffect(() => {
    function handle(e: MouseEvent) {
      if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) {
        setPickerOpen(false)
      }
    }
    document.addEventListener('mousedown', handle)
    return () => document.removeEventListener('mousedown', handle)
  }, [])

  const addMutation = useMutation({
    mutationFn: async () => {
      if (!selectedTicket) throw new Error('No ticket selected')
      const opt = RELATION_OPTIONS.find(o => o.key === selectedRelation)
      if (!opt) throw new Error('Invalid relation')

      const [source, target] = opt.reversed ? [selectedTicket.id, ticketId] : [ticketId, selectedTicket.id]

      if (resolveAsResolve && opt.link_type === 'duplicate_of') {
        // Resolve as duplicate with custom or default notes
        const notes = resolutionNotes ?? duplicateResolutionNotes(selectedTicket.tracking_number)
        await addDuplicateLinkAndResolve(source, target, notes)
      } else {
        // Just add the link
        await addLink(source, target, opt.link_type)
      }
    },
    onSuccess: async () => {
      setSearchInput('')
      setSelectedTicket(null)
      setSelectedRelation('related_to')
      setResolveAsResolve(false)
      setResolutionNotes(null)
      setShowForm(false)
      setError('')
      qc.invalidateQueries({ queryKey: ['links', ticketId] })
      qc.invalidateQueries({ queryKey: ['ticket', ticketId] })
      qc.invalidateQueries({ queryKey: ['statusHistory', ticketId] })
      if (selectedTicket) {
        qc.invalidateQueries({ queryKey: ['links', selectedTicket.id] })
      }
    },
    onError: (err) => setError(extractError(err)),
  })

  const removeMutation = useMutation({
    mutationFn: (link: TicketLink) => removeLink(link.source_id, link.target_id, link.link_type),
    onSuccess: (_, link) => {
      qc.invalidateQueries({ queryKey: ['links', ticketId] })
      // Invalidate the other end's links too
      const otherId = link.source_id === ticketId ? link.target_id : link.source_id
      qc.invalidateQueries({ queryKey: ['links', otherId] })
    },
  })

  // Handle Enter key for jump-to-ticket
  const handleSearchKeyDown = async (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      const q = searchInput.trim()
      if (!q) return
      try {
        const ticket = await getTicket(q)
        if (linkedIds.has(ticket.id)) {
          setError('This ticket is already linked')
          return
        }
        setSelectedTicket(ticket)
        setSearchInput('')
        setPickerOpen(false)
      } catch {
        setError(`No ticket matches "${q}"`)
      }
    }
  }

  // Sort links by label then tracking number
  const sortedLinks = [...links].sort((a, b) => {
    const labelA = getLabel(a, ticketId)
    const labelB = getLabel(b, ticketId)
    if (labelA !== labelB) return labelA.localeCompare(labelB)
    const idA = a.source_id === ticketId ? a.target_id : a.source_id
    const idB = b.source_id === ticketId ? b.target_id : b.source_id
    const ticketA = otherTicketMap.get(idA)
    const ticketB = otherTicketMap.get(idB)
    if (ticketA && 'ticket' in ticketA && ticketB && 'ticket' in ticketB) {
      return ticketA.ticket.tracking_number.localeCompare(ticketB.ticket.tracking_number)
    }
    return 0
  })

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center justify-between">
          <CardTitle className="text-xs font-semibold uppercase tracking-wider text-gray-400">
            Linked Tickets
          </CardTitle>
          {!showForm && (
            <button className="text-xs text-blue-600 hover:underline" onClick={() => setShowForm(true)}>
              Add link
            </button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {/* List */}
        <div className="space-y-2">
          {sortedLinks.length === 0 && !showForm && (
            <p className="text-xs text-gray-400">No linked tickets</p>
          )}
          {sortedLinks.map(link => {
            const otherId = link.source_id === ticketId ? link.target_id : link.source_id
            const other = otherTicketMap.get(otherId)
            const label = getLabel(link, ticketId)

            return (
              <div key={`${link.source_id}-${link.target_id}-${link.link_type}`} className="flex items-center gap-2 text-xs">
                <Badge variant="secondary" className="text-[10px] shrink-0">{label}</Badge>
                <div className="flex-1 min-w-0">
                  {other && 'ticket' in other ? (
                    <Link to="/tickets/$id" params={{ id: other.ticket.id }}>
                      <span className="text-xs font-medium text-blue-600 hover:underline truncate" title={other.ticket.subject}>
                        {other.ticket.tracking_number} · {other.ticket.subject}
                      </span>
                    </Link>
                  ) : (
                    <span className="text-xs text-gray-400">Ticket not visible to you</span>
                  )}
                </div>
                <button
                  onClick={() => removeMutation.mutate(link)}
                  disabled={removeMutation.isPending}
                  className="text-gray-400 hover:text-gray-600 shrink-0"
                  aria-label={
                    other && 'ticket' in other
                      ? `Remove link to ${other.ticket.tracking_number}`
                      : 'Remove link'
                  }
                >
                  <XIcon className="h-3 w-3" />
                </button>
              </div>
            )
          })}
        </div>

        {/* Add form */}
        {showForm && (
          <div className="space-y-2 pt-2 border-t">
            {/* Relation select */}
            <div>
              <label htmlFor="relation-select" className="text-xs text-gray-500 block mb-1">Relation</label>
              <Select
                id="relation-select"
                className="h-8 text-xs w-full"
                value={selectedRelation}
                onChange={(e) => {
                  setSelectedRelation(e.target.value)
                  // Reset resolve checkbox when relation changes
                  setResolveAsResolve(false)
                  setResolutionNotes(null)
                }}
              >
                {RELATION_OPTIONS.map(opt => (
                  <option key={opt.key} value={opt.key}>
                    {opt.label}
                  </option>
                ))}
              </Select>
            </div>

            {/* Ticket picker */}
            <div>
              <label htmlFor="ticket-search-input" className="text-xs text-gray-500 block mb-1">Ticket</label>
              <div ref={pickerRef} className="relative">
                {selectedTicket ? (
                  <div className="flex items-center gap-2 text-xs">
                    <span className="bg-blue-100 text-blue-800 rounded-full px-2 py-0.5">
                      {selectedTicket.tracking_number} · {selectedTicket.subject}
                    </span>
                    <button
                      onClick={() => {
                        setSelectedTicket(null)
                        setResolveAsResolve(false)
                        setResolutionNotes(null)
                      }}
                      className="text-gray-400 hover:text-gray-600"
                      aria-label="Clear ticket"
                    >
                      <XIcon className="h-3 w-3" />
                    </button>
                  </div>
                ) : (
                  <>
                    <input
                      id="ticket-search-input"
                      type="text"
                      value={searchInput}
                      onChange={(e) => {
                        setSearchInput(e.target.value)
                        setPickerOpen(true)
                        setError('')
                      }}
                      onFocus={() => setPickerOpen(true)}
                      onKeyDown={handleSearchKeyDown}
                      placeholder="Tracking number or subject…"
                      className="h-8 w-full rounded-md border border-gray-200 bg-white px-3 text-xs text-gray-800 placeholder-gray-400 focus:border-blue-400 focus:outline-none focus:ring-1 focus:ring-blue-400"
                    />

                    {/* Suggestions dropdown */}
                    {pickerOpen && filteredSuggestions.length > 0 && (
                      <ul className="absolute z-10 mt-1 w-full rounded-md border bg-white py-1 shadow-lg text-xs">
                        {filteredSuggestions.map(t => (
                          <li
                            key={t.id}
                            className="cursor-pointer px-3 py-1.5 hover:bg-blue-50 text-gray-700"
                            onMouseDown={(e) => {
                              e.preventDefault()
                              setSelectedTicket(t)
                              setSearchInput('')
                              setPickerOpen(false)
                              // Reset notes to null so default is shown for new ticket
                              if (selectedRelation === 'duplicate_of') {
                                setResolutionNotes(null)
                              }
                            }}
                          >
                            {t.tracking_number} · {t.subject}
                          </li>
                        ))}
                      </ul>
                    )}

                    {/* "No matches" message */}
                    {pickerOpen && searchInput.trim() && filteredSuggestions.length === 0 && (
                      <div className="absolute z-10 mt-1 w-full rounded-md border bg-white px-3 py-2 shadow-lg text-xs text-gray-500">
                        No tickets match "{searchInput.trim()}"
                      </div>
                    )}
                  </>
                )}
              </div>
            </div>

            {/* Resolve as duplicate checkbox - only for duplicate_of relation */}
            {selectedRelation === 'duplicate_of' && selectedTicket && (
              <div className="space-y-2 pt-2 border-t">
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={resolveAsResolve}
                    onChange={(e) => {
                      setResolveAsResolve(e.target.checked)
                      if (e.target.checked && resolutionNotes === null) {
                        // Set default notes when checkbox is checked, but preserve edits
                        setResolutionNotes(duplicateResolutionNotes(selectedTicket.tracking_number))
                      }
                    }}
                    className="w-4 h-4"
                  />
                  <span className="text-xs text-gray-700">Also resolve this ticket as a duplicate</span>
                </label>

                {/* Resolution notes textarea - only when resolve is checked */}
                {resolveAsResolve && (
                  <div>
                    <label htmlFor="resolution-notes" className="text-xs text-gray-500 block mb-1">Resolution notes</label>
                    <textarea
                      id="resolution-notes"
                      value={resolutionNotes ?? duplicateResolutionNotes(selectedTicket.tracking_number)}
                      onChange={(e) => setResolutionNotes(e.target.value)}
                      rows={3}
                      placeholder="Notes explaining why this is a duplicate"
                      className="w-full rounded-md border border-gray-200 bg-white px-3 py-2 text-xs text-gray-800 placeholder-gray-400 focus:border-blue-400 focus:outline-none focus:ring-1 focus:ring-blue-400"
                    />
                  </div>
                )}
              </div>
            )}

            {error && <p className="text-xs text-red-600">{error}</p>}

            {/* Buttons */}
            <div className="flex gap-2 pt-1">
              <Button
                size="sm"
                className="h-7 text-xs"
                onClick={() => addMutation.mutate()}
                disabled={!selectedTicket || addMutation.isPending}
              >
                {addMutation.isPending ? 'Linking…' : 'Link'}
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="h-7 text-xs"
                onClick={() => {
                  setShowForm(false)
                  setSearchInput('')
                  setSelectedTicket(null)
                  setSelectedRelation('related_to')
                  setError('')
                }}
              >
                Cancel
              </Button>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
