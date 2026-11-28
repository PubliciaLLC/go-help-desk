import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The image is always light (#709): the page is read with html.dark taken off
// and put back in the same task. readBoxes is the thing that reads the page, so
// it is the probe; the painting and the saving are out of the picture.
const seen = vi.hoisted(() => ({ dark: [] as boolean[], switching: [] as boolean[], throwIn: false }))

vi.mock('@/components/charts/exportImage', async (orig) => {
  const real = await orig<typeof import('@/components/charts/exportImage')>()
  return {
    ...real,
    readBoxes: () => {
      seen.dark.push(document.documentElement.classList.contains('dark'))
      seen.switching.push(document.documentElement.hasAttribute('data-theme-switching'))
      if (seen.throwIn) throw new Error('layout failed')
      return []
    },
    paintPng: vi.fn(async () => 'data:image/png;base64,AAAA'),
  }
})
vi.mock('@/lib/saveFile', () => ({ saveDataUrl: vi.fn() }))

import { exportElement } from './exportElement'

describe('exportElement in the dark theme', () => {
  let el: HTMLElement
  beforeEach(() => {
    seen.dark = []
    seen.switching = []
    seen.throwIn = false
    document.documentElement.classList.add('dark')
    el = document.createElement('div')
    document.body.appendChild(el)
  })
  afterEach(() => {
    document.documentElement.classList.remove('dark')
    document.documentElement.removeAttribute('data-theme-switching')
    el.remove()
  })

  it('reads the page without the dark class and puts it back', async () => {
    await exportElement(el, ['Last 30 days'], 'r.png')
    expect(seen.dark).toEqual([false])
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('switches transitions off while it reads, and back on after', async () => {
    await exportElement(el, [], 'r.png')
    expect(seen.switching).toEqual([true])
    expect(document.documentElement.hasAttribute('data-theme-switching')).toBe(false)
  })

  it('puts the dark class back when reading throws', async () => {
    seen.throwIn = true
    await expect(exportElement(el, [], 'r.png')).rejects.toThrow('layout failed')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(document.documentElement.hasAttribute('data-theme-switching')).toBe(false)
  })

  it('leaves a light page alone', async () => {
    document.documentElement.classList.remove('dark')
    await exportElement(el, [], 'r.png')
    expect(seen.dark).toEqual([false])
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
