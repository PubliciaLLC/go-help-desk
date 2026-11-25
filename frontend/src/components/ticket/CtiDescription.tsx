import { mostSpecificDescription, type Described } from '@/lib/ctiDescription'

/**
 * The description of the chosen Category / Type / Item, beneath the chooser.
 *
 * The region is always in the page and is a polite live region, so a change is
 * announced; the text inside it exists only while there is some, so with nothing
 * to say there is no box and no height (and no margin: the gap belongs to the
 * text, not to the region). `id` is what the selectors name in aria-describedby.
 *
 * The text is a React text node, never HTML or Markdown, and `whitespace-pre-line`
 * turns the newlines an administrator typed into line breaks. It is plain text
 * on the page, not a hover tooltip: a native <select> cannot give an <option>
 * one, and a hover does not exist on a phone.
 */
export function CtiDescription({
  id,
  category,
  type,
  item,
}: {
  id: string
  category?: Described | null
  type?: Described | null
  item?: Described | null
}) {
  const text = mostSpecificDescription(category, type, item)
  return (
    <div id={id} aria-live="polite">
      {text && <p className="mt-2 whitespace-pre-line wrap-anywhere text-sm text-gray-600">{text}</p>}
    </div>
  )
}
