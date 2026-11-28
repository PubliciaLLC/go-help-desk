import type { CreateTicketInput, RequesterHit } from '@/api/tickets'

// Who a ticket is for (#444), offered to staff and administrators only. Me is
// the default and sends nothing, so the form behaves as it always did.
export type Requester =
  | { mode: 'me' }
  | { mode: 'user'; picked: RequesterHit | null }
  | { mode: 'email'; email: string; name: string; phone: string }

export const ME: Requester = { mode: 'me' }

// The lookup answers nothing under two characters; asking would only get a 400.
// Counted in characters (code points), as the server counts them: string length
// counts UTF-16 units, which makes one emoji two.
export const MIN_QUERY = 2
export const queryLength = (q: string) => [...q].length

type ReporterFields = Pick<
  CreateTicketInput,
  'reporter_user_id' | 'reporter_email' | 'guest_name' | 'guest_phone'
>

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
      // The guest form's rule: a name is required, a phone number is not.
      const email = r.email.trim()
      const name = r.name.trim()
      const phone = r.phone.trim()
      if (!email) return { error: "Type the requester's email address, or choose Me." }
      if (!name) return { error: "Type the requester's name, or choose Me." }
      return {
        fields: {
          reporter_email: email,
          guest_name: name,
          ...(phone ? { guest_phone: phone } : {}),
        },
      }
    }
  }
}
