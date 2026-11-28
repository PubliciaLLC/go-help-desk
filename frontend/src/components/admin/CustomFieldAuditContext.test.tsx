import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { CustomFieldAuditContext } from './CustomFieldAuditContext'

// The audit entries for a custom field name the field on both sides, but the
// diff only lists keys that changed, and shows nothing at all for a create or
// a delete. This line says which field, and which option or place.

describe('CustomFieldAuditContext', () => {
  it('names the field for a create entry, which has no before', () => {
    render(<CustomFieldAuditContext before={null} after={{ field_name: 'Department', field_type: 'select' }} />)
    expect(screen.getByText('Department')).toBeTruthy()
  })

  it('names the field and the option for an option-deleted entry, which has no after', () => {
    const { container } = render(
      <CustomFieldAuditContext before={{ field_name: 'Department', option_value: 'Marketing' }} after={null} />,
    )
    expect(container.textContent).toBe('Department · option "Marketing"')
  })

  it('names the field and the place for an attached entry', () => {
    const { container } = render(
      <CustomFieldAuditContext
        before={null}
        after={{ field_name: 'Department', scope_path: 'Hardware › Laptop', visible_on_new: true }}
      />,
    )
    expect(container.textContent).toBe('Department · Hardware › Laptop')
  })

  it('takes the field name from before when after has none', () => {
    render(<CustomFieldAuditContext before={{ field_name: 'Dept' }} after={{ active: false }} />)
    expect(screen.getByText('Dept')).toBeTruthy()
  })

  it('renders nothing when the entry carries no allow-listed text', () => {
    const { container } = render(<CustomFieldAuditContext before={{ active: true }} after={{ active: false }} />)
    expect(container.textContent).toBe('')
  })

  it('renders nothing when both sides are missing', () => {
    const { container } = render(<CustomFieldAuditContext />)
    expect(container.textContent).toBe('')
  })

  it('shows text that looks like markup as text', () => {
    const { container } = render(
      <CustomFieldAuditContext after={{ field_name: '<img src=x onerror=alert(1)>' }} />,
    )
    expect(container.querySelector('img')).toBeNull()
    expect(container.textContent).toContain('<img src=x onerror=alert(1)>')
  })
})
