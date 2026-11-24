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

  it('renders with an empty draft when the read fails', async () => {
    vi.spyOn(api, 'get').mockRejectedValue(new Error('down'))
    const { result } = setup()
    await loaded(result)
    expect(result.current.stored).toBeUndefined()
    expect(result.current.bool('sla_enabled')).toBe(false)
    expect(result.current.dirty).toBe(false)
  })
})
