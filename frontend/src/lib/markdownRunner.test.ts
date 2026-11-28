import { beforeEach, describe, expect, it, vi } from 'vitest'

// #443: what the runner remembers. A result that failed for a reason that may
// not repeat (the worker broke, a slow machine ran out of budget) must not stick;
// and the memory of past results is small, because each holds a tree.

const runFormat = vi.hoisted(() => vi.fn())
vi.mock('@/lib/markdownBackend', () => ({ runFormat }))

import { formatMarkdown } from './markdownRunner'

const ok = { ok: true as const, tree: { type: 'root' as const, children: [] } }

beforeEach(() => {
  runFormat.mockReset()
  vi.resetModules()
})

describe('formatMarkdown caching', () => {
  it('remembers a result, so the same text is not formatted twice', async () => {
    runFormat.mockResolvedValue(ok)
    await formatMarkdown('remember me')
    await formatMarkdown('remember me')
    expect(runFormat).toHaveBeenCalledTimes(1)
  })

  it.each(['timeout', 'error', 'unavailable'])('does not remember %s: it is tried again on the next view', async (reason) => {
    runFormat.mockResolvedValueOnce({ ok: false, reason }).mockResolvedValueOnce(ok)
    expect((await formatMarkdown(`retry ${reason}`)).ok).toBe(false)
    expect((await formatMarkdown(`retry ${reason}`)).ok).toBe(true)
    expect(runFormat).toHaveBeenCalledTimes(2)
  })

  it('does remember a refusal that will not change', async () => {
    runFormat.mockResolvedValue({ ok: false, reason: 'complex' })
    await formatMarkdown('stays complex')
    await formatMarkdown('stays complex')
    expect(runFormat).toHaveBeenCalledTimes(1)
  })

  it('keeps at most 20 results', async () => {
    runFormat.mockResolvedValue(ok)
    for (let i = 0; i < 25; i++) await formatMarkdown(`post ${i}`)
    runFormat.mockClear()
    await formatMarkdown('post 24') // recent: still there
    expect(runFormat).not.toHaveBeenCalled()
    await formatMarkdown('post 0') // oldest: gone
    expect(runFormat).toHaveBeenCalledTimes(1)
  })

  it('says how big a result is, for the page budget', async () => {
    runFormat.mockResolvedValue({
      ok: true,
      tree: { type: 'root', children: [{ type: 'element', tagName: 'p', properties: {}, children: [{ type: 'text', value: 'a' }] }] },
    })
    const r = await formatMarkdown('sized')
    expect(r.ok && r.nodes).toBe(2)
  })
})
