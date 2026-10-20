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
  // Rounded up, so that one byte over still reads as over.
  const percent = Math.ceil(((bytes - MAX_TEXT_BYTES) / MAX_TEXT_BYTES) * 100)
  return (
    <div aria-live="polite" className="text-xs">
      {over ? (
        <p role="alert" className="text-red-600">
          This is {percent}% over the size limit ({fmt(MAX_TEXT_BYTES)} bytes). Letters outside basic English and emoji take 2
          to 4 bytes each. Shorten it to send.
        </p>
      ) : near ? (
        <p className="text-gray-500">
          {fmt(bytes)} / {fmt(MAX_TEXT_BYTES)} bytes
        </p>
      ) : null}
    </div>
  )
}
