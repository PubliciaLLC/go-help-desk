import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { CTI_DESCRIPTION_MAX, isDescriptionTooLong } from '@/lib/ctiDescription'
import { charCount } from '@/lib/fieldLimits'

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
  const count = charCount(value)
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
          Shown beneath the Category / Type / Item chooser on the new-ticket form. Plain text, no formatting; line
          breaks are kept. Anyone who can pick this entry can read it, including visitors on the guest form when
          guest submission is on.
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
