import { THEME_COLOR, type ResolvedTheme } from '@/lib/theme'

// Ticket statuses carry a colour an administrator chose (#709). In light it is
// shown as chosen. In dark a dark navy on the dark card is unreadable, so the
// text and border colour is mixed toward white until it reaches AA (4.5:1)
// against the card. Dots and the dashboard card's left border keep the raw
// colour: they are not text.

type RGB = [number, number, number]

const HEX = /^#(?:[0-9a-f]{3}|[0-9a-f]{6})$/i
const STEP = 0.1
const MAX_STEPS = 10
const AA = 4.5

function parseHex(hex: string): RGB | null {
  if (!HEX.test(hex)) return null
  const h = hex.length === 4 ? [...hex.slice(1)].map((c) => c + c).join('') : hex.slice(1)
  return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16)) as RGB
}

function toHex(rgb: RGB): string {
  return '#' + rgb.map((v) => Math.round(Math.min(255, Math.max(0, v))).toString(16).padStart(2, '0')).join('')
}

const toLinear = (v: number) => {
  const s = v / 255
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
}
const fromLinear = (l: number) => 255 * (l <= 0.0031308 ? l * 12.92 : 1.055 * l ** (1 / 2.4) - 0.055)

function luminance([r, g, b]: RGB): number {
  return 0.2126 * toLinear(r) + 0.7152 * toLinear(g) + 0.0722 * toLinear(b)
}

/** WCAG contrast ratio of two hex colours; 1 when either is not hex. */
export function contrastRatio(a: string, b: string): number {
  const ca = parseHex(a)
  const cb = parseHex(b)
  if (!ca || !cb) return 1
  const [hi, lo] = [luminance(ca), luminance(cb)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

// sRGB <-> OKLab (Bjorn Ottosson's matrices).
function toOklab(rgb: RGB): RGB {
  const [r, g, b] = rgb.map(toLinear)
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ]
}

function fromOklab([L, a, b]: RGB): RGB {
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  return [
    fromLinear(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    fromLinear(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    fromLinear(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
  ]
}

const WHITE_LAB = toOklab([255, 255, 255])

/**
 * The colour to draw status text and borders in. Light: the colour as chosen.
 * Dark: the colour, lightened in 10% steps toward white until it reads on the
 * dark card. Anything that is not a hex colour comes back as it was.
 */
export function readableStatusColor<T extends string | null | undefined>(hex: T, theme: ResolvedTheme): T {
  if (theme === 'light' || !hex) return hex
  const rgb = parseHex(hex)
  if (!rgb) return hex
  const card = THEME_COLOR.dark
  if (contrastRatio(hex, card) >= AA) return hex
  const lab = toOklab(rgb)
  for (let i = 1; i <= MAX_STEPS; i++) {
    const t = i * STEP
    const mixed = toHex(fromOklab(lab.map((v, k) => v + (WHITE_LAB[k] - v) * t) as RGB))
    if (i === MAX_STEPS || contrastRatio(mixed, card) >= AA) return mixed as T
  }
  return hex
}
