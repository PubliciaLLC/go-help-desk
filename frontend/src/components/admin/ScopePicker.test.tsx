import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ScopePicker } from './ScopePicker'
import type { ScopeInfo } from '@/api/types'

const catalogue: ScopeInfo[] = [
  { scope: 'tickets:read', resource: 'tickets', action: 'read' },
  { scope: 'tickets:write', resource: 'tickets', action: 'write' },
  { scope: 'users:read', resource: 'users', action: 'read' },
  { scope: 'users:write', resource: 'users', action: 'write' },
]

describe('ScopePicker', () => {
  it('selects a scope', async () => {
    const onChange = vi.fn()
    render(<ScopePicker catalogue={catalogue} selected={[]} onChange={onChange} />)

    await userEvent.click(screen.getByLabelText('tickets:read'))
    expect(onChange).toHaveBeenCalledWith(['tickets:read'])
  })

  // The server treats write as implying read. A write box ticked while read
  // shows unticked would misrepresent what the credential can do.
  it('selecting write also selects read', async () => {
    const onChange = vi.fn()
    render(<ScopePicker catalogue={catalogue} selected={[]} onChange={onChange} />)

    await userEvent.click(screen.getByLabelText('tickets:write'))
    expect(onChange).toHaveBeenCalledWith(['tickets:read', 'tickets:write'])
  })

  it('unselecting read also unselects write', async () => {
    const onChange = vi.fn()
    render(
      <ScopePicker
        catalogue={catalogue}
        selected={['tickets:read', 'tickets:write']}
        onChange={onChange}
      />,
    )

    await userEvent.click(screen.getByLabelText('tickets:read'))
    expect(onChange).toHaveBeenCalledWith([])
  })

  it('does not touch other resources', async () => {
    const onChange = vi.fn()
    render(
      <ScopePicker catalogue={catalogue} selected={['users:write', 'users:read']} onChange={onChange} />,
    )

    await userEvent.click(screen.getByLabelText('tickets:read'))
    expect(onChange).toHaveBeenCalledWith(['tickets:read', 'users:read', 'users:write'])
  })

  // There is deliberately no shortcut that grants everything. If one is ever
  // added, it must be a decision someone makes on purpose, not a convenience
  // that quietly recreates the unrestricted credential this replaced.
  it('offers no select-all or wildcard control', () => {
    render(<ScopePicker catalogue={catalogue} selected={[]} onChange={vi.fn()} />)

    expect(screen.queryByLabelText('*')).toBeNull()
    for (const text of [/select all/i, /full access/i, /all permissions/i, /grant all/i]) {
      expect(screen.queryByText(text)).toBeNull()
    }
    // Every checkbox is exactly one catalogue entry.
    expect(screen.getAllByRole('checkbox')).toHaveLength(catalogue.length)
  })

  // Empty is the default and it denies. Saying so where the choice is made is
  // the difference between a deliberate decision and a surprise 403.
  it('warns that an empty selection can do nothing', () => {
    render(<ScopePicker catalogue={catalogue} selected={[]} onChange={vi.fn()} />)
    expect(screen.getByText(/cannot do anything/i)).toBeDefined()
  })

  it('drops the warning once something is selected', () => {
    render(<ScopePicker catalogue={catalogue} selected={['tickets:read']} onChange={vi.fn()} />)
    expect(screen.queryByText(/cannot do anything/i)).toBeNull()
    expect(screen.getByText('1 selected')).toBeDefined()
  })
})
