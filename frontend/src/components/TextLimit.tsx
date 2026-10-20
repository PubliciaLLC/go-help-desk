import { MAX_TEXT_BYTES, isOverLimit, nearLimit, textBytes } from '@/lib/textLimit'

const fmt = (n: number) => n.toLocaleString('en-US')

/**
 * The counter under a description or reply field. Silent until 90 percent of the
 * limit, then a count; past it, an alert saying how far over. The live region is
 * always present, so a screen reader hears the change when it appears.
 */
export function TextLimit({ value }: { value: string }) {
  const over = isOverLimit(value)
  const near = nearLimit(value)
  const bytes = near ? textBytes(value) : 0
  return (
    <div aria-live="polite" className="text-xs">
      {over ? (
        <p role="alert" className="text-red-600">
          {fmt(bytes - MAX_TEXT_BYTES)} too many characters: {fmt(bytes)} / {fmt(MAX_TEXT_BYTES)}. Shorten it to send.
        </p>
      ) : near ? (
        <p className="text-gray-500">
          {fmt(bytes)} / {fmt(MAX_TEXT_BYTES)}
        </p>
      ) : null}
    </div>
  )
}
