import { describe, expect, it, vi, afterEach } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, type AxiosResponse } from 'axios'
import { renderWithQuery } from '@/test/render'
import { api } from '@/api/client'
import type { SiteIconStatus } from '@/api/admin'
import { IconSection } from './settings/sections/IconSection'

// The site icon section of System -> Branding (#708): one upload, seven
// generated files the server serves, a background colour for the home screen
// icons. The API is mocked with the JSON shapes of the build contract.

afterEach(() => vi.restoreAllMocks())

const URL_BASE = '/api/v1/branding/icons/'
const V = '9f1c2a7b3d4e5f60'
const FILES: Array<[string, number[], number]> = [
  ['favicon.ico', [16, 32, 48], 3120],
  ['favicon-16.png', [16], 412],
  ['favicon-32.png', [32], 980],
  ['apple-touch-icon.png', [180], 21034],
  ['icon-192.png', [192], 23001],
  ['icon-512.png', [512], 98012],
  ['icon-maskable-512.png', [512], 80110],
]

// The order the previews are shown in: small to large, as the contract's table.
const SHOWN_ORDER = [
  'favicon-16.png',
  'favicon-32.png',
  'favicon.ico',
  'apple-touch-icon.png',
  'icon-192.png',
  'icon-512.png',
  'icon-maskable-512.png',
]

const NOT_SET: SiteIconStatus = {
  set: false,
  background: '#ffffff',
  version: '',
  source_size: 0,
  updated_at: null,
  files: [],
}

function isSet(background = '#ffffff'): SiteIconStatus {
  return {
    set: true,
    background,
    version: V,
    source_size: 1024,
    updated_at: '2026-11-25T10:00:00Z',
    files: FILES.map(([name, sizes, bytes]) => ({ name, url: `${URL_BASE}${name}?v=${V}`, sizes, bytes })),
  }
}

function refusal(status: number, code: string, message: string): AxiosError {
  const err = new AxiosError(`Request failed with status code ${status}`, String(status))
  err.response = { status, statusText: '', headers: {}, config: {}, data: { error: { code, message } } } as unknown as AxiosResponse
  return err
}

/** A stateful mock of the four admin icon routes. */
function mockIcon(initial: SiteIconStatus) {
  let status = initial
  const get = vi.spyOn(api, 'get').mockImplementation(((url: string) => {
    if (url === '/admin/settings/icon') return Promise.resolve({ data: status })
    return Promise.resolve({ data: [] })
  }) as never)
  const post = vi.spyOn(api, 'post').mockImplementation(((_url: string, form: FormData) => {
    status = isSet(String(form.get('background')))
    return Promise.resolve({ data: status })
  }) as never)
  const patch = vi.spyOn(api, 'patch').mockImplementation(((_url: string, body: { background: string }) => {
    status = isSet(body.background)
    return Promise.resolve({ data: status })
  }) as never)
  const del = vi.spyOn(api, 'delete').mockImplementation((() => {
    status = NOT_SET
    return Promise.resolve({ data: undefined })
  }) as never)
  return { get, post, patch, del }
}

async function renderIcon(initial: SiteIconStatus) {
  const m = mockIcon(initial)
  renderWithQuery(<IconSection />)
  await screen.findByRole('heading', { level: 2, name: 'Icon' })
  await screen.findByLabelText('Home screen background colour')
  return m
}

const png = () => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'logo.png', { type: 'image/png' })
const picker = () => document.querySelector('input[type="file"]') as HTMLInputElement
const hex = () => screen.getByLabelText('Home screen background colour, hex') as HTMLInputElement
const apply = () => screen.queryByRole('button', { name: /apply colour|applying/i }) as HTMLButtonElement | null
// A button that must not act but keeps its place in the tab order, so focus is
// never dropped from it: aria-disabled, not the disabled attribute.
const off = (b: HTMLElement | null) => b!.getAttribute('aria-disabled') === 'true'
// hidden: the page is aria-hidden while the confirmation dialog is open.
const statusEl = () => screen.getByRole('status', { hidden: true })
const statusText = () => statusEl().textContent
/** A promise the test settles by hand, to look at the page while a request is in flight. */
function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

