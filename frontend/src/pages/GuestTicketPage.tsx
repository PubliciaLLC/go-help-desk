import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listPublicCategories } from '@/api/tickets'
import { createGuestTicket, listGuestFields } from '@/api/guest'
import type { GuestField } from '@/api/types'
import { overLimit } from '@/lib/fieldLimits'
import { extractError } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { TextLimit } from '@/components/TextLimit'
import { isOverLimit } from '@/lib/textLimit'
import { MarkdownEditor } from '@/components/editor/MarkdownEditor'
import { Select } from '@/components/ui/select'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

interface SuccessInfo {
  trackingNumber: string
  // Set when the ticket was filed but some details could not be saved.
  notice?: string
}

function GuestFieldInput({
  field,
  value,
  onChange,
}: {
  field: GuestField
  value: string
  onChange: (v: string) => void
}) {
  const id = `cf-${field.id}`
  switch (field.field_type) {
    case 'textarea':
      return <Textarea id={id} value={value} onChange={(e) => onChange(e.target.value)} rows={3} maxLength={field.max_length} />
    case 'number':
      return <Input id={id} type="number" value={value} onChange={(e) => onChange(e.target.value)} />
    case 'select':
      return (
        <Select id={id} value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="">Select…</option>
          {(field.options ?? []).map((opt) => (
            <option key={opt} value={opt}>{opt}</option>
          ))}
        </Select>
      )
    default:
      return <Input id={id} value={value} onChange={(e) => onChange(e.target.value)} maxLength={field.max_length} />
  }
}

export function GuestTicketPage() {
  const [subject, setSubject] = useState('')
  const [description, setDescription] = useState('')
  const [categoryId, setCategoryId] = useState('')
  const [fieldValues, setFieldValues] = useState<Record<string, string>>({})
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

  // The custom fields an administrator chose to offer visitors under this
  // category (#627), and only those: the server tells this form which they are
  // and refuses any other key. This form asked for every field once and threw
  // the answers away on submit, which could also stop a visitor filing over a
  // field the server never took; it was removed, and is back only because the
  // server now decides what a guest may fill.
  //
  // The form does not go on without the list. The lookup is throttled (429) and
  // can fail like any request, and a form that carried on would show no fields
  // and then be refused for a required one it never offered, with nothing to
  // fill in. So a failure says so and offers a retry, and submit waits until the
  // fields are known.
  const fieldsQuery = useQuery({
    queryKey: ['guest-fields', categoryId],
    queryFn: () => listGuestFields(categoryId),
    enabled: !!categoryId,
    retry: false,
  })
  const guestFields = fieldsQuery.data ?? []
  const fieldsPending = !!categoryId && !fieldsQuery.isSuccess

  function chooseCategory(id: string) {
    setCategoryId(id)
    // Another category may not offer these fields, and the server refuses a
    // field it did not offer, so nothing typed for one is carried to the next.
    setFieldValues({})
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    if (!subject.trim()) { setError('Subject is required'); return }
    if (!categoryId) { setError('Category is required'); return }
    if (!name.trim()) { setError('Name is required'); return }
    if (!email.trim()) { setError('Email is required'); return }
    if (isOverLimit(description)) { setError('The description is too long. Shorten it to send.'); return }
    if (fieldsPending) return // the button is disabled; this covers Enter in a field

    const customFields: Record<string, string> = {}
    for (const f of guestFields) {
      const v = fieldValues[f.id] ?? ''
      // maxlength counts UTF-16 units and a paste can get past it, so the exact
      // check is made here; the server holds the same limit.
      const tooLong = overLimit(f.name, v, f.max_length)
      if (tooLong) { setError(tooLong); return }
      if (v.trim()) customFields[f.id] = v
      else if (f.required) { setError(`${f.name} is required`); return }
    }

    setLoading(true)
    try {
      // The guest endpoint, not the authenticated one. This page posted to
      // /tickets for as long as it has existed, which sits behind a role check,
      // so every submission from a visitor answered 401.
      let notice: string | undefined
      const trackingNumber = await createGuestTicket({
        subject,
        description,
        category_id: categoryId,
        guest_name: name.trim(),
        guest_email: email.trim(),
        guest_phone: phone.trim() || undefined,
        custom_fields: Object.keys(customFields).length > 0 ? customFields : undefined,
      }, (n) => { notice = n })
      setSuccess({ trackingNumber, notice })
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
              {success.notice && (
                <p role="status" className="rounded-md border border-amber-200 bg-amber-50 p-3 text-amber-900">
                  {success.notice}
                </p>
              )}
              <p className="text-gray-500">
                We have emailed you a link to follow the ticket. Keep this
                number too — with your email address it will get you a fresh
                link if you lose the first one.
              </p>
              <Button
                type="button"
                variant="outline"
                className="w-full mt-2"
                onClick={() => {
                  setSuccess(null)
                  setSubject('')
                  setDescription('')
                  chooseCategory('')
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
                <Label id="description-label">Description</Label>
                <MarkdownEditor
                  value={description}
                  onChange={setDescription}
                  labelId="description-label"
                  placeholder="Full details of the request or issue"
                />
                <TextLimit value={description} />
              </div>

              <div className="space-y-1">
                <Label htmlFor="category">Category *</Label>
                <Select
                  id="category"
                  value={categoryId}
                  onChange={(e) => chooseCategory(e.target.value)}
                >
                  <option value="">Select…</option>
                  {categories.map((c) => (
                    <option key={c.id} value={c.id}>{c.name}</option>
                  ))}
                </Select>
              </div>

              {guestFields.map((f) => (
                <div key={f.id} className="space-y-1">
                  <Label htmlFor={`cf-${f.id}`}>
                    {f.name}
                    {f.required && <span className="ml-0.5 text-red-500"> *</span>}
                  </Label>
                  <GuestFieldInput
                    field={f}
                    value={fieldValues[f.id] ?? ''}
                    onChange={(v) => setFieldValues((prev) => ({ ...prev, [f.id]: v }))}
                  />
                </div>
              ))}

              {fieldsQuery.isError && (
                <div role="alert" className="space-y-2 text-sm text-red-600">
                  <p>The extra questions for this category could not be loaded. Try again in a moment.</p>
                  <Button type="button" variant="outline" size="sm" onClick={() => fieldsQuery.refetch()}>
                    Try again
                  </Button>
                </div>
              )}

              {error && <p role="alert" className="text-sm text-red-600">{error}</p>}

              <Button type="submit" className="w-full" disabled={loading || fieldsPending || isOverLimit(description)}>
                {loading ? 'Submitting…' : 'Submit request'}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
