// The description of a category, type or item (1.3.0): optional plain-text
// guidance, shown beneath the Category / Type / Item chooser.
// docs/DESIGN.md, Data Model -> Ticket Classification -> Descriptions.

import { charCount } from '@/lib/fieldLimits'

// The server's limit, in characters (not bytes, not UTF-16 units).
export const CTI_DESCRIPTION_MAX = 500

// What the server will refuse: more than 500 characters once trimmed.
export function isDescriptionTooLong(value: string): boolean {
  return charCount(value.trim()) > CTI_DESCRIPTION_MAX
}

// Anything with an optional description: a Category, a TicketType or a
// TicketItem. A row from an older server has no key, which reads as none.
export interface Described {
  description?: string
}

/**
 * What to show for the chosen levels: the item's description if it has one,
 * otherwise the type's, otherwise the category's, otherwise nothing ('').
 * A level that is not chosen is undefined or null. Text is returned trimmed and
 * otherwise as it is: it is plain text and must be rendered as text.
 *
 * This is the one place the rule lives, so the pages cannot drift apart.
 */
export function mostSpecificDescription(
  category?: Described | null,
  type?: Described | null,
  item?: Described | null,
): string {
  for (const level of [item, type, category]) {
    const text = level?.description?.trim()
    if (text) return text
  }
  return ''
}
