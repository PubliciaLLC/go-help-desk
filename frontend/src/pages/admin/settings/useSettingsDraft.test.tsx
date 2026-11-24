import type { ReactNode } from 'react'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, configure, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AxiosError } from 'axios'
import { api } from '@/api/client'
import { useSettingsDraft, type SettingsDraft } from './useSettingsDraft'

// RTL_STRICT=1 runs these under React's StrictMode (double render, double effects).
if (process.env.RTL_STRICT) configure({ reactStrictMode: true })

// #674: one draft per page. The hook owns the draft, the write-only keys, the
// patch and the save; these tests pin what the pages rely on.

const DUMP = { sla_enabled: false, ticket_scope_enforced: false, allowed: ['.pdf'] }

function setup(opts: { cached?: Record<string, unknown> } = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity }, mutations: { retry: false } },
  })
  if (opts.cached) queryClient.setQueryData(['admin', 'settings'], opts.cached)
  function wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  }
  // Every render's view of the draft, so a test can look at the first render
  // in which something became true, not only at where it ended up.
  const renders: { isLoading: boolean; stored: Record<string, unknown> | undefined; sla: boolean; dirty: boolean }[] = []
  const hook = renderHook(
    () => {
      const d = useSettingsDraft()
      renders.push({ isLoading: d.isLoading, stored: d.stored, sla: d.bool('sla_enabled'), dirty: d.dirty })
      return d
    },
    { wrapper },
  )
  return { ...hook, queryClient, renders }
}

let dump: Record<string, unknown>
let patchSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  dump = { ...DUMP }
  vi.spyOn(api, 'get').mockImplementation((() => Promise.resolve({ data: { ...dump } })) as never)
  patchSpy = vi.spyOn(api, 'patch').mockImplementation(((_url: string, body: Record<string, unknown>) => {
    dump = { ...dump, ...body }
    return Promise.resolve({ data: undefined })
  }) as never)
})
afterEach(() => vi.restoreAllMocks())

function refusal(code: string, message: string) {
  const err = new AxiosError('Request failed with status code 400')
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  err.response = { status: 400, data: { error: { code, message } } } as any
  return err
}

const loaded = (r: { current: SettingsDraft }) => waitFor(() => expect(r.current.isLoading).toBe(false))

