import type { CreateTicketInput, RequesterHit } from '@/api/tickets'

// Who a ticket is for (#444), offered to staff and administrators only. Me is
// the default and sends nothing, so the form behaves as it always did.
export type Requester =
  | { mode: 'me' }
  | { mode: 'user'; picked: RequesterHit | null }
  | { mode: 'email'; email: string }

export const ME: Requester = { mode: 'me' }

// The lookup answers nothing under two characters; asking would only get a 400.
export const MIN_QUERY = 2

type ReporterFields = Pick<CreateTicketInput, 'reporter_user_id' | 'reporter_email'>

// What to send for a choice, or why it cannot be sent yet.
export function reporterFields(r: Requester): { fields: ReporterFields } | { error: string } {
  switch (r.mode) {
    case 'me':
      return { fields: {} }
    case 'user':
      return r.picked
        ? { fields: { reporter_user_id: r.picked.id } }
        : { error: 'Pick a requester from the search results, or choose Me.' }
    case 'email': {
      const email = r.email.trim()
      return email
        ? { fields: { reporter_email: email } }
        : { error: "Type the requester's email address, or choose Me." }
    }
  }
}
