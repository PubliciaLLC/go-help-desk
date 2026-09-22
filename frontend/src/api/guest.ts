import axios from 'axios'

// A separate axios instance, not the shared `api`.
//
// The shared client sends session cookies and, on a 401, redirects to the login
// page. Neither is right here: a guest has no session to send, and bouncing
// them to a login they cannot complete is the worst possible answer to an
// expired link.
const guestApi = axios.create({ baseURL: '/api/v1/guest' })

export interface GuestReply {
  id: string
  body: string
  created_at: string
  // True for the guest's own messages. The server derives it from the reply
  // having no author and sends no identity, so staff are never named.
  from_you: boolean
}

export interface GuestTicket {
  tracking_number: string
  subject: string
  description: string
  status: string
  created_at: string
  updated_at: string
  replies: GuestReply[]
}

// The server answers 404 for every reason a token does not work — expired,
// rotated, never issued, ticket closed — and deliberately does not say which.
// So there is one error here too.
export class GuestLinkInvalid extends Error {}

function auth(token: string) {
  return { headers: { Authorization: `Guest ${token}` } }
}

export async function getGuestTicket(token: string): Promise<GuestTicket> {
  try {
    const res = await guestApi.get<GuestTicket>('/ticket', auth(token))
    return res.data
  } catch {
    throw new GuestLinkInvalid()
  }
}

export async function addGuestReply(token: string, body: string): Promise<GuestReply> {
  const res = await guestApi.post<GuestReply>('/replies', { body }, auth(token))
  return res.data
}

export interface GuestTicketInput {
  subject: string
  description: string
  category_id: string
  type_id?: string | null
  item_id?: string | null
  guest_email: string
  guest_name: string
  guest_phone?: string
}

// Returns the tracking number, which is all the server gives. The link arrives
// by email, because the mailbox is the credential.
export async function createGuestTicket(input: GuestTicketInput): Promise<string> {
  const res = await guestApi.post<{ tracking_number: string }>('/tickets', input)
  return res.data.tracking_number
}

// Always resolves. The server answers 202 whether or not anything matched, so
// that this cannot be used to find out whether a ticket or an address exists —
// and the page must say the same thing either way.
export async function resendGuestLink(trackingNumber: string, email: string): Promise<void> {
  await guestApi.post('/resend', { tracking_number: trackingNumber, email })
}
