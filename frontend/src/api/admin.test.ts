import { describe, expect, it, vi, beforeEach } from 'vitest'
import { api } from './client'
import { saveSAMLConfig, uploadLogo, addGroupScope, updateSLAPolicy } from './admin'

// admin.ts is 557 lines and almost all of it is one-line wrappers, which are
// deliberately not tested — see the note in tickets.test.ts. Covered here is
// the handful of places it does something that can be wrong on its own:
// a null guard, a multipart body built by hand, and two payloads where an
// optional field being absent means something different from being empty.

beforeEach(() => {
  vi.restoreAllMocks()
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
})
