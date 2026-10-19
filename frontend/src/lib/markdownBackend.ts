import type { Root } from 'hast'
import MarkdownWorker from './markdown.worker?worker'
import type { FormatResult } from './markdownRunner'

// #443: the real backend: formatting in a Web Worker under a time budget. Tests
// run markdownBackend.inthread.ts instead (jsdom has no Worker); see vite.config.ts.

/** How long one post may take to format once the worker is running. */
export const BUDGET_MS = 1000
/** How long a new worker may take to start before we give up on it. */
const STARTUP_MS = 10_000

interface Job {
  id: number
  text: string
  resolve: (r: FormatResult) => void
}

// One shared worker, one job at a time: a page of replies does not start a
// worker (and load the parser) per reply. A job that overruns terminates the
// worker; the next job starts a fresh one.
class WorkerClient {
  private worker: Worker | null = null
  private ready = false
  private queue: Job[] = []
  private current: Job | null = null
  private timer: ReturnType<typeof setTimeout> | null = null
  private nextId = 1

  format(text: string): Promise<FormatResult> {
    return new Promise((resolve) => {
      this.queue.push({ id: this.nextId++, text, resolve })
      this.pump()
    })
  }

  private spawn() {
    const worker = new MarkdownWorker()
    this.worker = worker
    this.ready = false
    worker.onmessage = (e: MessageEvent) => {
      if (this.worker !== worker) return
      const data = e.data as { type?: string; id?: number; ok?: boolean; tree?: Root; reason?: string }
      if (data.type === 'ready') {
        this.ready = true
        this.pump()
        return
      }
      const job = this.current
      if (!job || data.id !== job.id) return
      this.finish(
        data.ok && data.tree
          ? { ok: true, tree: data.tree }
          : { ok: false, reason: data.reason === 'complex' ? 'complex' : 'error' },
      )
    }
    worker.onerror = () => {
      if (this.worker !== worker) return
      this.kill()
      this.failAll('error')
    }
    this.startTimer(STARTUP_MS, 'error')
  }

  private startTimer(ms: number, reason: 'timeout' | 'error') {
    this.clearTimer()
    this.timer = setTimeout(() => {
      this.kill()
      // A job that overran gets 'timeout' and the next one a fresh worker. A
      // worker that never started fails everything waiting for it: retrying a
      // broken worker for every post would never end.
      if (this.current) this.finish({ ok: false, reason })
      else this.failAll(reason)
    }, ms)
  }

  private clearTimer() {
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
  }

  private kill() {
    this.clearTimer()
    this.worker?.terminate()
    this.worker = null
    this.ready = false
  }

  private failAll(reason: 'error' | 'timeout') {
    const jobs = [...(this.current ? [this.current] : []), ...this.queue.splice(0)]
    this.current = null
    for (const j of jobs) j.resolve({ ok: false, reason })
  }

  private finish(result: FormatResult) {
    this.clearTimer()
    const job = this.current
    this.current = null
    job?.resolve(result)
    this.pump()
  }

  private pump() {
    if (this.current || this.queue.length === 0) return
    if (!this.worker) {
      try {
        this.spawn()
      } catch {
        // No worker (blocked, or not supported): every post is shown as written.
        for (const j of this.queue.splice(0)) j.resolve({ ok: false, reason: 'error' })
        return
      }
    }
    if (!this.ready) return
    this.current = this.queue.shift()!
    this.startTimer(BUDGET_MS, 'timeout')
    this.worker!.postMessage({ id: this.current.id, text: this.current.text })
  }
}

let client: WorkerClient | null = null

export function runFormat(text: string): Promise<FormatResult> {
  client ??= new WorkerClient()
  return client.format(text)
}
