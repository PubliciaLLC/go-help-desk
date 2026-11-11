import { beforeEach, describe, expect, it } from 'vitest'
import type { AxiosAdapter, InternalAxiosRequestConfig } from 'axios'
import { api } from './client'
import { listPublicItems, listPublicTypes, removeLink, attachmentDownloadUrl, listReplies } from './tickets'
import { getUser, deleteGroup, removeGroupMember, updateItem, listAssignments } from './admin'
import { deleteMyAPIKey } from './apikeys'
import { seg } from './seg'
import { removePasskey } from './passkeys'

// Values that come from the address bar or other untrusted text reach these
// helpers. Whatever they contain must stay inside its own path segment, or a
// crafted link sends the page's authenticated request to another route.
const EVIL = '../admin/users'
const ENCODED = encodeURIComponent(EVIL)

let seen: string[]

beforeEach(() => {
  seen = []
  const adapter: AxiosAdapter = async (config: InternalAxiosRequestConfig) => {
    seen.push(api.getUri(config))
    return { data: [], status: 200, statusText: '', headers: {}, config }
  }
  api.defaults.adapter = adapter
})

describe('API helpers keep a variable inside its path segment', () => {
  const cases: [string, () => Promise<unknown> | unknown][] = [
    ['listPublicTypes', () => listPublicTypes(EVIL)],
    ['listPublicItems (category)', () => listPublicItems(EVIL, 't')],
    ['listPublicItems (type)', () => listPublicItems('c', EVIL)],
    ['listReplies', () => listReplies(EVIL)],
    ['removeLink (link type)', () => removeLink('t', 'x', EVIL as never)],
    ['getUser', () => getUser(EVIL)],
    ['deleteGroup', () => deleteGroup(EVIL)],
    ['removeGroupMember', () => removeGroupMember('g', EVIL)],
    ['updateItem', () => updateItem('c', 't', EVIL, {})],
    ['listAssignments', () => listAssignments('category', EVIL)],
    ['deleteMyAPIKey', () => deleteMyAPIKey(EVIL)],
    ['removePasskey', () => removePasskey(EVIL)],
  ]

  it.each(cases)('%s', async (_name, call) => {
    await call()
    expect(seen).toHaveLength(1)
    expect(seen[0]).toContain(ENCODED)
    expect(seen[0]).not.toContain(EVIL)
  })

  it('attachmentDownloadUrl', () => {
    const url = attachmentDownloadUrl(EVIL, EVIL)
    expect(url).toBe(`/api/v1/tickets/${ENCODED}/attachments/${ENCODED}`)
  })
})

describe('seg', () => {
  it('encodes what would otherwise leave its segment', () => {
    expect(seg('a/b?c#d')).toBe('a%2Fb%3Fc%23d')
    expect(seg('11111111-1111-4111-8111-111111111111')).toBe('11111111-1111-4111-8111-111111111111')
  })

  // encodeURIComponent leaves these alone, and a browser resolves them as
  // directory moves, so they are refused rather than encoded.
  it.each(['', '.', '..'])('refuses %j', value => {
    expect(() => seg(value)).toThrow()
  })

  const refused: [string, () => Promise<unknown>][] = [
    ['listPublicTypes', () => listPublicTypes('..')],
    ['getUser', () => getUser('.')],
    ['deleteMyAPIKey', () => deleteMyAPIKey('')],
  ]
  it.each(refused)('a helper refuses a dot segment before any request: %s', async (_name, call) => {
    await expect(call()).rejects.toThrow()
    expect(seen).toHaveLength(0)
  })
})

// Source as text (import.meta.glob is Vite's, so this needs no Node types).
// A new helper that writes a raw variable into a path fails here instead of
// shipping, which is how the unencoded ones got there.
const sources = import.meta.glob('./*.ts', { query: '?raw', import: 'default', eager: true }) as Record<string, string>

describe('every API helper routes path values through seg', () => {
  const files = Object.entries(sources).filter(([name]) => !name.endsWith('.test.ts'))

  it('finds the helper modules', () => {
    expect(files.map(([name]) => name)).toEqual(expect.arrayContaining(['./admin.ts', './tickets.ts', './apikeys.ts', './passkeys.ts']))
  })

  it.each(files)('%s', (_name, text) => {
    const raw: string[] = []
    // A template literal that starts like a path. Only the part before "?" is
    // path; query values have their own encoding.
    for (const m of text.matchAll(/`(\/[^`]*)`/g)) {
      const path = m[1].split('?')[0]
      for (const v of path.matchAll(/\$\{([^}]*)\}/g)) {
        if (!/^seg\(\w+\)$/.test(v[1])) raw.push(v[1])
      }
    }
    expect(raw).toEqual([])
  })
})
