import { useEffect, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { addReply, uploadAttachment, listTicketCannedResponses } from '@/api/tickets'
import { extractError } from '@/api/client'
import { AttachmentUpload, type UploadState } from '@/components/AttachmentUpload'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { MessageSquareTextIcon } from 'lucide-react'
import type { CannedResponse } from '@/api/types'

interface CannedResponsePickerProps {
  responses: CannedResponse[]
  onSelect: (body: string) => void
  onClose: () => void
  // Ref to the wrapper that contains BOTH the trigger button and this picker.
  // Using the trigger's own container (rather than a ref scoped to just the
  // picker) keeps a click on the trigger from counting as "outside" — the
  // trigger's onClick toggle is the only thing that should open/close it.
  containerRef: React.RefObject<HTMLDivElement | null>
}

function CannedResponsePicker({ responses, onSelect, onClose, containerRef }: CannedResponsePickerProps) {
  const [filter, setFilter] = useState('')

  useEffect(() => {
    function handlePointerDown(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        onClose()
      }
    }
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [onClose, containerRef])

  const filtered = filter.trim()
    ? responses.filter((r) => r.name.toLowerCase().includes(filter.trim().toLowerCase()))
    : responses

  return (
    <div className="absolute z-10 mt-1 w-80 rounded-md border border-gray-200 bg-white shadow-lg">
      <div className="border-b border-gray-100 p-2">
        <Input
          autoFocus
          placeholder="Filter by name…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="h-8 text-xs"
        />
      </div>
      <div className="max-h-56 overflow-y-auto py-1">
        {responses.length === 0 ? (
          <p className="px-3 py-2 text-xs text-gray-400">No canned responses available for this ticket.</p>
        ) : filtered.length === 0 ? (
          <p className="px-3 py-2 text-xs text-gray-400">No matches</p>
        ) : (
          filtered.map((r) => (
            <button
              key={r.id}
              type="button"
              className="block w-full px-3 py-2 text-left hover:bg-gray-50"
              onClick={() => onSelect(r.body)}
            >
              <div className="text-sm font-medium text-gray-900">{r.name}</div>
              <div className="truncate text-xs text-gray-400">{r.body}</div>
            </button>
          ))
        )}
      </div>
    </div>
  )
}

// ── Page ──────────────────────────────────────────────────────────────────────

export interface ReplyComposerProps {
  ticketId: string
  isStaffOrAdmin: boolean
}

/**
 * The reply / work-entry composer at the foot of a ticket.
 *
 * Extracted from TicketDetailPage, where its seven pieces of state, two refs,
 * a query and a mutation sat inline among the timeline and the lifecycle
 * actions. None of it is read anywhere else on the page, which is what made
 * this the second seam to lift out after the classification panel.
 *
 * The attachment upload deliberately runs AFTER the reply is created rather
 * than alongside it: attachments belong to the ticket, and uploading them for
 * a reply that then fails to save would leave orphans.
 */
export function ReplyComposer({ ticketId, isStaffOrAdmin }: ReplyComposerProps) {
  const qc = useQueryClient()

  const [body, setBody] = useState('')
  const [internal, setInternal] = useState(false)
  const [notify, setNotify] = useState(true)
  const [files, setFiles] = useState<File[]>([])
  const [uploadStates, setUploadStates] = useState<Record<string, UploadState> | undefined>()
  const [error, setError] = useState('')
  const [pickerOpen, setPickerOpen] = useState(false)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const pickerContainerRef = useRef<HTMLDivElement>(null)

  const { data: cannedResponses = [] } = useQuery({
    queryKey: ['ticket-canned-responses', ticketId],
    queryFn: () => listTicketCannedResponses(ticketId),
    enabled: isStaffOrAdmin,
  })

  const mutation = useMutation({
    mutationFn: () => addReply(ticketId, body, internal, notify),
    onSuccess: async () => {
      setBody('')
      setError('')
      qc.invalidateQueries({ queryKey: ['replies', ticketId] })
      qc.invalidateQueries({ queryKey: ['statusHistory', ticketId] })
      qc.invalidateQueries({ queryKey: ['ticket', ticketId] })

      // Upload any attached files to the ticket.
      if (files.length > 0) {
        const initial: Record<string, UploadState> = {}
        for (const f of files) initial[f.name] = { status: 'pending' }
        setUploadStates(initial)

        for (const f of files) {
          setUploadStates((prev) => ({ ...prev!, [f.name]: { status: 'uploading' } }))
          try {
            await uploadAttachment(ticketId, f)
            setUploadStates((prev) => ({ ...prev!, [f.name]: { status: 'done' } }))
          } catch (err) {
            setUploadStates((prev) => ({
              ...prev!,
              [f.name]: { status: 'error', error: extractError(err) },
            }))
          }
        }

        qc.invalidateQueries({ queryKey: ['attachments', ticketId] })
        // Clear files after a short delay so the user can see the done states.
        setTimeout(() => {
          setFiles([])
          setUploadStates(undefined)
        }, 1500)
      }
    },
    onError: (err) => setError(extractError(err)),
  })

  function insertCannedResponse(text: string) {
    const textarea = textareaRef.current
    const start = textarea?.selectionStart ?? body.length
    const end = textarea?.selectionEnd ?? body.length
    const newValue = body.slice(0, start) + text + body.slice(end)
    const cursorPos = start + text.length
    setBody(newValue)
    setPickerOpen(false)
    requestAnimationFrame(() => {
      textarea?.focus()
      textarea?.setSelectionRange(cursorPos, cursorPos)
    })
  }

  const busy = !!uploadStates

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm">
          {isStaffOrAdmin ? 'Add work log entry' : 'Add reply'}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {isStaffOrAdmin && (
          <div className="relative inline-block" ref={pickerContainerRef}>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className="h-8 text-xs"
              onClick={() => setPickerOpen((o) => !o)}
              disabled={busy}
            >
              <MessageSquareTextIcon className="mr-1.5 h-3.5 w-3.5" />
              Insert canned response
            </Button>
            {pickerOpen && (
              <CannedResponsePicker
                responses={cannedResponses}
                onSelect={insertCannedResponse}
                onClose={() => setPickerOpen(false)}
                containerRef={pickerContainerRef}
              />
            )}
          </div>
        )}

        <Textarea
          ref={textareaRef}
          placeholder={isStaffOrAdmin ? 'Describe the work performed or add a note…' : 'Type your reply…'}
          rows={4}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          disabled={busy}
        />

        {isStaffOrAdmin && (
          <>
            <div className="flex flex-wrap gap-4">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={internal}
                  onChange={(e) => {
                    setInternal(e.target.checked)
                    // An internal note is for colleagues, so it never mails the
                    // customer; unticking it restores the default.
                    setNotify(!e.target.checked)
                  }}
                  className="h-4 w-4 rounded border-gray-300"
                  disabled={busy}
                />
                Internal note (not visible to customer)
              </label>

              {!internal && (
                <label className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={notify}
                    onChange={(e) => setNotify(e.target.checked)}
                    className="h-4 w-4 rounded border-gray-300"
                    disabled={busy}
                  />
                  Send ticket update email to customer
                </label>
              )}
            </div>

            <AttachmentUpload
              files={files}
              onChange={setFiles}
              uploadStates={uploadStates}
              disabled={busy}
              maxFiles={5}
            />
          </>
        )}

        {error && <p className="text-sm text-red-600">{error}</p>}

        <Button
          onClick={() => mutation.mutate()}
          disabled={mutation.isPending || busy || !body.trim()}
        >
          {mutation.isPending
            ? 'Saving…'
            : busy
            ? 'Uploading files…'
            : isStaffOrAdmin
            ? 'Save entry'
            : 'Send reply'}
        </Button>
      </CardContent>
    </Card>
  )
}
