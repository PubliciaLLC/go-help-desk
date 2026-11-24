import type { QueueCount, QueueKey } from '@/api/types'

// The queue strip's cells (DESIGN.md, Dashboard, The queue strip), as data: the
// label, and the ticket list address that shows exactly the tickets the cell
// counts. The count comes from the server; which cells exist for whom is the
// server's decision too, so this table is looked up by key and nothing else.
export const QUEUE_CELLS: Record<QueueKey, { label: string; href: string }> = {
  assigned_to_me: { label: 'Assigned to me', href: '/tickets?view=me' },
  my_groups: { label: 'My groups', href: '/tickets?view=groups' },
  sla_breached: { label: 'Breached', href: '/tickets?view=me&sla=breached' },
  sla_at_risk: { label: 'At risk', href: '/tickets?view=me&sla=at_risk' },
  awaiting_my_reply: { label: 'Awaiting my reply', href: '/tickets?view=me&awaiting_reply=true' },
  my_tickets: { label: 'Your tickets', href: '/tickets' },
}

export interface QueueCell extends QueueCount {
  key: QueueKey
  label: string
  href: string
}

const isKnown = (key: string): key is QueueKey => Object.hasOwn(QUEUE_CELLS, key)

// The cells to draw, in the server's order. A key this build does not know is
// ignored, so a later release may add one.
export function knownCells(queues: QueueCount[]): QueueCell[] {
  return queues.flatMap((q) => (isKnown(q.key) ? [{ ...q, key: q.key, ...QUEUE_CELLS[q.key] }] : []))
}

// The query of an address, as the object the router hands the ticket list's
// search parser: the same reading a typed address gets.
export function addressSearch(href: string): Record<string, string> {
  return Object.fromEntries(new URLSearchParams(href.split('?')[1] ?? ''))
}