describe('with no icon uploaded', () => {
  it('says so, offers Upload icon, and shows no previews and no Apply colour', async () => {
    await renderIcon(NOT_SET)
    expect(screen.getByText('No icon uploaded. The built-in icon is used.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Upload icon' })).toBeTruthy()
    expect(screen.queryByRole('group', { name: 'Generated icons' })).toBeNull()
    expect(document.querySelectorAll('figure')).toHaveLength(0)
    expect(apply()).toBeNull()
    expect(screen.queryByRole('button', { name: 'Remove icon' })).toBeNull()
    expect(screen.getByText('Used when you upload an icon.')).toBeTruthy()
  })

  it('states the requirements and why SVG is missing', async () => {
    await renderIcon(NOT_SET)
    const text = document.body.textContent ?? ''
    expect(text).toContain('Upload one square PNG, JPG or GIF, at least 512 × 512 px and at most 5 MB')
    expect(text).toContain('SVG is not accepted, for the same reason as the logo.')
  })

  it('does not offer SVG in the file picker', async () => {
    await renderIcon(NOT_SET)
    expect(picker().getAttribute('accept')).toBe('.png,.jpg,.jpeg,.gif')
  })

  it('uploads the chosen file with the background colour, then shows the seven previews', async () => {
    const { post } = await renderIcon(NOT_SET)
    fireEvent.change(hex(), { target: { value: '#112233' } })
    const file = png()
    await userEvent.upload(picker(), file)

    await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
    const [url, form] = post.mock.calls[0] as unknown as [string, FormData]
    expect(url).toBe('/admin/settings/icon')
    expect(form).toBeInstanceOf(FormData)
    expect(form.get('icon')).toBe(file)
    expect(form.get('background')).toBe('#112233')

    await waitFor(() => expect(statusText()).toBe('Icon saved. Reload the page to see it in this tab.'))
    const group = screen.getByRole('group', { name: 'Generated icons' })
    const figures = group.querySelectorAll('figure')
    expect(figures).toHaveLength(7)
    expect([...figures].map((f) => f.querySelector('figcaption')?.textContent)).toEqual([
      'Browser tab, 16 × 16',
      'Browser tab, 32 × 32',
      'favicon.ico, 16, 32 and 48',
      'iPhone and iPad, 180 × 180',
      'Android and desktop, 192 × 192',
      'Android and desktop, 512 × 512',
      'Android, masked, 512 × 512',
    ])
    // Small to large, though the server lists favicon.ico first.
    expect([...figures].map((f) => f.querySelector('img')?.getAttribute('src'))).toEqual(
      SHOWN_ORDER.map((name) => `${URL_BASE}${name}?v=${V}`),
    )
    // The caption names the image, so the image itself is decorative.
    for (const img of group.querySelectorAll('img')) expect(img.getAttribute('alt')).toBe('')
    expect(screen.getByRole('button', { name: 'Replace icon' })).toBeTruthy()
  })

  it('shows the server message for a refused upload in an alert', async () => {
    const { post } = await renderIcon(NOT_SET)
    const message =
      'The image is 800 × 600 pixels. The icon must be square: crop it to the same width and height and upload it again.'
    post.mockRejectedValueOnce(refusal(400, 'icon_not_square', message))
    await userEvent.upload(picker(), png())
    expect((await screen.findByRole('alert')).textContent).toBe(message)
    expect(screen.queryByRole('group', { name: 'Generated icons' })).toBeNull()
  })

  it('does not upload while the colour is not a hex colour', async () => {
    const { post } = await renderIcon(NOT_SET)
    fireEvent.change(hex(), { target: { value: '#12' } })
    expect(off(screen.getByRole('button', { name: 'Upload icon' }))).toBe(true)
    const click = vi.spyOn(picker(), 'click')
    await userEvent.click(screen.getByRole('button', { name: 'Upload icon' }))
    expect(click).not.toHaveBeenCalled()
    expect(post).not.toHaveBeenCalled()
  })
})

