import { useCallback, useState } from 'react'
import { contextBox, isTransparent, paintPng, readBoxes, type Measure } from '@/components/charts/exportImage'
import { saveDataUrl } from '@/lib/saveFile'
import { withLightTheme } from '@/lib/theme'

// The glue between a Reports element on screen and a PNG file: read the
// element's layout back as boxes, add the strip that says which period and
// filters it shows, paint, save. The pieces are in charts/exportImage.ts; this
// only joins them to the page's own element and the browser's canvas.

// Text width as the canvas measures it. Where there is no canvas (jsdom) a
// character is 7 px: only the strip's wrapping depends on it.
function canvasMeasure(): Measure {
  const ctx = document.createElement('canvas').getContext('2d')
  return (text, font) => {
    if (!ctx) return text.length * 7
    ctx.font = font
    return ctx.measureText(text).width
  }
}

// The colour behind an element: its own computed background-color, or its
// nearest ancestor's that has one. Read it inside withLightTheme (see
// readLight below): the picture is always light.
function backgroundOf(el: HTMLElement): string {
  for (let e: HTMLElement | null = el; e; e = e.parentElement) {
    const c = window.getComputedStyle(e).getPropertyValue('background-color')
    if (!isTransparent(c)) return c
  }
  return '#ffffff' // theme-ok: the export is always light, and this is the paper colour
}

// An image is always light (printing, pasting), whatever theme the person uses
// (#709). The layout and the colours are read with the page's dark class taken
// off and put back in the same task, so no frame is painted in between and
// nothing flashes. data-theme-switching switches transitions off for the same
// moment: a transition would otherwise report the dark colour it starts from.
// paintPng is async and runs afterwards on values already read.
function readLight(el: HTMLElement, contextLines: string[], measure: Measure) {
  const html = document.documentElement
  const hadFlag = html.hasAttribute('data-theme-switching')
  html.setAttribute('data-theme-switching', '')
  try {
    return withLightTheme(() => {
      const { width, height } = el.getBoundingClientRect()
      const boxes = readBoxes(el, measure)
      const strip = contextBox(contextLines, width, measure, height)
      return { width, height, boxes, strip, background: backgroundOf(el) }
    })
  } finally {
    if (!hadFlag) html.removeAttribute('data-theme-switching')
  }
}

export async function exportElement(el: HTMLElement, contextLines: string[], filename: string): Promise<void> {
  const { width, height, boxes, strip, background } = readLight(el, contextLines, canvasMeasure())
  const url = await paintPng([...boxes, ...strip.boxes], width, height + strip.h, background)
  saveDataUrl(url, filename)
}

export const IMAGE_FAILED = 'The image could not be created in this browser. Download the CSV for the same numbers.'

export interface ImageExport {
  busy: boolean
  failed: boolean
  run: (el: HTMLElement | null, contextLines: string[], filename: string) => Promise<void>
}

// One image button's state: idle, busy (the button is disabled) or failed (the
// message shows beside it until the next try). Nothing is saved on a failure.
export function useImageExport(): ImageExport {
  const [state, setState] = useState<'idle' | 'busy' | 'failed'>('idle')
  const run = useCallback(async (el: HTMLElement | null, contextLines: string[], filename: string) => {
    if (!el) return
    setState('busy')
    try {
      await exportElement(el, contextLines, filename)
      setState('idle')
    } catch {
      setState('failed')
    }
  }, [])
  return { busy: state === 'busy', failed: state === 'failed', run }
}
