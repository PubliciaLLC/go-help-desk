import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import {
  createTicket,
  listPublicCategories,
  listPublicTypes,
  uploadAttachment,
} from '@/api/tickets'
import { listCategories, listTypes, listItems } from '@/api/admin'
import { extractError } from '@/api/client'
import { useAuthStore } from '@/store/auth'
import { Layout } from '@/components/Layout'
import { AttachmentUpload, type UploadState } from '@/components/AttachmentUpload'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Select } from '@/components/ui/select'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const PRIORITIES = ['critical', 'high', 'medium', 'low'] as const

export function NewTicketPage() {
  const navigate = useNavigate()
  const { user } = useAuthStore()
  const isStaffOrAdmin = user?.role === 'staff' || user?.role === 'admin'

  const [subject, setSubject] = useState('')
  const [description, setDescription] = useState('')
  const [categoryId, setCategoryId] = useState('')
  const [typeId, setTypeId] = useState('')
  const [itemId, setItemId] = useState('')
  const [priority, setPriority] = useState<'medium' | 'critical' | 'high' | 'low'>('medium')
  const [files, setFiles] = useState<File[]>([])
  const [uploadStates, setUploadStates] = useState<Record<string, UploadState> | undefined>()
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [createdTicketId, setCreatedTicketId] = useState<string | null>(null)

  // Staff/admin use the admin endpoints (all categories/types/items, active or inactive).
  // Regular users use the public endpoints (active only, no items).
  const { data: categories = [] } = useQuery({
    queryKey: isStaffOrAdmin ? ['admin-categories'] : ['public-categories'],
    queryFn: isStaffOrAdmin ? listCategories : listPublicCategories,
  })

  const { data: types = [] } = useQuery({
    queryKey: isStaffOrAdmin
      ? ['admin-types', categoryId]
      : ['public-types', categoryId],
    queryFn: () =>
      isStaffOrAdmin ? listTypes(categoryId) : listPublicTypes(categoryId),
    enabled: !!categoryId,
  })

  const { data: items = [] } = useQuery({
    queryKey: ['admin-items', categoryId, typeId],
    queryFn: () => listItems(categoryId, typeId),
    enabled: isStaffOrAdmin && !!categoryId && !!typeId,
  })

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (!subject.trim()) { setError('Subject is required'); return }
    if (!categoryId) { setError('Category is required'); return }

    setSubmitting(true)
    try {
      const t = await createTicket({
        subject,
        description,
        category_id: categoryId,
        type_id: typeId || undefined,
        item_id: isStaffOrAdmin ? (itemId || undefined) : undefined,
        priority: isStaffOrAdmin ? priority : undefined,
      })

      if (files.length === 0) {
        navigate({ to: '/tickets/$id', params: { id: t.id } })
        return
      }

      // Upload attachments one by one.
      setCreatedTicketId(t.id)
      const initial: Record<string, UploadState> = {}
      for (const f of files) initial[f.name] = { status: 'pending' }
      setUploadStates(initial)

      let allOk = true
      for (const f of files) {
        setUploadStates((prev) => ({ ...prev!, [f.name]: { status: 'uploading' } }))
        try {
          await uploadAttachment(t.id, f)
          setUploadStates((prev) => ({ ...prev!, [f.name]: { status: 'done' } }))
        } catch (err) {
          allOk = false
          setUploadStates((prev) => ({
            ...prev!,
            [f.name]: { status: 'error', error: extractError(err) },
          }))
        }
      }

      if (allOk) {
        navigate({ to: '/tickets/$id', params: { id: t.id } })
      }
      // If some uploads failed, stay on page so the user sees errors.
      // They can still navigate via the "View ticket" link shown below.
    } catch (err) {
      setError(extractError(err))
    } finally {
      setSubmitting(false)
    }
  }

  const isUploading = !!uploadStates
  const uploadDone = isUploading && Object.values(uploadStates!).every((s) => s.status !== 'uploading')

  return (
    <Layout>
      <div className="max-w-2xl space-y-6">
        <h1 className="text-2xl font-bold text-gray-900">New Ticket</h1>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">Ticket details</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleSubmit} className="space-y-5">
              <div className="space-y-1">
                <Label htmlFor="subject">Subject *</Label>
                <Input
                  id="subject"
                  value={subject}
                  onChange={(e) => setSubject(e.target.value)}
                  placeholder="Short summary of the issue"
                  disabled={isUploading}
                  required
                />
              </div>

              <div className="space-y-1">
                <Label htmlFor="description">Description</Label>
                <Textarea
                  id="description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Full details of the request or issue"
                  rows={5}
                  disabled={isUploading}
                />
              </div>

              <div className={`grid gap-4 ${isStaffOrAdmin ? 'grid-cols-3' : 'grid-cols-2'}`}>
                <div className="space-y-1">
                  <Label htmlFor="category">Category *</Label>
                  <Select
                    id="category"
                    value={categoryId}
                    onChange={(e) => {
                      setCategoryId(e.target.value)
                      setTypeId('')
                      setItemId('')
                    }}
                    disabled={isUploading}
                  >
                    <option value="">Select…</option>
                    {categories.map((c) => (
                      <option key={c.id} value={c.id}>{c.name}</option>
                    ))}
                  </Select>
                </div>

                <div className="space-y-1">
                  <Label htmlFor="type">Type</Label>
                  <Select
                    id="type"
                    value={typeId}
                    onChange={(e) => {
                      setTypeId(e.target.value)
                      setItemId('')
                    }}
                    disabled={isUploading || !categoryId || types.length === 0}
                  >
                    <option value="">Select…</option>
                    {types.map((t) => (
                      <option key={t.id} value={t.id}>{t.name}</option>
                    ))}
                  </Select>
                </div>

                {isStaffOrAdmin && (
                  <div className="space-y-1">
                    <Label htmlFor="item">Item</Label>
                    <Select
                      id="item"
                      value={itemId}
                      onChange={(e) => setItemId(e.target.value)}
                      disabled={isUploading || !typeId || items.length === 0}
                    >
                      <option value="">Select…</option>
                      {items.map((i) => (
                        <option key={i.id} value={i.id}>{i.name}</option>
                      ))}
                    </Select>
                  </div>
                )}
              </div>

              {isStaffOrAdmin && (
                <div className="space-y-1 max-w-xs">
                  <Label htmlFor="priority">Priority</Label>
                  <Select
                    id="priority"
                    value={priority}
                    onChange={(e) => setPriority(e.target.value as typeof priority)}
                    disabled={isUploading}
                  >
                    {PRIORITIES.map((p) => (
                      <option key={p} value={p}>{p.charAt(0).toUpperCase() + p.slice(1)}</option>
                    ))}
                  </Select>
                </div>
              )}

              <div className="space-y-1">
                <Label>Attachments</Label>
                <AttachmentUpload
                  files={files}
                  onChange={setFiles}
                  uploadStates={uploadStates}
                  disabled={isUploading}
                />
              </div>

              {error && <p className="text-sm text-red-600">{error}</p>}

              {uploadDone && createdTicketId && (
                <div className="rounded-md bg-yellow-50 border border-yellow-200 p-3 text-sm space-y-1">
                  <p className="text-yellow-800 font-medium">Some files failed to upload.</p>
                  <button
                    type="button"
                    className="text-blue-600 underline hover:no-underline"
                    onClick={() => navigate({ to: '/tickets/$id', params: { id: createdTicketId } })}
                  >
                    View ticket anyway
                  </button>
                </div>
              )}

              {!uploadDone && (
                <div className="flex gap-3">
                  <Button type="submit" disabled={submitting || isUploading}>
                    {submitting
                      ? 'Submitting…'
                      : isUploading
                      ? 'Uploading files…'
                      : 'Submit ticket'}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => navigate({ to: '/tickets' })}
                    disabled={submitting || isUploading}
                  >
                    Cancel
                  </Button>
                </div>
              )}
            </form>
          </CardContent>
        </Card>
      </div>
    </Layout>
  )
}
