import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { CredentialAuditContext } from './CredentialAuditContext'

describe('CredentialAuditContext', () => {
  it('joins an OAuth client name and scopes', () => {
    const { container } = render(
      <CredentialAuditContext entityType="oauth_client" before={null} after={{ client_name: 'Sync', scopes: 'tickets:read' }} />,
    )
    expect(container.textContent).toBe('Sync · tickets:read')
  })

  it('reads the before side when there is no after, as on a delete', () => {
    const { container } = render(
      <CredentialAuditContext entityType="webhook" before={{ url_host: 'example.com', events: '*' }} after={null} />,
    )
    expect(container.textContent).toBe('example.com · *')
  })

  it('renders nothing when the entry has neither key', () => {
    const { container } = render(<CredentialAuditContext entityType="webhook" before={{ enabled: true }} after={null} />)
    expect(container.textContent).toBe('')
  })

  it('does not show a webhook URL even if one were sent', () => {
    const { container } = render(
      <CredentialAuditContext entityType="webhook" before={null} after={{ url: 'https://example.com/T/SECRET', url_host: 'example.com' }} />,
    )
    expect(container.textContent).not.toContain('SECRET')
  })
})
