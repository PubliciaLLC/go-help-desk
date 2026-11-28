// A description or a reply is at most 50,000 characters, counted by the server in
// bytes of the stored text (ticket.MaxDescriptionLength, MaxReplyLength). The
// forms count the same way, so a text they let through is one the server takes.
export const MAX_TEXT_BYTES = 50_000

const encoder = new TextEncoder()

export function textBytes(s: string): number {
  return encoder.encode(s).length
}

export function isOverLimit(s: string): boolean {
  // A string cannot be over the limit in bytes unless it has more than a third
  // of it in UTF-16 units, so skip encoding for the usual short reply.
  return s.length * 3 > MAX_TEXT_BYTES && textBytes(s) > MAX_TEXT_BYTES
}

/** From 90 percent of the limit, the counter is shown. */
export function nearLimit(s: string): boolean {
  return s.length * 3 >= MAX_TEXT_BYTES * 0.9 && textBytes(s) >= MAX_TEXT_BYTES * 0.9
}
