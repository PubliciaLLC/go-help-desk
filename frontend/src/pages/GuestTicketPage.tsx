import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { createTicket, listPublicCategories } from '@/api/tickets'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Select } from '@/components/ui/select'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

interface SuccessInfo {
  trackingNumber: string
}

export function GuestTicketPage() {
  const [subject, setSubject] = useState('')
  const [description, setDescription] = useState('')
  const [categoryId, setCategoryId] = useState('')
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [phone, setPhone] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [success, setSuccess] = useState<SuccessInfo | null>(null)

  const { data: categories = [] } = useQuery({
    queryKey: ['public-categories'],
    queryFn: listPublicCategories,
  })

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (!subject.trim()) { setError('Subject is required'); return }
    if (!categoryId) { setError('Category is required'); return }
    if (!name.trim()) { setError('Name is required'); return }
    if (!email.trim()) { setError('Email is required'); return }

    setLoading(true)
    try {
      const t = await createTicket({
        subject,
        description,
        category_id: categoryId,
        guest_name: name.trim(),
        guest_email: email.trim(),
        guest_phone: phone.trim() || undefined,
      })
      setSuccess({ trackingNumber: t.tracking_number })
    } catch (err) {
      setError(extractError(err))
    } finally {
      setLoading(false)
    }
  }

  if (success) {
    return (
      <div className="min-h-screen bg-gray-50 flex items-center justify-center p-4">
        <div className="max-w-md w-full">
          <Card>
            <CardHeader>
              <CardTitle className="text-lg text-green-700">Ticket submitted</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3 text-sm text-gray-700">
              <p>
                Your request has been received. Your tracking number is:
              </p>
              <p className="text-center text-2xl font-mono font-bold tracking-widest text-gray-900">
                {success.trackingNumber}
              </p>
              <p className="text-gray-500">
                Keep this number — you can use it to follow up with the help desk.
              </p>
              <Button
                type="button"
                variant="outline"
                className="w-full mt-2"
                onClick={() => {
                  setSuccess(null)
                  setSubject('')
                  setDescription('')
                  setCategoryId('')
                  setName('')
                  setEmail('')
                  setPhone('')
                }}
              >
                Submit another ticket
              </Button>
            </CardContent>
          </Card>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-gray-50 flex items-start justify-center p-4 pt-12">
      <div className="max-w-lg w-full space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Submit a request</h1>
          <p className="mt-1 text-sm text-gray-500">
            Fill out the form below and we will get back to you shortly.
          </p>
        </div>

        <Card>
          <CardContent className="pt-6">
            <form onSubmit={handleSubmit} className="space-y-5">
              {/* Contact info */}
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-1">
                  <Label htmlFor="name">Name *</Label>
                  <Input
                    id="name"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="Your full name"
                    required
                  />
                </div>
                <div className="space-y-1">
                  <Label htmlFor="email">Email *</Label>
                  <Input
                    id="email"
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="you@example.com"
                    required
                  />
                </div>
              </div>

              <div className="space-y-1">
                <Label htmlFor="phone">Phone <span className="text-gray-400 font-normal">(optional)</span></Label>
                <Input
                  id="phone"
                  type="tel"
                  value={phone}
                  onChange={(e) => setPhone(e.target.value)}
                  placeholder="+1 555 000 0000"
                />
              </div>

              <hr className="border-gray-100" />

              {/* Ticket details */}
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

              <div className="space-y-1">
                <Label htmlFor="category">Category *</Label>
                <Select
                  id="category"
                  value={categoryId}
                  onChange={(e) => setCategoryId(e.target.value)}
                >
                  <option value="">Select…</option>
                  {categories.map((c) => (
                    <option key={c.id} value={c.id}>{c.name}</option>
                  ))}
                </Select>
              </div>

              {error && <p className="text-sm text-red-600">{error}</p>}

              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? 'Submitting…' : 'Submit request'}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
