// Chart tones. Marks are drawn in currentColor, so a tone is a text class and
// nothing else. Only classes already in use elsewhere in the app.
export type ChartTone = 'blue' | 'green' | 'amber' | 'red' | 'gray'

export const TONE_CLASS: Record<ChartTone, string> = {
  blue: 'text-blue-600',
  green: 'text-green-600',
  amber: 'text-amber-600',
  red: 'text-red-600',
  gray: 'text-gray-500',
}
