import { Input } from '@/components/ui/input'

/**
 * The date a key stops working, or empty for never.
 *
 * A date, not a time: nobody picks a key's expiry to the minute. The server
 * wants an instant, so expiryToISO turns the chosen day into the last second of
 * that day in the person's own time zone (see expiryToISO). The server still
 * refuses one in the past, and says so.
 */
export function ExpiryField({
  value,
  onChange,
  disabled,
}: {
  value: string
  onChange: (v: string) => void
  disabled?: boolean
}) {
  return (
    <label className="block text-sm text-gray-700">
      <span className="font-medium">Expires</span>
      <span className="ml-2 text-xs text-gray-500">optional — leave empty for a key that never expires</span>
      <Input
        type="date"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="mt-1 max-w-xs"
      />
    </label>
  )
}
