// Pure chart geometry. No DOM, no React.

// The smallest of 1, 2 or 5 times a power of ten that is at or above n.
// Zero and below give 1, so an all-zero chart still has an axis.
export function niceCeiling(n: number): number {
  if (n <= 0) return 1
  let base = 1
  while (base * 10 < n) base *= 10
  for (const m of [1, 2, 5, 10]) {
    if (base * m >= n) return base * m
  }
  return base * 10
}

// Evenly spaced from x = 0 to x = width; a single value sits at the centre.
// y is measured down from the top. A max of zero or less is treated as 1.
export function pointsFor(
  values: number[],
  width: number,
  height: number,
  max: number,
): [number, number][] {
  const top = max > 0 ? max : 1
  const n = values.length
  return values.map((v, i) => {
    const x = n === 1 ? width / 2 : (i * width) / (n - 1)
    const y = height * (1 - v / top)
    return [x, y]
  })
}

export function linePath(points: [number, number][]): string {
  if (points.length === 0) return ''
  return points.map(([x, y], i) => `${i === 0 ? 'M' : 'L'}${x} ${y}`).join(' ')
}

// The line, then straight down to the baseline and back to the start.
export function areaPath(points: [number, number][], height: number): string {
  if (points.length === 0) return ''
  const [firstX] = points[0]
  const [lastX] = points[points.length - 1]
  return `${linePath(points)} L${lastX} ${height} L${firstX} ${height} Z`
}
