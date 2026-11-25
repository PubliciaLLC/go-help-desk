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

    expect((await screen.findByRole('status')).textContent).toBe('Icon saved. Reload the page to see it in this tab.')
    const group = screen.getByRole('group', { name: 'Generated icons' })
    const figures = group.querySelectorAll('figure')
    expect(figures).toHaveLength(7)
    expect([...figures].map((f) => f.querySelector('figcaption')?.textContent)).toEqual([
      'favicon.ico, 16, 32 and 48',
      'Browser tab, 16 × 16',
      'Browser tab, 32 × 32',
      'iPhone and iPad, 180 × 180',
      'Android and desktop, 192 × 192',
      'Android and desktop, 512 × 512',
      'Android, masked, 512 × 512',
    ])
    expect([...figures].map((f) => f.querySelector('img')?.getAttribute('src'))).toEqual(
      FILES.map(([name]) => `${URL_BASE}${name}?v=${V}`),
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
    expect((screen.getByRole('button', { name: 'Upload icon' }) as HTMLButtonElement).disabled).toBe(true)
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
    expect(imgs.map((i) => i.getAttribute('width'))).toEqual(['48', '16', '32', '90', '96', '96', '96'])
    for (const i of imgs) expect(i.getAttribute('height')).toBe(i.getAttribute('width'))
    // The two icons a phone masks are drawn the way a phone draws them.
    const frame = (name: string) => imgs.find((i) => i.getAttribute('src')?.includes(name))!.parentElement!
    expect(frame('icon-maskable-512').className).toContain('rounded-full')
    expect(frame('apple-touch-icon').className).toContain('rounded-[22%]')
  })

  it('Apply colour stays disabled until the colour differs, then sends PATCH {background}', async () => {
    const { patch } = await renderIcon(isSet())
    expect(hex().value).toBe('#ffffff')
    expect(apply()!.disabled).toBe(true)

    fireEvent.change(hex(), { target: { value: '#112233' } })
    expect(apply()!.disabled).toBe(false)
    await userEvent.click(apply()!)

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(1))
    expect(patch).toHaveBeenCalledWith('/admin/settings/icon', { background: '#112233' })
    expect((await screen.findByRole('status')).textContent).toBe('Background colour applied.')
    await waitFor(() => expect(apply()!.disabled).toBe(true))
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

  it('an invalid hex shows the hint in an alert and keeps Apply disabled', async () => {
    await renderIcon(isSet())
    fireEvent.change(hex(), { target: { value: '#12' } })
    expect((await screen.findByRole('alert')).textContent).toBe('Use a colour such as #ffffff.')
    expect(apply()!.disabled).toBe(true)
  })

  it('a capital-letter spelling of the stored colour is not a change', async () => {
    await renderIcon(isSet('#aabbcc'))
    fireEvent.change(hex(), { target: { value: '#AABBCC' } })
    expect(apply()!.disabled).toBe(true)
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
    expect((await screen.findByRole('status')).textContent).toBe('Icon removed.')
    await screen.findByText('No icon uploaded. The built-in icon is used.')
    expect(screen.queryByRole('group', { name: 'Generated icons' })).toBeNull()
  })

  it('Cancel in the confirmation deletes nothing', async () => {
    const { del } = await renderIcon(isSet())
    await userEvent.click(screen.getByRole('button', { name: 'Remove icon' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Cancel' }))
    expect(del).not.toHaveBeenCalled()
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
