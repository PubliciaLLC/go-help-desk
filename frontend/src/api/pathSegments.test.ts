import { beforeEach, describe, expect, it } from 'vitest'
import type { AxiosAdapter, InternalAxiosRequestConfig } from 'axios'
import { api } from './client'
import { listPublicItems, listPublicTypes, removeLink, attachmentDownloadUrl, listReplies } from './tickets'
import { getUser, deleteGroup, removeGroupMember, updateItem, listAssignments } from './admin'
import { deleteMyAPIKey } from './apikeys'
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
