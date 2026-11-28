/** Used when there is no usable width (a hidden ancestor, a font size that would not parse). */
export const DEFAULT_PER_ROW = 80

// East Asian wide and fullwidth characters, and emoji: each is about two Latin
// characters wide, so a row holds half as many. Ranges, not a table of the
// standard: close enough for a height estimate, and small enough to audit.
function isWide(cp: number): boolean {
  return (
    (cp >= 0x1100 && cp <= 0x115f) || // Hangul Jamo
    (cp >= 0x2e80 && cp <= 0xa4cf) || // CJK radicals, kana, symbols, ideographs, Yi
    (cp >= 0xac00 && cp <= 0xd7a3) || // Hangul syllables
    (cp >= 0xf900 && cp <= 0xfaff) || // CJK compatibility ideographs
    (cp >= 0xfe30 && cp <= 0xfe6f) || // CJK compatibility forms, small forms
    (cp >= 0xff00 && cp <= 0xff60) || // fullwidth forms
    (cp >= 0xffe0 && cp <= 0xffe6) || // fullwidth signs
    (cp >= 0x1f300 && cp <= 0x1faff) || // emoji and pictographs
    (cp >= 0x20000 && cp <= 0x3fffd) // CJK extensions
  )
}

/**
 * How many rows plain text takes when a row holds `perRow` Latin characters. A
 * stored line is at least one row, and a longer one wraps. A wide character
 * counts as `wideUnits` Latin characters (two unless the caller measured it) and
 * a character beyond the BMP, a surrogate pair, as one character, not two. An
 * estimate: it ignores where a word would have broken and the width of
 * individual letters, so text of narrow letters ("iiii") comes out about twice
 * too high and wide ones ("WWWW", emoji) about 0.6 to 0.75 of the drawn height.
 *
 * A `perRow` that is not a usable width (0, NaN, under one character) is the
 * default width, never one row per character.
 */
export function plainRows(text: string, perRow: number, wideUnits = 2): number {
  const per = perRow >= 1 && Number.isFinite(perRow) ? Math.floor(perRow) : DEFAULT_PER_ROW
  const wide = wideUnits > 0 && Number.isFinite(wideUnits) ? wideUnits : 2
  let rows = 0
  let units = 0
  const endRow = () => {
    rows += Math.max(1, Math.ceil(units / per))
    units = 0
  }
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i)
    if (c === 10) {
      endRow()
      continue
    }
    let cp = c
    // A high surrogate followed by a low one is one character.
    if (c >= 0xd800 && c <= 0xdbff && i + 1 < text.length) {
      const low = text.charCodeAt(i + 1)
      if (low >= 0xdc00 && low <= 0xdfff) {
        cp = 0x10000 + ((c - 0xd800) << 10) + (low - 0xdc00)
        i++
      }
    }
    units += isWide(cp) ? wide : 1
  }
  endRow()
  return rows
}
