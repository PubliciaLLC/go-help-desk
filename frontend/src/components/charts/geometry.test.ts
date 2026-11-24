import { describe, expect, it } from 'vitest'
import { areaPath, linePath, niceCeiling, pointsFor } from './geometry'

describe('niceCeiling', () => {
  it.each([
    [0, 1],
    [-3, 1],
    [1, 1],
    [7, 10],
    [10, 10],
    [11, 20],
    [999, 1000],
  ])('niceCeiling(%i) is %i', (n, want) => {
    expect(niceCeiling(n)).toBe(want)
  })
})

describe('pointsFor', () => {
  it('puts a single value at the centre', () => {
    expect(pointsFor([5], 100, 50, 10)).toEqual([[50, 25]])
  })

  it('spans x from 0 to width for two values', () => {
    expect(pointsFor([0, 10], 100, 50, 10)).toEqual([
      [0, 50],
      [100, 0],
    ])
  })

  it('spaces thirty values evenly from 0 to width', () => {
    const pts = pointsFor(Array.from({ length: 30 }, () => 0), 290, 40, 10)
    expect(pts).toHaveLength(30)
    expect(pts[0][0]).toBe(0)
    expect(pts[29][0]).toBe(290)
    expect(pts[1][0] - pts[0][0]).toBeCloseTo(290 / 29)
    expect(pts.every(([, y]) => y === 40)).toBe(true)
  })

  it('treats a non-positive max as 1', () => {
    expect(pointsFor([0.5], 100, 50, 0)).toEqual([[50, 25]])
    expect(pointsFor([0.5], 100, 50, -4)).toEqual([[50, 25]])
  })

  it('returns no points for no values', () => {
    expect(pointsFor([], 100, 50, 10)).toEqual([])
  })
})

describe('linePath and areaPath', () => {
  const pts: [number, number][] = [
    [0, 50],
    [50, 25],
    [100, 0],
  ]

  it('draws the line through every point', () => {
    expect(linePath(pts)).toBe('M0 50 L50 25 L100 0')
  })

  it('closes the area down to the baseline', () => {
    expect(areaPath(pts, 50)).toBe('M0 50 L50 25 L100 0 L100 50 L0 50 Z')
  })

  it('is empty for no points', () => {
    expect(linePath([])).toBe('')
    expect(areaPath([], 50)).toBe('')
  })
})
