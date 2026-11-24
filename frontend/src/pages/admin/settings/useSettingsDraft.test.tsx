import type { ReactNode } from 'react'
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AxiosError } from 'axios'
import { api } from '@/api/client'
import { useSettingsDraft, type SettingsDraft } from './useSettingsDraft'

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