describe('useSettingsDraft', () => {
  it('is not dirty while loading, nor after the load', async () => {
    const { result } = setup()
    expect(result.current.isLoading).toBe(true)
    expect(result.current.dirty).toBe(false)
    await loaded(result)
    expect(result.current.dirty).toBe(false)
    expect(result.current.stored).toEqual(DUMP)
  })

  it('is dirty after an edit, and again clean when the edit is put back', async () => {
    const { result } = setup()
    await loaded(result)
    act(() => result.current.setBool('sla_enabled', true))
    expect(result.current.dirty).toBe(true)
    act(() => result.current.setBool('sla_enabled', false))
    expect(result.current.dirty).toBe(false)
  })

  it('is dirty after typing a write-only key, and not once it is blank again', async () => {
    const { result } = setup()
    await loaded(result)
    act(() => result.current.setSecret('virustotal_api_key', 'abc'))
    expect(result.current.secret('virustotal_api_key')).toBe('abc')
    expect(result.current.dirty).toBe(true)
    act(() => result.current.setSecret('virustotal_api_key', '   '))
    expect(result.current.dirty).toBe(false)
    act(() => result.current.setSecretCleared('virustotal_api_key', true))
    expect(result.current.secretCleared('virustotal_api_key')).toBe(true)
    expect(result.current.dirty).toBe(true)
  })

  it('is not dirty while the save is pending, nor after the save and the refetch', async () => {
    const { result, renders } = setup()
    await loaded(result)
    let release: () => void = () => {}
    patchSpy.mockImplementationOnce(((_url: string, body: Record<string, unknown>) =>
      new Promise((resolve) => {
        release = () => {
          dump = { ...dump, ...body }
          resolve({ data: undefined })
        }
      })) as never)
    // The refetch takes a moment, as a real one does.
    vi.spyOn(api, 'get').mockImplementation((() =>
      new Promise((resolve) => setTimeout(() => resolve({ data: { ...dump } }), 30))) as never)
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    const from = renders.length
    await waitFor(() => expect(result.current.isPending).toBe(true))
    expect(result.current.dirty).toBe(false)
    await act(async () => release())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    // The save was held until the fresh dump arrived, so there is no render in
    // which the draft differs from the stored value because of a saved edit.
    expect(result.current.stored?.sla_enabled).toBe(true)
    expect(result.current.dirty).toBe(false)
    expect(result.current.saved).toBe(true)
    // At no render between the click and the end was the saved edit "unsaved".
    expect(renders.slice(from).some((r) => r.dirty)).toBe(false)
  })

  it('sends only what changed, and clears the typed key after a save', async () => {
    const { result } = setup()
    await loaded(result)
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.setSecret('virustotal_api_key', ' k '))
    act(() => result.current.save())
    await waitFor(() => expect(patchSpy).toHaveBeenCalled())
    expect(patchSpy.mock.calls[0][1]).toEqual({ sla_enabled: true, virustotal_api_key: 'k' })
    await waitFor(() => expect(result.current.saved).toBe(true))
    expect(result.current.secret('virustotal_api_key')).toBe('')
  })

  it('keeps every edit, and stays dirty, when the save is refused', async () => {
    const { result } = setup()
    await loaded(result)
    patchSpy.mockRejectedValueOnce(refusal('invalid', 'nope'))
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.setSecret('virustotal_api_key', 'k'))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.error).not.toBe(''))
    expect(result.current.bool('sla_enabled')).toBe(true)
    expect(result.current.secret('virustotal_api_key')).toBe('k')
    expect(result.current.dirty).toBe(true)
    expect(result.current.saved).toBe(false)
  })

  it('says what an admin can do when email is not set up', async () => {
    const { result } = setup()
    await loaded(result)
    patchSpy.mockRejectedValueOnce(refusal('email_required', 'raw'))
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.error).toMatch(/SMTP_HOST and SMTP_FROM/))
  })

  it.each([
    { name: 'turning ticket scope on', key: 'ticket_scope_enforced', about: true },
    { name: 'another setting', key: 'sla_enabled', about: false },
  ])('aboutClients is only for the save that turns scope on: $name', async ({ key, about }) => {
    const { result } = setup()
    await loaded(result)
    patchSpy.mockResolvedValueOnce({ data: { warning: 'w' } } as never)
    act(() => result.current.setBool(key, true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.warning).not.toBeNull())
    expect(result.current.warning).toEqual({ text: 'w', aboutClients: about })
  })

  it('toggles an array entry on and off', async () => {
    const { result } = setup()
    await loaded(result)
    act(() => result.current.toggleStrArr('allowed', '.txt', true))
    expect(result.current.strArr('allowed')).toEqual(['.pdf', '.txt'])
    act(() => result.current.toggleStrArr('allowed', '.pdf', false))
    expect(result.current.strArr('allowed')).toEqual(['.txt'])
  })

  it('is still loading in the render where the data first appears, when nothing was cached', async () => {
    const { result, renders } = setup()
    await loaded(result)
    const first = renders.find((r) => r.stored !== undefined)
    expect(first).toBeDefined()
    expect(first!.isLoading).toBe(true)
    // The first render that is not loading already shows the seeded draft.
    const shown = renders.find((r) => !r.isLoading)
    expect(shown).toBeDefined()
    expect(shown!.stored).toEqual(DUMP)
  })

  it('is seeded in the first render when the dump was already cached', () => {
    const { result, renders } = setup({ cached: { ...DUMP, sla_enabled: true } })
    expect(renders[0].isLoading).toBe(false)
    expect(renders[0].sla).toBe(true)
    expect(result.current.has('allowed')).toBe(true)
    expect(result.current.dirty).toBe(false)
  })

  it('keeps an unsaved edit when the dump is refetched', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    act(() => result.current.setBool('sla_enabled', true))
    // Something else changed on the server, as a section's own save does.
    dump = { ...dump, ticket_scope_enforced: true }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.stored?.ticket_scope_enforced).toBe(true))
    expect(result.current.bool('sla_enabled')).toBe(true)
    expect(result.current.dirty).toBe(true)
  })

  it('lets a key that was not edited follow a refetch', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    act(() => result.current.setBool('sla_enabled', true))
    dump = { ...dump, ticket_scope_enforced: true, allowed: ['.png'] }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.bool('ticket_scope_enforced')).toBe(true))
    expect(result.current.strArr('allowed')).toEqual(['.png'])
  })

  it('keeps an edited array entry across a refetch too', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    act(() => result.current.toggleStrArr('allowed', '.txt', true))
    dump = { ...dump, ticket_scope_enforced: true }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.bool('ticket_scope_enforced')).toBe(true))
    expect(result.current.strArr('allowed')).toEqual(['.pdf', '.txt'])
  })

  it('clears the edited marks after a successful save, so the next refetch is followed', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.saved).toBe(true))
    expect(result.current.dirty).toBe(false)
    // The server now says something else about the key that was saved.
    dump = { ...dump, sla_enabled: false }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.stored?.sla_enabled).toBe(false))
    expect(result.current.bool('sla_enabled')).toBe(false)
    expect(result.current.dirty).toBe(false)
  })

  it('keeps an edit made while a save is pending, and stays dirty for it', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    let release: () => void = () => {}
    patchSpy.mockImplementationOnce(((_url: string, body: Record<string, unknown>) =>
      new Promise((resolve) => {
        release = () => {
          dump = { ...dump, ...body }
          resolve({ data: undefined })
        }
      })) as never)
    act(() => result.current.setBool('sla_enabled', true)) // A, sent
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    act(() => result.current.setBool('ticket_scope_enforced', true)) // B, after the send
    await act(async () => release())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    await waitFor(() => expect(result.current.stored?.sla_enabled).toBe(true))
    expect(patchSpy.mock.calls[0][1]).toEqual({ sla_enabled: true })
    expect(result.current.bool('ticket_scope_enforced')).toBe(true)
    expect(result.current.dirty).toBe(true)
    // A was saved, so it follows the server again.
    dump = { ...dump, sla_enabled: false }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.bool('sla_enabled')).toBe(false))
    expect(result.current.bool('ticket_scope_enforced')).toBe(true)
  })

  it('keeps a key put back to its pre-save value while the save is pending, and stays dirty for it', async () => {
    const { result } = setup()
    await loaded(result)
    let release: () => void = () => {}
    patchSpy.mockImplementationOnce(((_url: string, body: Record<string, unknown>) =>
      new Promise((resolve) => {
        release = () => {
          dump = { ...dump, ...body }
          resolve({ data: undefined })
        }
      })) as never)
    act(() => result.current.setBool('sla_enabled', true)) // sent
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    act(() => result.current.setBool('sla_enabled', false)) // the operator's last word: back to what it was
    await act(async () => release())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    await waitFor(() => expect(result.current.stored?.sla_enabled).toBe(true))
    expect(result.current.bool('sla_enabled')).toBe(false)
    expect(result.current.dirty).toBe(true)
  })

  it('does not keep a mark for an array that ends equal to what was sent', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    let release: () => void = () => {}
    patchSpy.mockImplementationOnce(((_url: string, body: Record<string, unknown>) =>
      new Promise((resolve) => {
        release = () => {
          dump = { ...dump, ...body }
          resolve({ data: undefined })
        }
      })) as never)
    act(() => result.current.toggleStrArr('allowed', '.txt', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    // Off and on again while pending: a new array, equal to the one sent.
    act(() => result.current.toggleStrArr('allowed', '.txt', false))
    act(() => result.current.toggleStrArr('allowed', '.txt', true))
    await act(async () => release())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    await waitFor(() => expect(result.current.stored?.allowed).toEqual(['.pdf', '.txt']))
    expect(result.current.dirty).toBe(false)
    // Another administrator removes it; nothing here is held against that.
    dump = { ...dump, allowed: ['.pdf'] }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.strArr('allowed')).toEqual(['.pdf']))
  })

  it('builds array toggles in one tick on each other, and from the stored list when put back', async () => {
    const { result } = setup()
    await loaded(result)
    act(() => {
      result.current.toggleStrArr('allowed', '.txt', true)
      result.current.toggleStrArr('allowed', '.png', true)
    })
    expect(result.current.strArr('allowed')).toEqual(['.pdf', '.txt', '.png'])
    act(() => {
      result.current.toggleStrArr('allowed', '.txt', false)
      result.current.toggleStrArr('allowed', '.png', false)
    })
    expect(result.current.strArr('allowed')).toEqual(['.pdf'])
    expect(result.current.dirty).toBe(false)
    // The first toggle puts the list back to the stored one; the second starts from it.
    act(() => {
      result.current.toggleStrArr('allowed', '.txt', true)
      result.current.toggleStrArr('allowed', '.txt', false)
      result.current.toggleStrArr('allowed', '.png', true)
    })
    expect(result.current.strArr('allowed')).toEqual(['.pdf', '.png'])
  })

  it('does not hold an edit that was put back, so another change to that key is followed', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.setBool('sla_enabled', false))
    expect(result.current.dirty).toBe(false)
    // Someone else turns it on.
    dump = { ...dump, sla_enabled: true }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.bool('sla_enabled')).toBe(true))
    expect(result.current.dirty).toBe(false)
  })

  it('keeps the edited marks when the save is refused', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    patchSpy.mockRejectedValueOnce(refusal('invalid', 'nope'))
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.error).not.toBe(''))
    dump = { ...dump, ticket_scope_enforced: true }
    await act(async () => { await queryClient.invalidateQueries({ queryKey: ['admin', 'settings'] }) })
    await waitFor(() => expect(result.current.stored?.ticket_scope_enforced).toBe(true))
    expect(result.current.bool('sla_enabled')).toBe(true)
  })

  it('renders with an empty draft when the read fails', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('down'))
    const { result } = setup()
    await loaded(result)
    expect(result.current.stored).toBeUndefined()
    expect(result.current.bool('sla_enabled')).toBe(false)
    expect(result.current.dirty).toBe(false)
  })
})

