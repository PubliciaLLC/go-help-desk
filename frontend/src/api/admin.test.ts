import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { AxiosError, AxiosHeaders } from 'axios'
import {
  saveSAMLConfig, uploadLogo, addGroupScope, createSLAPolicy, updateSLAPolicy,
  deleteGroup, deleteOAuthClient, deleteAPIKey, deleteCategory, deleteType, deleteItem,
  deleteCategoryAssignment, deleteTypeAssignment, deleteItemAssignment, removeGroupScope, updateSettings,
} from './admin'

// admin.ts is 557 lines and almost all of it is one-line wrappers, which are
// deliberately not tested — see the note in tickets.test.ts. Covered here is
// the handful of places it does something that can be wrong on its own:
// a null guard, a multipart body built by hand, and two payloads where an
// optional field being absent means something different from being empty.

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('updateSettings', () => {
  // PATCH /admin/settings answers 204 with no body, or 200 {"warning"} (#420).
  it('hands back the warning when there is one, and nothing when the body is empty', async () => {
    const patch = vi.spyOn(api, 'patch')
    patch.mockResolvedValueOnce({ data: { warning: 'JIRA sync will see no tickets.' } })
    expect(await updateSettings({ ticket_scope_enforced: true })).toEqual({ warning: 'JIRA sync will see no tickets.' })

    patch.mockResolvedValueOnce({ data: undefined })
    expect((await updateSettings({ site_name: 'x' })).warning).toBeUndefined()
  })
})

describe('saveSAMLConfig', () => {
  // The caller reads `result.warning` to show "saved, but the certificate
  // expires soon". The endpoint answers 204 with no body when there is nothing
  // to warn about, so res.data is undefined and the guard is what stops the
  // admin page throwing on a successful save.
  it('returns an object when the server sends no body', async () => {
    vi.spyOn(api, 'put').mockResolvedValue({ data: undefined })

    const res = await saveSAMLConfig({ metadata_url: 'https://idp/meta', cert_pem: 'c', key_pem: 'k' })
    expect(res).toEqual({})
    expect(res.warning).toBeUndefined()
  })

  it('passes a warning through when there is one', async () => {
    vi.spyOn(api, 'put').mockResolvedValue({ data: { warning: 'certificate expires in 5 days' } })

    const res = await saveSAMLConfig({ metadata_url: 'https://idp/meta', cert_pem: 'c', key_pem: 'k' })
    expect(res.warning).toBe('certificate expires in 5 days')
  })
})

describe('uploadLogo', () => {
  // The field name and the content type are both load-bearing and neither is
  // type-checked. A wrong field name is rejected by the server as "no file
  // uploaded" on an upload that plainly contained a file.
  it('sends the file as multipart under the field name the server reads', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: { url: '/uploads/logo.png' } })
    const file = new File(['bytes'], 'logo.png', { type: 'image/png' })

    await uploadLogo(file)

    const [path, body, config] = post.mock.calls[0]
    expect(path).toBe('/admin/settings/logo')
    expect(body).toBeInstanceOf(FormData)
    expect((body as FormData).get('logo')).toBe(file)
    expect(config).toMatchObject({ headers: { 'Content-Type': 'multipart/form-data' } })
  })

  it('returns the URL the server assigned rather than a local one', async () => {
    vi.spyOn(api, 'post').mockResolvedValue({ data: { url: '/uploads/abc123.png' } })
    await expect(uploadLogo(new File([''], 'l.png'))).resolves.toEqual({ url: '/uploads/abc123.png' })
  })
})

