import { describe, expect, it, vi, beforeEach } from 'vitest'
import { useState } from 'react'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import * as ticketsApi from '@/api/tickets'
import { RequesterPicker } from './RequesterPicker'
import { ME, type Requester } from './requester'

// The page-level behaviour (what a submit sends) is in
// pages/new-ticket-requester.test.tsx. This is the control on its own.

const ADA = { id: 'u-ada', display_name: 'Ada Lovelace', email: 'ada@example.com' }

function Harness({ onChange }: { onChange?: (r: Requester) => void }) {
  const [value, setValue] = useState<Requester>(ME)
  return (
    <RequesterPicker
      value={value}
      onChange={(r) => {
        setValue(r)
        onChange?.(r)
      }}
    />
  )
}

let lookup: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  vi.restoreAllMocks()
  lookup = vi.spyOn(ticketsApi, 'lookupRequesters').mockResolvedValue([ADA])
})

describe('RequesterPicker', () => {
  it('offers Me, Another user and Email address as one group, Me chosen', () => {
    renderWithQuery(<Harness />)

    const group = screen.getByRole('group', { name: 'Requester' })
    expect(group).toBeTruthy()
    expect((screen.getByRole('radio', { name: 'Me' }) as HTMLInputElement).checked).toBe(true)
    expect(screen.getByRole('radio', { name: 'Another user' })).toBeTruthy()
    expect(screen.getByRole('radio', { name: 'Email address' })).toBeTruthy()
    expect(screen.queryByLabelText('Search users')).toBeNull()
    expect(screen.queryByLabelText('Requester email')).toBeNull()
  })

  it('says so when nobody matches, and offers nothing to pick', async () => {
    lookup.mockResolvedValue([])
    const user = userEvent.setup()
    renderWithQuery(<Harness />)

    await user.click(screen.getByRole('radio', { name: 'Another user' }))
    await user.type(screen.getByLabelText('Search users'), 'zzz')

    await waitFor(() => expect(screen.getByRole('status').textContent).toBe('No users match.'))
    expect(screen.queryByRole('list', { name: 'Matching users' })).toBeNull()
  })

  it('lists matches with their address, and Change goes back to searching', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    renderWithQuery(<Harness onChange={onChange} />)

    await user.click(screen.getByRole('radio', { name: 'Another user' }))
    await user.type(screen.getByLabelText('Search users'), 'ada')
    const list = await screen.findByRole('list', { name: 'Matching users' })
    expect(list.textContent).toContain('ada@example.com')

    await user.click(screen.getByRole('button', { name: /ada lovelace/i }))
    expect(onChange).toHaveBeenLastCalledWith({ mode: 'user', picked: ADA })
    expect(screen.queryByLabelText('Search users')).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Change' }))
    expect(onChange).toHaveBeenLastCalledWith({ mode: 'user', picked: null })
    expect((screen.getByLabelText('Search users') as HTMLInputElement).value).toBe('')
  })

  it('asks for a name and an optional phone number beside a typed address', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    renderWithQuery(<Harness onChange={onChange} />)

    await user.click(screen.getByRole('radio', { name: 'Email address' }))
    expect(screen.getByLabelText('Requester name (required)')).toBeTruthy()
    expect(screen.getByLabelText('Requester phone (optional)')).toBeTruthy()

    await user.type(screen.getByLabelText('Requester name (required)'), 'Ada')
    expect(onChange).toHaveBeenLastCalledWith({ mode: 'email', email: '', name: 'Ada', phone: '' })
  })

  it('counts the two-character minimum in characters, as the server does: one emoji is one', async () => {
    const user = userEvent.setup()
    renderWithQuery(<Harness />)

    await user.click(screen.getByRole('radio', { name: 'Another user' }))
    await user.type(screen.getByLabelText('Search users'), '\u{1F600}')
    await new Promise((r) => setTimeout(r, 450)) // past the debounce
    expect(lookup).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText('Search users'), 'a')
    await waitFor(() => expect(lookup).toHaveBeenCalledWith('\u{1F600}a'))
  })
})
