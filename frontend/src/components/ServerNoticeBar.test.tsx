import { beforeEach, describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { ServerNoticeBar, type Notice } from './ServerNoticeBar'

// The bar takes notices as data so a new source (an update notice, say) only
// has to produce one. `info` is for good news and housekeeping: it never makes
// the bar red and is not a "problem".

const info: Notice = { id: 'update', severity: 'info', title: 'Version 1.4 is available', signature: '1.4', body: <p>Update at your leisure.</p> }
const config: Notice = { id: 'c', severity: 'config', title: 'Needs setup', signature: '', body: <p>Setup.</p> }
const failure: Notice = { id: 'f', severity: 'failure', title: 'Broken', signature: '', body: <p>Fix.</p> }

beforeEach(() => sessionStorage.clear())

describe('the info severity', () => {
  it('as the only notice is a polite status, not an alert, and not red', () => {
    render(<ServerNoticeBar notices={[info]} />)
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('status').textContent).toContain('Version 1.4 is available')
    const bar = screen.getByRole('region', { name: 'Server notices' })
    expect(bar.className).not.toContain('bg-red-')
    expect(bar.className).not.toContain('bg-amber-')
  })

  it('is not counted in "N server problems"', () => {
    render(<ServerNoticeBar notices={[info, config, failure]} />)
    expect(screen.getByRole('alert').textContent).toContain('2 server problems need attention')
  })

  it('does not turn an amber bar red, and does not keep one red', () => {
    const { rerender } = render(<ServerNoticeBar notices={[info, config]} />)
    expect(screen.getByRole('region', { name: 'Server notices' }).className).toContain('bg-amber-')
    rerender(<ServerNoticeBar notices={[info, failure]} />)
    expect(screen.getByRole('region', { name: 'Server notices' }).className).toContain('bg-red-')
  })

  it('beside one problem names that problem', () => {
    render(<ServerNoticeBar notices={[info, config]} />)
    expect(screen.getByRole('alert').textContent).toContain('Needs setup')
  })

  it('still lists in the details and can be hidden', () => {
    render(<ServerNoticeBar notices={[info, config]} />)
    fireEvent.click(screen.getByRole('button', { name: /details/i }))
    expect(screen.getByText('Update at your leisure.')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /hide for this session: version 1.4/i }))
    expect(screen.queryByText('Update at your leisure.')).toBeNull()
  })

  it('two info notices with no problems say how many, as a status', () => {
    render(<ServerNoticeBar notices={[info, { ...info, id: 'u2', title: 'Other' }]} />)
    expect(screen.getByRole('status').textContent).toContain('2 notices')
  })
})
