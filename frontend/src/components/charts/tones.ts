// Chart tones. Marks are drawn in currentColor, so a tone is a text class and
// nothing else. The chart tokens are lighter in dark (index.css).
export type ChartTone = 'blue' | 'green' | 'amber' | 'red' | 'gray'

export const TONE_CLASS: Record<ChartTone, string> = {
  blue: 'text-chart-blue',
  green: 'text-chart-green',
  amber: 'text-chart-amber',
  red: 'text-chart-red',
  gray: 'text-chart-gray',
}
