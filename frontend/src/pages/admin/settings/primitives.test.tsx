import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { SettingRow, Toggle } from './primitives'

// A switch in a settings row is named by the row's label, so a screen reader
// says what it switches; one with its own label keeps it.
describe('Toggle in a SettingRow', () => {
  it('takes its name from the row label when it has none of its own', () => {
    render(
      <SettingRow label="Allow self sign-up">
        <Toggle checked={false} onChange={() => {}} />
      </SettingRow>,
    )
    expect(screen.getByRole('switch', { name: 'Allow self sign-up' })).toBeTruthy()
  })

  it('keeps a label of its own', () => {
    render(
      <SettingRow label="Row label">
        <Toggle checked label="Own label" onChange={() => {}} />
      </SettingRow>,
    )
    expect(screen.getByRole('switch', { name: 'Own label' })).toBeTruthy()
  })

  it('has no name outside a row and without a label (the caller must supply one)', () => {
    render(<Toggle checked onChange={() => {}} />)
    expect(screen.getByRole('switch').getAttribute('aria-labelledby')).toBeNull()
  })
})