describe('with an icon uploaded', () => {
  it('offers Replace icon and Remove icon, and previews each generated file at its size', async () => {
    await renderIcon(isSet())
    expect(screen.getByRole('button', { name: 'Replace icon' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Remove icon' })).toBeTruthy()
    expect(screen.queryByText('No icon uploaded. The built-in icon is used.')).toBeNull()
    const imgs = [...screen.getByRole('group', { name: 'Generated icons' }).querySelectorAll('img')]
    expect(imgs.map((i) => i.getAttribute('width'))).toEqual(['16', '32', '48', '90', '64', '96', '96'])
    for (const i of imgs) expect(i.getAttribute('height')).toBe(i.getAttribute('width'))
    // The two icons a phone masks are drawn the way a phone draws them.
    const frame = (name: string) => imgs.find((i) => i.getAttribute('src')?.includes(name))!.parentElement!
    expect(frame('icon-maskable-512').className).toContain('rounded-full')
    expect(frame('apple-touch-icon').className).toContain('rounded-[22%]')
  })

  it('Apply colour stays disabled until the colour differs, then sends PATCH {background}', async () => {
    const { patch } = await renderIcon(isSet())
    expect(hex().value).toBe('#ffffff')
    expect(off(apply())).toBe(true)

    fireEvent.change(hex(), { target: { value: '#112233' } })
    expect(off(apply())).toBe(false)
    await userEvent.click(apply()!)

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings/icon', { background: '#112233' })
    await waitFor(() => expect(statusText()).toBe('Background colour applied.'))
    await waitFor(() => expect(off(apply())).toBe(true))
    expect(hex().value).toBe('#112233')
  })

  it('the colour picker and the hex field are one value', async () => {
    await renderIcon(isSet())
    const colour = screen.getByLabelText('Home screen background colour') as HTMLInputElement
    expect(colour.type).toBe('color')
    fireEvent.change(colour, { target: { value: '#abcdef' } })
    expect(hex().value).toBe('#abcdef')
    fireEvent.change(hex(), { target: { value: '#001122' } })
    expect(colour.value).toBe('#001122')
  })

  it('an invalid hex keeps Apply disabled, and the hint in an alert waits until the field is left', async () => {
    await renderIcon(isSet())
    fireEvent.change(hex(), { target: { value: '#12' } })
    // Still typing: nothing is announced on each keystroke.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(off(apply())).toBe(true)
    fireEvent.blur(hex())
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe('Use a colour such as #ffffff.')
    expect(off(apply())).toBe(true)
    // The field points at its hint.
    expect(hex().getAttribute('aria-describedby')).toBe(alert.id)
    // Correcting it clears the alert.
    fireEvent.change(hex(), { target: { value: '#123456' } })
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('a full-length colour that is not hex is reported without leaving the field', async () => {
    await renderIcon(isSet())
    fireEvent.change(hex(), { target: { value: '#12345g' } })
    expect((await screen.findByRole('alert')).textContent).toBe('Use a colour such as #ffffff.')
  })

  it('a capital-letter spelling of the stored colour is not a change', async () => {
    await renderIcon(isSet('#aabbcc'))
    fireEvent.change(hex(), { target: { value: '#AABBCC' } })
    expect(off(apply())).toBe(true)
  })

  it('Remove icon asks first, then deletes and says so', async () => {
    const { del } = await renderIcon(isSet())
    await userEvent.click(screen.getByRole('button', { name: 'Remove icon' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Remove the icon?')).toBeTruthy()
    expect(dialog.textContent).toContain(
      "The browser tab and new home screen icons go back to the built-in icon. Icons already on someone's home screen may keep the old picture.",
    )
    expect(del).not.toHaveBeenCalled()

    await userEvent.click(within(dialog).getByRole('button', { name: 'Remove icon' }))
    await waitFor(() => expect(del).toHaveBeenCalledWith('/admin/settings/icon'))
    await waitFor(() => expect(statusText()).toBe('Icon removed.'))
    await screen.findByText('No icon uploaded. The built-in icon is used.')
    expect(screen.queryByRole('group', { name: 'Generated icons' })).toBeNull()
  })

  it('after a removal focus lands on Upload icon, not on the page body', async () => {
    await renderIcon(isSet())
    await userEvent.click(screen.getByRole('button', { name: 'Remove icon' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Remove icon' }))
    await waitFor(() => expect(statusText()).toBe('Icon removed.'))
    const upload = await screen.findByRole('button', { name: 'Upload icon' })
    await waitFor(() => expect(document.activeElement).toBe(upload))
  })

  it('a refused removal keeps the dialog open with the reason, and the icon stays', async () => {
    const { del } = await renderIcon(isSet())
    del.mockRejectedValueOnce(refusal(500, 'storage_error', 'saving the icon'))
    await userEvent.click(screen.getByRole('button', { name: 'Remove icon' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Remove icon' }))
    expect((await within(dialog).findByRole('alert')).textContent).toBe('saving the icon')
    expect(screen.getByRole('dialog')).toBeTruthy()
    expect(statusText()).toBe('')
    expect(screen.getByRole('group', { name: 'Generated icons', hidden: true })).toBeTruthy()
    // Cancel puts focus back where it was: the page's own Remove icon button.
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Remove icon' })))
  })

  it('a failed colour change shows the server message and keeps what was typed', async () => {
    const { patch } = await renderIcon(isSet())
    patch.mockRejectedValueOnce(
      refusal(400, 'invalid_background', 'The background colour must be a hex colour such as #ffffff.'),
    )
    fireEvent.change(hex(), { target: { value: '#112233' } })
    await userEvent.click(apply()!)
    expect((await screen.findByRole('alert')).textContent).toBe(
      'The background colour must be a hex colour such as #ffffff.',
    )
    expect(statusText()).toBe('')
    expect(hex().value).toBe('#112233')
    expect(off(apply())).toBe(false)
  })

  it('when another administrator removed the icon, the colour change says so and the page catches up', async () => {
    const { patch, get } = await renderIcon(isSet())
    // The server no longer has the icon: the next status read says so.
    patch.mockImplementationOnce((() => {
      get.mockImplementation((() => Promise.resolve({ data: NOT_SET })) as never)
      return Promise.reject(refusal(404, 'no_icon', 'No icon is uploaded.'))
    }) as never)
    fireEvent.change(hex(), { target: { value: '#112233' } })
    await userEvent.click(apply()!)
    expect((await screen.findByRole('alert')).textContent).toBe('No icon is uploaded.')
    // The previews and Remove are gone because the status was read again.
    await screen.findByText('No icon uploaded. The built-in icon is used.')
    expect(screen.queryByRole('group', { name: 'Generated icons' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Remove icon' })).toBeNull()
  })

  it('Cancel in the confirmation deletes nothing', async () => {
    const { del } = await renderIcon(isSet())
    await userEvent.click(screen.getByRole('button', { name: 'Remove icon' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    expect(del).not.toHaveBeenCalled()
  })
})

describe('while a request is in flight', () => {
  it('upload: the button says Uploading… and keeps focus but does nothing', async () => {
    const { post } = await renderIcon(NOT_SET)
    const d = deferred<{ data: SiteIconStatus }>()
    post.mockReturnValueOnce(d.promise as never)
    const button = screen.getByRole('button', { name: 'Upload icon' })
    button.focus()
    // Not userEvent.upload, which clicks the file input and moves focus to it.
    fireEvent.change(picker(), { target: { files: [png()] } })

    const busy = await screen.findByRole('button', { name: 'Uploading…' })
    expect(busy).toBe(button)
    expect(off(busy)).toBe(true)
    expect((busy as HTMLButtonElement).disabled).toBe(false)
    expect(document.activeElement).toBe(busy)
    const click = vi.spyOn(picker(), 'click')
    await userEvent.click(busy)
    expect(click).not.toHaveBeenCalled()

    // The server now has the icon, as the next read will show.
    vi.mocked(api.get).mockImplementation((() => Promise.resolve({ data: isSet() })) as never)
    d.resolve({ data: isSet() })
    await screen.findByRole('button', { name: 'Replace icon' })
  })

  it('colour: Applying… keeps focus on the button, and it is inert afterwards because nothing differs', async () => {
    const { patch } = await renderIcon(isSet())
    const d = deferred<{ data: SiteIconStatus }>()
    patch.mockReturnValueOnce(d.promise as never)
    fireEvent.change(hex(), { target: { value: '#112233' } })
    await userEvent.click(apply()!)

    const busy = await screen.findByRole('button', { name: 'Applying…' })
    expect(off(busy)).toBe(true)
    expect(document.activeElement).toBe(busy)
    await userEvent.click(busy)
    expect(patch).toHaveBeenCalledTimes(1)
    // The colour fields are locked while it runs.
    expect(hex().disabled).toBe(true)

    d.resolve({ data: isSet('#112233') })
    await waitFor(() => expect(statusText()).toBe('Background colour applied.'))
    const done = screen.getByRole('button', { name: 'Apply colour' })
    expect(off(done)).toBe(true)
    // Focus is still on it: it did not vanish or become unfocusable.
    expect(document.activeElement).toBe(done)
  })

  it('removal: the dialog says Working…, the page button says Removing… and neither acts twice', async () => {
    const { del } = await renderIcon(isSet())
    const d = deferred<{ data: undefined }>()
    del.mockReturnValueOnce(d.promise as never)
    await userEvent.click(screen.getByRole('button', { name: 'Remove icon' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Remove icon' }))
    const working = await within(dialog).findByRole('button', { name: 'Working…' })
    await userEvent.click(working)
    expect(del).toHaveBeenCalledTimes(1)
    expect(off(screen.getByRole('button', { name: 'Removing…', hidden: true }))).toBe(true)
    d.resolve({ data: undefined })
    await waitFor(() => expect(statusText()).toBe('Icon removed.'))
  })
})

describe('the status line', () => {
  it('is in the page before it has anything to say, so a screen reader is watching when its text changes', async () => {
    await renderIcon(NOT_SET)
    const before = screen.getByRole('status')
    expect(before.textContent).toBe('')
    await userEvent.upload(picker(), png())
    await waitFor(() => expect(statusText()).toBe('Icon saved. Reload the page to see it in this tab.'))
    // The same element: its text changed, it was not mounted with the text.
    expect(screen.getByRole('status')).toBe(before)
  })

  it('is cleared when the next action starts', async () => {
    const { post } = await renderIcon(NOT_SET)
    await userEvent.upload(picker(), png())
    await waitFor(() => expect(statusText()).not.toBe(''))
    post.mockRejectedValueOnce(refusal(400, 'invalid_type', 'unsupported file type: must be PNG, JPG, or GIF'))
    await userEvent.upload(picker(), png())
    await screen.findByRole('alert')
    expect(statusText()).toBe('')
  })
})

describe('when the icon status cannot be read', () => {
  async function renderBroken() {
    const get = vi.spyOn(api, 'get').mockRejectedValue(refusal(500, 'internal_error', 'could not read the icon'))
    const post = vi.spyOn(api, 'post')
    const patch = vi.spyOn(api, 'patch')
    renderWithQuery(<IconSection />)
    await screen.findByRole('heading', { level: 2, name: 'Icon' })
    await screen.findByRole('alert')
    return { get, post, patch }
  }

  it('shows the error and Retry, and does not claim there is no icon or offer to change it', async () => {
    await renderBroken()
    expect(screen.getByRole('alert').textContent).toBe('could not read the icon')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy()
    expect(screen.queryByText(/No icon uploaded/)).toBeNull()
    expect(screen.queryByRole('button', { name: /upload icon|replace icon|remove icon|apply colour/i })).toBeNull()
    expect(screen.queryByLabelText('Home screen background colour')).toBeNull()
    expect(screen.queryByLabelText('Home screen background colour, hex')).toBeNull()
    expect(document.querySelector('input[type="file"]')).toBeNull()
    expect(screen.queryByRole('group', { name: 'Generated icons' })).toBeNull()
  })

  it('Retry reads the status again and shows the section when it works', async () => {
    const { get } = await renderBroken()
    get.mockImplementation((() => Promise.resolve({ data: isSet('#abcdef') })) as never)
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await screen.findByRole('button', { name: 'Replace icon' })
    expect(hex().value).toBe('#abcdef')
    expect(screen.queryByRole('alert')).toBeNull()
  })
})

describe('the words on the page', () => {
  it('tells the administrator what the colour also does, and how installed icons update', async () => {
    await renderIcon(isSet())
    const text = document.body.textContent ?? ''
    expect(text).toContain(
      "This colour is also the browser's theme colour: mobile browser toolbars and the installed app's title bar take it, not only the icon fill.",
    )
    expect(text).toContain(
      'Icons already added to a home screen keep the old picture on iPhone and update later on Android.',
    )
  })

  it('says the same when no icon is uploaded yet', async () => {
    await renderIcon(NOT_SET)
    const text = document.body.textContent ?? ''
    expect(text).toContain("also the browser's theme colour")
    expect(text).toContain('keep the old picture on iPhone and update later on Android')
  })
})

describe('the previews', () => {
  const imgs = () => [...screen.getByRole('group', { name: 'Generated icons' }).querySelectorAll('img')]
  const frame = (name: string) => imgs().find((i) => i.getAttribute('src')?.includes(name))!.parentElement!

  it('the favicons sit on a checkerboard so transparency shows; the home screen icons do not', async () => {
    await renderIcon(isSet())
    for (const name of ['favicon.ico', 'favicon-16.png', 'favicon-32.png']) {
      expect(frame(name).className, name).toContain('conic-gradient')
    }
    for (const name of ['apple-touch-icon', 'icon-192', 'icon-512', 'icon-maskable-512']) {
      expect(frame(name).className, name).not.toContain('conic-gradient')
    }
  })

  it('every preview has a hairline border, so a white icon has an edge on the white card', async () => {
    await renderIcon(isSet())
    expect(imgs()).toHaveLength(7)
    for (const i of imgs()) {
      const cls = i.parentElement!.className
      expect(cls, i.getAttribute('src')!).toMatch(/\bborder\b/)
      expect(cls, i.getAttribute('src')!).toMatch(/\bborder-gray-/)
    }
  })

  it('icon-192 and icon-512 are shown at different sizes', async () => {
    await renderIcon(isSet())
    const w = (name: string) => imgs().find((i) => i.getAttribute('src')?.includes(name))!.getAttribute('width')
    expect(w('icon-192')).not.toBe(w('icon-512'))
  })

  it('a caption under the group explains the checkerboard and "masked"', async () => {
    await renderIcon(isSet())
    const group = screen.getByRole('group', { name: 'Generated icons' })
    const note = screen.getByText(/checkerboard/i)
    expect(note.textContent).toContain('transparent')
    expect(note.textContent).toMatch(/masked/i)
    expect(note.textContent).toContain('Android')
    // Under the group, and the group points at it.
    expect(group.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(group.contains(note)).toBe(false)
    expect(group.getAttribute('aria-describedby')).toBe(note.id)
  })

  it('every thumbnail sits at the bottom of an area of one height, so the captions line up', async () => {
    await renderIcon(isSet())
    const areas = imgs().map((i) => i.parentElement!.parentElement!)
    expect(areas).toHaveLength(7)
    const heights = new Set(areas.map((a) => a.style.height))
    expect(heights.size).toBe(1)
    const h = parseInt([...heights][0], 10)
    // Tall enough for the largest thumbnail and its border.
    const largest = Math.max(...imgs().map((i) => Number(i.getAttribute('width'))))
    expect(h).toBeGreaterThanOrEqual(largest + 2)
    for (const a of areas) expect(a.className).toContain('items-end')
  })

  it('the background colour comes before the previews, and the buttons after them', async () => {
    await renderIcon(isSet())
    const before = (a: Node, b: Node) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
    const intro = screen.getByText(/Upload one square PNG/)
    const colour = screen.getByLabelText('Home screen background colour')
    const apply_ = screen.getByRole('button', { name: 'Apply colour' })
    const group = screen.getByRole('group', { name: 'Generated icons' })
    const replace = screen.getByRole('button', { name: 'Replace icon' })
    const remove = screen.getByRole('button', { name: 'Remove icon' })
    expect(before(intro, colour)).toBe(true)
    expect(before(apply_, group)).toBe(true)
    expect(before(group, replace)).toBe(true)
    expect(before(group, remove)).toBe(true)
  })

  it('shows no caption and no previews without an icon', async () => {
    await renderIcon(NOT_SET)
    expect(screen.queryByText(/checkerboard/i)).toBeNull()
  })
})

describe('accessible names', () => {
  it('the colour inputs and the previews group are named', async () => {
    await renderIcon(isSet())
    expect(screen.getByLabelText('Home screen background colour')).toBeTruthy()
    expect(screen.getByLabelText('Home screen background colour, hex')).toBeTruthy()
    expect(screen.getByRole('group', { name: 'Generated icons' })).toBeTruthy()
  })

  it('the icon buttons never share a name with the logo buttons', async () => {
    await renderIcon(isSet())
    for (const b of screen.getAllByRole('button')) expect(b.textContent).not.toMatch(/logo/i)
  })
})
