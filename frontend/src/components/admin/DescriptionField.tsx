import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { CTI_DESCRIPTION_MAX, isDescriptionTooLong, trimDescription } from '@/lib/ctiDescription'
import { charCount } from '@/lib/fieldLimits'

// Quoted verbatim in docs/DESIGN.md (Descriptions -> Admin). Public means public:
// the catalogue routes serve every ACTIVE category, type and item to anyone while
// guest submission is on, and to any signed-in user always, whatever the level.
const DESCRIPTION_HINT =
  'Shown beneath the Category / Type / Item chooser on the new-ticket form, the guest form (categories only) and ' +
  'the reclassify card on a ticket. Plain text, no formatting; line breaks are kept. This text is public: while ' +
  'guest submission is on, anyone can read the description of every active category, type and item, and any ' +
  'signed-in user can read them at any time. Do not put anything here you would not put on a public web page.'

/**
 * The optional Description of a category, type or item (create and edit).
 *
 * No maxLength attribute: it counts UTF-16 units, so it would stop a person
 * short on emoji and say nothing. The counter counts characters as the server
 * does, and the caller disables its button while `isDescriptionTooLong`.
 */
export function DescriptionField({
  id,
  value,
  onChange,
}: {
  id: string
  value: string
  onChange: (value: string) => void
}) {
  // The trimmed text, as the limit is applied to it.
  const count = charCount(trimDescription(value))
  const over = isDescriptionTooLong(value)
  const hintId = `${id}-hint`
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs text-gray-600">
        Description <span className="font-normal text-gray-400">(optional)</span>
      </Label>
      <Textarea
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={3}
        aria-describedby={hintId}
        aria-invalid={over || undefined}
        className="text-sm"
      />
      <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-1 text-xs">
        <p id={hintId} className="min-w-0 flex-1 text-gray-500 wrap-anywhere">
          {DESCRIPTION_HINT}
        </p>
        <span className={over ? 'font-medium text-red-600' : 'text-gray-500'}>
          {count} / {CTI_DESCRIPTION_MAX}
        </span>
      </div>
      {over && (
        <p role="alert" className="text-xs text-red-600">
          This description is too long. Shorten it to {CTI_DESCRIPTION_MAX} characters or fewer to save.
        </p>
      )}
    </div>
  )
}
