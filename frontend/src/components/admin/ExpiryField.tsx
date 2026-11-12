import { Input } from '@/components/ui/input'
import { NO_LIMIT, maxExpiryDate, pastLimit } from '@/lib/expiry'

/**
 * The date a key stops working.
 *
 * A date, not a time: nobody picks a key's expiry to the minute. expiryToISO
 * turns the chosen day into an instant. While the instance has a limit (2 years
 * unless an administrator changes it) the field is required and capped at it,
 * which the server enforces too; only with no limit may it be left empty, for a
 * key that never expires.
 */
export function ExpiryField({
  value,
  onChange,
  maxYears,
  disabled,
}: {
  value: string
  onChange: (v: string) => void
  /** The instance's limit in years, or NO_LIMIT. */
  maxYears: number
  disabled?: boolean
}) {
  const limited = maxYears !== NO_LIMIT
  const tooLate = pastLimit(value, maxYears)
  return (
    <label className="block text-sm text-gray-700">
      <span className="font-medium">Expires</span>
      <span className="ml-2 text-xs text-gray-500">
        {limited
          ? `required — at most ${maxYears === 1 ? '1 year' : `${maxYears} years`} from today`
          : 'optional — leave empty for a key that never expires'}
      </span>
      <Input
        type="date"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        required={limited}
        max={limited ? maxExpiryDate(maxYears) : undefined}
        className="mt-1 max-w-xs"
      />
      {tooLate && (
        <span role="alert" className="mt-1 block text-xs text-red-600">
          That is later than the {maxYears === 1 ? '1 year' : `${maxYears} year`} limit.
        </span>
      )}
    </label>
  )
}
