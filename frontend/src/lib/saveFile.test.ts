import { afterEach, describe, expect, it, vi } from 'vitest'
import { saveDataUrl } from './saveFile'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('saveDataUrl', () => {
  it('clicks a temporary download link carrying the URL and name, then removes it', () => {
    const seen: Array<{ download: string | null; href: string | null; connected: boolean }> = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      seen.push({
        download: this.getAttribute('download'),
        href: this.getAttribute('href'),
        connected: this.isConnected,
      })
    })

    saveDataUrl('data:text/csv;charset=utf-8,a%2Cb', 'kpis-2026-11-01-to-2026-11-24.csv')

    expect(seen).toEqual([
      {
        download: 'kpis-2026-11-01-to-2026-11-24.csv',
        href: 'data:text/csv;charset=utf-8,a%2Cb',
        connected: true,
      },
    ])
    expect(document.querySelectorAll('a')).toHaveLength(0)
  })
})