describe('payloads where absent and empty differ', () => {
  // A scope with no type_id is category-level and covers every type beneath it
  // (see group_scopes in DESIGN.md). Sending type_id: "" instead of omitting it
  // would be a different, narrower scope — or a foreign-key error.
  it('omits type_id for a category-level scope rather than sending an empty string', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: undefined })
    await addGroupScope('g1', { category_id: 'c1' })

    const [, body] = post.mock.calls[0]
    expect(body).toEqual({ category_id: 'c1' })
    expect('type_id' in (body as object)).toBe(false)
  })

  // clear_category is how the UI removes a policy's category, because omitting
  // category_id means "leave it alone". The two must not collapse.
  it('keeps clear_category distinct from omitting category_id', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })
    await updateSLAPolicy('p1', { clear_category: true })

    const [, body] = patch.mock.calls[0]
    expect(body).toEqual({ clear_category: true })
  })

  // A policy with no priority is the catch-all tier, and clear_priority is the
  // only way to widen an existing policy back to it — priority: null and an
  // absent priority are the same nil pointer on the server.
  it('keeps clear_priority distinct from omitting priority', async () => {
    const patch = vi.spyOn(api, 'patch').mockResolvedValue({ data: {} })
    await updateSLAPolicy('p1', { name: 'Catch-all', clear_priority: true })

    const [, body] = patch.mock.calls[0]
    expect(body).toEqual({ name: 'Catch-all', clear_priority: true })
    expect('priority' in (body as object)).toBe(false)
  })

  // Sending priority: "" instead of omitting it is a 400: the server validates
  // a supplied priority, and "" is not one of the four.
  it('omits priority entirely for a catch-all policy', async () => {
    const post = vi.spyOn(api, 'post').mockResolvedValue({ data: {} })
    await createSLAPolicy({ name: 'Catch-all', response_target_min: 60, resolution_target_min: 480 })

    const [, body] = post.mock.calls[0]
    expect('priority' in (body as object)).toBe(false)
  })
})

// Deleting something that is already gone answers 404 not_found (#418). That
// is the outcome the caller asked for, so these resolve; every other failure,
// including the router's plain-text 404 for a route that does not exist, still
// rejects so the page can say what went wrong.
describe('deletes treat a not_found 404 as already gone', () => {
  function failure(status: number, data: unknown) {
    const headers = new AxiosHeaders()
    return new AxiosError('Request failed', 'ERR_BAD_REQUEST', { headers } as never, null, {
      status, statusText: 'x', headers, config: { headers } as never, data,
    } as never)
  }
  const envelope = (code: string) => ({ error: { code, message: 'm' } })

  const calls: Record<string, { url: string; call: () => Promise<void> }> = {
    deleteGroup: { url: '/admin/groups/g', call: () => deleteGroup('g') },
    deleteOAuthClient: { url: '/admin/oauth-clients/o', call: () => deleteOAuthClient('o') },
    deleteAPIKey: { url: '/admin/api-keys/k', call: () => deleteAPIKey('k') },
    deleteCategory: { url: '/admin/categories/c', call: () => deleteCategory('c') },
    deleteType: { url: '/admin/categories/c/types/t', call: () => deleteType('c', 't') },
    deleteItem: { url: '/admin/categories/c/types/t/items/i', call: () => deleteItem('c', 't', 'i') },
    deleteCategoryAssignment: { url: '/admin/categories/c/fields/a', call: () => deleteCategoryAssignment('c', 'a') },
    deleteTypeAssignment: { url: '/admin/categories/c/types/t/fields/a', call: () => deleteTypeAssignment('c', 't', 'a') },
    deleteItemAssignment: { url: '/admin/categories/c/types/t/items/i/fields/a', call: () => deleteItemAssignment('c', 't', 'i', 'a') },
    removeGroupScope: { url: '/admin/groups/g/scopes', call: () => removeGroupScope('g', { category_id: 'c' }) },
  }

  for (const [name, { url, call }] of Object.entries(calls)) {
    it(`${name}: resolves on 404 not_found, rejects on anything else`, async () => {
      const del = vi.spyOn(api, 'delete')

      del.mockRejectedValueOnce(failure(404, envelope('not_found')))
      await expect(call()).resolves.toBeUndefined()
      expect(del.mock.calls[0][0]).toBe(url)

      // The router's answer for a path that is not a route: plain text, no envelope.
      del.mockRejectedValueOnce(failure(404, '404 page not found\n'))
      await expect(call()).rejects.toMatchObject({ response: { status: 404 } })

      // A 404 that is not the not_found code is not "already gone" either.
      del.mockRejectedValueOnce(failure(404, envelope('something_else')))
      await expect(call()).rejects.toMatchObject({ response: { status: 404 } })

      del.mockRejectedValueOnce(failure(409, envelope('category_in_use')))
      await expect(call()).rejects.toMatchObject({ response: { status: 409 } })

      del.mockRejectedValueOnce(failure(500, envelope('internal_error')))
      await expect(call()).rejects.toMatchObject({ response: { status: 500 } })
    })
  }
})
