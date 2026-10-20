import { useState } from 'react'
import { MAX_TEXT_BYTES, isOverLimit, nearLimit, textBytes } from '@/lib/textLimit'

const fmt = (n: number) => n.toLocaleString('en-US')

type Level = 'ok' | 'near' | 'over'

/**
 * The counter under a description or reply field.
 *
 * Silent until 90 percent of the limit, then a count in bytes; past the limit, a
 * message saying how far over. What a screen reader hears is separate from what
 * is shown, because a count that changes on every keystroke must not be read on
 * every keystroke:
 *  - the visible count and percent are in no live region;
 *  - crossing 90 percent is announced once ("Approaching the size limit") in a
 *    polite status region, and coming back under once ("Back under the size
 *    limit");
 *  - going over is announced once, by an alert whose message does not change as
 *    the count does.
 */
export function TextLimit({ value }: { value: string }) {
  const over = isOverLimit(value)
  const level: Level = over ? 'over' : nearLimit(value) ? 'near' : 'ok'
  const bytes = level === 'ok' ? 0 : textBytes(value)
  // Integer arithmetic: 3,500 / 50,000 * 100 is 7.000000000000001 in floating point.
  // Rounded up, so that one byte over still reads as over.
  const percent = Math.ceil(((bytes - MAX_TEXT_BYTES) * 100) / MAX_TEXT_BYTES)

  // Derived while rendering, from the level the last render had: the message
  // changes only when the level does, never on a keystroke within a level.
  const [announcement, setAnnouncement] = useState('')
  const [previous, setPrevious] = useState<Level>(level)
  if (previous !== level) {
    setPrevious(level)
    if (level === 'near') setAnnouncement(previous === 'over' ? 'Back under the size limit' : 'Approaching the size limit')
    else setAnnouncement('') // over: the alert speaks. ok: nothing to say.
  }

  return (
    <div className="text-xs">
      <div aria-live="polite" className="sr-only">
        {announcement}
      </div>
      {level === 'over' ? (
        <>
          <p className="text-red-600">
            This is {percent}% over the size limit ({fmt(MAX_TEXT_BYTES)} bytes). Letters outside basic English and emoji take
            2 to 4 bytes each. Shorten it to send.
          </p>
          <p role="alert" className="sr-only">
            This text is over the size limit ({fmt(MAX_TEXT_BYTES)} bytes). Shorten it to send.
          </p>
        </>
      ) : level === 'near' ? (
        <p className="text-gray-500">
          {fmt(bytes)} / {fmt(MAX_TEXT_BYTES)} bytes
        </p>
      ) : null}
    </div>
  )
}