// What the draft does while a save is on its way, and in the moment after the
// PATCH has answered and before the settings have been fetched again. The draft
// is the dump, then the patch that was sent, then what the operator last set.
describe('useSettingsDraft: around a save', () => {
  // The next PATCH waits to be answered: ok() applies it, no() refuses it.
  function holdPatch() {
    const h = { ok: () => {}, no: () => {} }
    patchSpy.mockImplementationOnce(((_url: string, body: Record<string, unknown>) =>
      new Promise((resolve, reject) => {
        h.ok = () => {
          dump = { ...dump, ...body }
          resolve({ data: undefined })
        }
        h.no = () => reject(refusal('invalid', 'nope'))
      })) as never)
    return h
  }
  // The next fetch of the settings waits to be answered.
  function holdFetch() {
    const h = { ok: () => {} }
    vi.spyOn(api, 'get').mockImplementationOnce((() =>
      new Promise((resolve) => {
        h.ok = () => resolve({ data: { ...dump } })
      })) as never)
    return h
  }
  const refetch = (qc: { invalidateQueries: (o: { queryKey: string[] }) => Promise<void> }) =>
    act(async () => { await qc.invalidateQueries({ queryKey: ['admin', 'settings'] }) })

  it.each([
    { name: 'the save goes through', outcome: 'ok' as const },
    { name: 'the save is refused', outcome: 'no' as const },
  ])('an unsent key put back to its stored value while saving is not held: $name', async ({ outcome }) => {
    dump = { ...DUMP, n: 30 }
    const { result, queryClient } = setup()
    await loaded(result)
    const p = holdPatch()
    act(() => result.current.setBool('sla_enabled', true)) // the save
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    // Two keys the save does not carry, touched and put back to what the dump holds.
    act(() => result.current.toggleStrArr('allowed', '.txt', true))
    act(() => result.current.toggleStrArr('allowed', '.txt', false))
    act(() => result.current.setNum('n', 3))
    act(() => result.current.setNum('n', 30))
    await act(async () => p[outcome]())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    // Another administrator changes both. They are not the operator's edits.
    dump = { ...dump, allowed: ['.png'], n: 40 }
    await refetch(queryClient)
    await waitFor(() => expect(result.current.num('n')).toBe(40))
    expect(result.current.strArr('allowed')).toEqual(['.png'])
    expect(result.current.dirty).toBe(outcome === 'no') // the refused save's own edit is still there
  })

  it('a toggle between the PATCH answer and the fresh settings starts from what was just saved', async () => {
    const { result } = setup()
    await loaded(result)
    const p = holdPatch()
    act(() => result.current.toggleStrArr('allowed', '.txt', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    const f = holdFetch()
    await act(async () => p.ok())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(1))
    // The PATCH has answered; the settings have not come back yet.
    expect(result.current.isPending).toBe(true)
    act(() => result.current.toggleStrArr('allowed', '.png', true))
    expect(result.current.strArr('allowed')).toEqual(['.pdf', '.txt', '.png'])
    await act(async () => f.ok())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.strArr('allowed')).toEqual(['.pdf', '.txt', '.png'])
    expect(result.current.dirty).toBe(true)
  })

  it('a key set back to its pre-save value during the save is the last word, and saves next', async () => {
    const { result } = setup()
    await loaded(result)
    const p = holdPatch()
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    act(() => result.current.setBool('sla_enabled', false))
    await act(async () => p.ok())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.bool('sla_enabled')).toBe(false)
    expect(result.current.dirty).toBe(true)
    act(() => result.current.save())
    await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(2))
    expect(patchSpy.mock.calls[1][1]).toEqual({ sla_enabled: false })
  })

  it('an edit made equal to what was sent, during the save, is not an edit', async () => {
    const { result } = setup()
    await loaded(result)
    const p = holdPatch()
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    act(() => result.current.setBool('sla_enabled', false))
    act(() => result.current.setBool('sla_enabled', true))
    await act(async () => p.ok())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.dirty).toBe(false)
  })

  // The save is refused: what the operator sent is still theirs, still unsaved.
  describe('a refused save keeps what was sent', () => {
    async function pendingSave(set: (d: SettingsDraft) => void) {
      dump = { ...DUMP, n: 30 }
      const hook = setup()
      await loaded(hook.result)
      const p = holdPatch()
      act(() => set(hook.result.current))
      act(() => hook.result.current.save())
      await waitFor(() => expect(hook.result.current.isPending).toBe(true))
      return { ...hook, p }
    }
    const refused = async (h: { result: { current: SettingsDraft }; p: { no: () => void } }) => {
      await act(async () => h.p.no())
      await waitFor(() => expect(h.result.current.isPending).toBe(false))
      await waitFor(() => expect(h.result.current.error).not.toBe(''))
    }

    it('after the same value is set again while saving', async () => {
      const h = await pendingSave((d) => d.setStrArr('allowed', ['.pdf', '.txt']))
      act(() => h.result.current.setStrArr('allowed', ['.pdf', '.txt']))
      await refused(h)
      expect(h.result.current.strArr('allowed')).toEqual(['.pdf', '.txt'])
      expect(h.result.current.dirty).toBe(true)
    })

    it('after the value is set off and on again while saving', async () => {
      const h = await pendingSave((d) => d.setBool('sla_enabled', true))
      act(() => h.result.current.setBool('sla_enabled', false))
      act(() => h.result.current.setBool('sla_enabled', true))
      await refused(h)
      expect(h.result.current.bool('sla_enabled')).toBe(true)
      expect(h.result.current.dirty).toBe(true)
    })

    it.each([
      { name: 'a number', set: (d: SettingsDraft) => d.setNum('n', 5), read: (d: SettingsDraft) => d.num('n'), want: 5 },
      { name: 'a switch', set: (d: SettingsDraft) => d.setBool('sla_enabled', true), read: (d: SettingsDraft) => d.bool('sla_enabled'), want: true },
    ])('after the settings are fetched again, with another key changed, while saving: $name', async ({ set, read, want }) => {
      const h = await pendingSave(set)
      dump = { ...dump, ticket_scope_enforced: true }
      await refetch(h.queryClient)
      await waitFor(() => expect(h.result.current.stored?.ticket_scope_enforced).toBe(true))
      await refused(h)
      expect(read(h.result.current)).toBe(want)
      expect(h.result.current.dirty).toBe(true)
    })

    it('and a later Save sends it', async () => {
      const h = await pendingSave((d) => d.setBool('sla_enabled', true))
      await refused(h)
      act(() => h.result.current.save())
      await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(2))
      expect(patchSpy.mock.calls[1][1]).toEqual({ sla_enabled: true })
    })
  })

  // An earlier save went through but its fetch failed, so the page shows what
  // that save wrote on top of an older dump. A later save, refused, must give
  // back only what it sent.
  describe('a refused save after one whose fetch failed', () => {
    async function afterFailedFetch() {
      dump = { ...DUMP, n: 0 }
      const hook = setup()
      await loaded(hook.result)
      const p1 = holdPatch()
      act(() => hook.result.current.setNum('n', 1))
      act(() => hook.result.current.save())
      await waitFor(() => expect(hook.result.current.isPending).toBe(true))
      vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('down'))
      await act(async () => p1.ok())
      await waitFor(() => expect(hook.result.current.isPending).toBe(false))
      expect(hook.result.current.num('n')).toBe(1) // shown, from the earlier save
      const p2 = holdPatch()
      act(() => hook.result.current.setBool('sla_enabled', true))
      act(() => hook.result.current.save())
      await waitFor(() => expect(hook.result.current.isPending).toBe(true))
      await act(async () => p2.no())
      await waitFor(() => expect(hook.result.current.isPending).toBe(false))
      await waitFor(() => expect(hook.result.current.error).not.toBe(''))
      return hook
    }

    it('keeps only what it sent as unsaved; what the earlier save wrote is not an edit', async () => {
      const { result, queryClient } = await afterFailedFetch()
      expect(patchSpy.mock.calls[1][1]).toEqual({ sla_enabled: true })
      expect(result.current.bool('sla_enabled')).toBe(true)
      expect(result.current.num('n')).toBe(1)
      expect(result.current.dirty).toBe(true)
      // Another administrator changes n; the fetch recovers. n follows it.
      dump = { ...dump, n: 2 }
      await refetch(queryClient)
      await waitFor(() => expect(result.current.num('n')).toBe(2))
      expect(result.current.bool('sla_enabled')).toBe(true)
      act(() => result.current.save())
      await waitFor(() => expect(patchSpy).toHaveBeenCalledTimes(3))
      expect(patchSpy.mock.calls[2][1]).toEqual({ sla_enabled: true })
    })

    it('is clean again once the refused key is put back', async () => {
      const { result } = await afterFailedFetch()
      act(() => result.current.setBool('sla_enabled', false))
      expect(result.current.dirty).toBe(false)
    })
  })

  // The dump that lands while a later save is in flight is newer than what the
  // earlier save (whose fetch failed) wrote; it takes over from it at once.
  describe('a dump that lands while a later save is in flight', () => {
    async function setUpEarlierSaveWithFailedFetch() {
      dump = { ...DUMP, b: 1 }
      const hook = setup()
      await loaded(hook.result)
      const p1 = holdPatch()
      act(() => hook.result.current.setNum('b', 2))
      act(() => hook.result.current.save())
      await waitFor(() => expect(hook.result.current.isPending).toBe(true))
      vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('down'))
      await act(async () => p1.ok())
      await waitFor(() => expect(hook.result.current.isPending).toBe(false))
      expect(hook.result.current.num('b')).toBe(2) // from the earlier save, the dump is stale
      dump = { ...dump, b: 0 } // another administrator
      const p2 = holdPatch()
      act(() => hook.result.current.setBool('sla_enabled', true))
      act(() => hook.result.current.save())
      await waitFor(() => expect(hook.result.current.isPending).toBe(true))
      await refetch(hook.queryClient) // a focus refetch lands meanwhile
      await waitFor(() => expect(hook.result.current.stored?.b).toBe(0))
      return { ...hook, p2 }
    }

    it('takes over from the earlier save when the later save is refused', async () => {
      const h = await setUpEarlierSaveWithFailedFetch()
      await act(async () => h.p2.no())
      await waitFor(() => expect(h.result.current.isPending).toBe(false))
      expect(h.result.current.num('b')).toBe(0)
      expect(h.result.current.bool('sla_enabled')).toBe(true)
      expect(h.result.current.dirty).toBe(true)
    })

    it('takes over from the earlier save when the later save goes through but its fetch fails', async () => {
      const h = await setUpEarlierSaveWithFailedFetch()
      vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('down'))
      await act(async () => h.p2.ok())
      await waitFor(() => expect(h.result.current.isPending).toBe(false))
      expect(h.result.current.num('b')).toBe(0)
      expect(h.result.current.bool('sla_enabled')).toBe(true)
      expect(h.result.current.dirty).toBe(false)
    })
  })

  it('keeps a write-only key typed while a save was in flight, if the save did not carry it', async () => {
    const { result } = setup()
    await loaded(result)
    const p = holdPatch()
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    act(() => result.current.setSecret('virustotal_api_key', 'x'))
    act(() => result.current.setSecretCleared('metadefender_api_key', true))
    await act(async () => p.ok())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.secret('virustotal_api_key')).toBe('x')
    expect(result.current.secretCleared('metadefender_api_key')).toBe(true)
    expect(result.current.dirty).toBe(true)
  })

  it('clears the write-only key the save carried, but not what was typed over it afterwards', async () => {
    const { result } = setup()
    await loaded(result)
    const p = holdPatch()
    act(() => result.current.setSecret('virustotal_api_key', 'one'))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    act(() => result.current.setSecret('virustotal_api_key', 'two'))
    await act(async () => p.ok())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(patchSpy.mock.calls[0][1]).toEqual({ virustotal_api_key: 'one' })
    expect(result.current.secret('virustotal_api_key')).toBe('two')
  })

  it('clears a write-only key the save carried', async () => {
    const { result } = setup()
    await loaded(result)
    act(() => result.current.setSecret('virustotal_api_key', 'one'))
    act(() => result.current.setSecretCleared('metadefender_api_key', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.saved).toBe(true))
    expect(result.current.secret('virustotal_api_key')).toBe('')
    expect(result.current.secretCleared('metadefender_api_key')).toBe(false)
  })

  it('ignores a second Save while one is in flight, and loses nothing', async () => {
    const { result } = setup()
    await loaded(result)
    const p = holdPatch()
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.isPending).toBe(true))
    act(() => result.current.save())
    await act(async () => { await new Promise((r) => setTimeout(r, 20)) })
    expect(patchSpy).toHaveBeenCalledTimes(1)
    await act(async () => p.no())
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.bool('sla_enabled')).toBe(true)
    expect(result.current.dirty).toBe(true)
  })

  it('shows the clamped figure that was saved, when the fresh settings could not be fetched', async () => {
    dump = { ...DUMP, attachment_max_mb: 25, guest_attachment_max_mb: 20 }
    const { result } = setup()
    await loaded(result)
    vi.spyOn(api, 'get').mockRejectedValue(new Error('down'))
    act(() => result.current.setNum('guest_attachment_max_mb', 60))
    act(() => result.current.save())
    await waitFor(() => expect(patchSpy).toHaveBeenCalled())
    expect(patchSpy.mock.calls[0][1]).toEqual({ guest_attachment_max_mb: 25 })
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.num('guest_attachment_max_mb')).toBe(25)
    expect(result.current.dirty).toBe(false)
  })

  it('takes the dump as it is when the fetch after a save works, even if its data did not change', async () => {
    const { result } = setup()
    await loaded(result)
    // The server answers 204 and keeps the old value (or the setting is read-only here).
    patchSpy.mockResolvedValueOnce({ data: undefined } as never)
    act(() => result.current.setBool('sla_enabled', true))
    act(() => result.current.save())
    await waitFor(() => expect(result.current.saved).toBe(true))
    await waitFor(() => expect(result.current.isPending).toBe(false))
    expect(result.current.stored?.sla_enabled).toBe(false)
    expect(result.current.bool('sla_enabled')).toBe(false)
    expect(result.current.dirty).toBe(false)
  })

  describe('a save that went through but whose fresh settings could not be fetched', () => {
    it('still shows what was saved, is not dirty, and is taken over by the next fetch', async () => {
      const { result, queryClient } = setup()
      await loaded(result)
      const p = holdPatch()
      act(() => result.current.setBool('sla_enabled', true))
      act(() => result.current.save())
      await waitFor(() => expect(result.current.isPending).toBe(true))
      vi.spyOn(api, 'get').mockRejectedValueOnce(new Error('down'))
      await act(async () => p.ok())
      await waitFor(() => expect(result.current.isPending).toBe(false))
      // The page still has the old dump, and says so nowhere; it shows the saved value.
      expect(result.current.stored?.sla_enabled).toBe(false)
      expect(result.current.bool('sla_enabled')).toBe(true)
      expect(result.current.dirty).toBe(false)
      // An edit now is judged against what was saved: setting it back is an edit.
      act(() => result.current.setBool('sla_enabled', false))
      expect(result.current.dirty).toBe(true)
      act(() => result.current.setBool('sla_enabled', true))
      expect(result.current.dirty).toBe(false)
      // The next fetch that works takes over, and another administrator's change comes through.
      dump = { ...dump, sla_enabled: false }
      await refetch(queryClient) // the dump is what it was before the save: no change in its data
      await waitFor(() => expect(result.current.bool('sla_enabled')).toBe(false))
      expect(result.current.dirty).toBe(false)
    })
  })

  it('never stays dirty over a key that equals the dump', async () => {
    const { result, queryClient } = setup()
    await loaded(result)
    act(() => result.current.setBool('sla_enabled', true))
    // The same change is made by someone else: the edit is now the dump's value.
    dump = { ...dump, sla_enabled: true }
    await refetch(queryClient)
    await waitFor(() => expect(result.current.stored?.sla_enabled).toBe(true))
    expect(result.current.dirty).toBe(false)
    // And when they change it back, it is theirs to change.
    dump = { ...dump, sla_enabled: false }
    await refetch(queryClient)
    await waitFor(() => expect(result.current.stored?.sla_enabled).toBe(false))
    expect(result.current.bool('sla_enabled')).toBe(false)
    expect(result.current.dirty).toBe(false)
  })
})
