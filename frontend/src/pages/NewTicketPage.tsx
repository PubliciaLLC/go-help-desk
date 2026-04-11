import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { createTicket } from '@/api/tickets'
import { listCategories, listTypes, listItems } from '@/api/admin'
import { extractError } from '@/api/client'
import { Layout } from '@/components/Layout'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Select } from '@/components/ui/select'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

const PRIORITIES = ['critical', 'high', 'medium', 'low'] as const

export function NewTicketPage() {
  const navigate = useNavigate()
  const [subject, setSubject] = useState('')
  const [description, setDescription] = useState('')
  const [categoryId, setCategoryId] = useState('')
  const [typeId, setTypeId] = useState('')
  const [itemId, setItemId] = useState('')
  const [priority, setPriority] = useState<'medium' | 'critical' | 'high' | 'low'>('medium')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const { data: categories = [] } = useQuery({
    queryKey: ['categories'],
    queryFn: listCategories,
  })

  const { data: types = [] } = useQuery({
    queryKey: ['types', categoryId],
    queryFn: () => listTypes(categoryId),
    enabled: !!categoryId,
  })

  const { data: items = [] } = useQuery({
    queryKey: ['items', categoryId, typeId],
    queryFn: () => listItems(categoryId, typeId),
    enabled: !!categoryId && !!typeId,
  })

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (!subject.trim()) { setError('Subject is required'); return }
    if (!categoryId) { setError('Category is required'); return }
    setLoading(true)
    try {
      const t = await createTicket({
        subject,
        description,
        category_id: categoryId,
        type_id: typeId || undefined,
        item_id: itemId || undefined,
        priority,
      })
      navigate({ to: '/tickets/$id', params: { id: t.id } })
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

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
                />
              </div>

              <div className="grid grid-cols-3 gap-4">
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
                    disabled={!categoryId || types.length === 0}
                  >
                    <option value="">Select…</option>
                    {types.map((t) => (
                      <option key={t.id} value={t.id}>{t.name}</option>
                    ))}
                  </Select>
                </div>

                <div className="space-y-1">
                  <Label htmlFor="item">Item</Label>
                  <Select
                    id="item"
                    value={itemId}
                    onChange={(e) => setItemId(e.target.value)}
                    disabled={!typeId || items.length === 0}
                  >
                    <option value="">Select…</option>
                    {items.map((i) => (
                      <option key={i.id} value={i.id}>{i.name}</option>
                    ))}
                  </Select>
                </div>
              </div>

              <div className="space-y-1 max-w-xs">
                <Label htmlFor="priority">Priority</Label>
                <Select
                  id="priority"
                  value={priority}
                  onChange={(e) => setPriority(e.target.value as typeof priority)}
                >
                  {PRIORITIES.map((p) => (
                    <option key={p} value={p}>{p.charAt(0).toUpperCase() + p.slice(1)}</option>
                  ))}
                </Select>
              </div>

              {error && <p className="text-sm text-red-600">{error}</p>}

              <div className="flex gap-3">
                <Button type="submit" disabled={loading}>
                  {loading ? 'Submitting…' : 'Submit ticket'}
                </Button>
                <Button type="button" variant="outline" onClick={() => navigate({ to: '/tickets' })}>
                  Cancel
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </div>
    </Layout>
  )
}
