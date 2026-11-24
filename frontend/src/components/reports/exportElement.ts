import { useCallback, useState } from 'react'
import { contextBox, isTransparent, paintPng, readBoxes, type Measure } from '@/components/charts/exportImage'
import { saveDataUrl } from '@/lib/saveFile'

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
// nearest ancestor's that has one, so a dark page gives a dark image.
function backgroundOf(el: HTMLElement): string {
  for (let e: HTMLElement | null = el; e; e = e.parentElement) {
    const c = window.getComputedStyle(e).getPropertyValue('background-color')
    if (!isTransparent(c)) return c
  }
  return '#ffffff'
}

export async function exportElement(el: HTMLElement, contextLines: string[], filename: string): Promise<void> {
  const measure = canvasMeasure()
  const { width, height } = el.getBoundingClientRect()
  const boxes = readBoxes(el, measure)
  const strip = contextBox(contextLines, width, measure, height)
  const url = await paintPng([...boxes, ...strip.boxes], width, height + strip.h, backgroundOf(el))
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
