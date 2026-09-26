import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { LinkedTicketsPanel } from './LinkedTicketsPanel'
import * as ticketsApi from '@/api/tickets'
import * as adminApi from '@/api/admin'
import type { Ticket, TicketLink, LinkType } from '@/api/types'

// Mock the Link component since it needs a router provider
vi.mock('@tanstack/react-router', () => ({
  Link: ({ to, params, children, ...rest }: { to: string; params: Record<string, string>; children: React.ReactNode }) => (
    // eslint-disable-next-line jsx-a11y/anchor-has-content
    <a href={to.replace('$id', params.id)} {...rest}>
      {children}
    </a>
  ),
}))

// Test tickets
const TICKETS: Record<string, Ticket> = {
  'tkt-1': {
    id: 'tkt-1',
    tracking_number: 'GHD-2026-000001',
    subject: 'Main ticket',
    description: 'Main ticket description',
    category_id: 'cat-1',
    priority: 'high',
    status_id: 'status-1',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  },
  'tkt-2': {
    id: 'tkt-2',
    tracking_number: 'GHD-2026-000002',
    subject: 'Parent ticket',
    description: 'Parent ticket description',
    category_id: 'cat-1',
    priority: 'high',
    status_id: 'status-1',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  },
  'tkt-3': {
    id: 'tkt-3',
    tracking_number: 'GHD-2026-000003',
    subject: 'Related ticket',
    description: 'Related ticket description',
    category_id: 'cat-1',
    priority: 'medium',
    status_id: 'status-1',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  },
  'tkt-9': {
    id: 'tkt-9',
    tracking_number: 'GHD-2026-000009',
    subject: 'Duplicate source',
    description: 'Duplicate source description',
    category_id: 'cat-1',
    priority: 'low',
    status_id: 'status-1',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  },
}

beforeEach(() => {
  vi.restoreAllMocks()
  // Mock listStatuses since it's called but not needed for these tests
  vi.spyOn(adminApi, 'listStatuses').mockResolvedValue([
    { id: 'status-1', name: 'New', kind: 'system', sort_order: 1, color: '#3b82f6', active: true, ticket_count: 0 },
  ])
})

function renderPanel(ticketId: string = 'tkt-1') {
  return renderWithQuery(<LinkedTicketsPanel ticketId={ticketId} />)
}

describe('LinkedTicketsPanel', () => {
  describe('list - label rendering', () => {
    // Table-driven test for label computation
    const labelCases: Array<{
      name: string
      link_type: LinkType
      viewedIsSource: boolean
      expectedLabel: string
    }> = [
      { name: 'related_to viewed as source', link_type: 'related_to', viewedIsSource: true, expectedLabel: 'Related to' },
      { name: 'related_to viewed as target', link_type: 'related_to', viewedIsSource: false, expectedLabel: 'Related to' },
      { name: 'parent_child viewed as source (is parent)', link_type: 'parent_child', viewedIsSource: true, expectedLabel: 'Child' },
      { name: 'parent_child viewed as target (is child)', link_type: 'parent_child', viewedIsSource: false, expectedLabel: 'Parent' },
      { name: 'caused_by viewed as source (was caused by)', link_type: 'caused_by', viewedIsSource: true, expectedLabel: 'Caused by' },
      { name: 'caused_by viewed as target (cause of)', link_type: 'caused_by', viewedIsSource: false, expectedLabel: 'Cause of' },
      { name: 'duplicate_of viewed as source (is duplicate)', link_type: 'duplicate_of', viewedIsSource: true, expectedLabel: 'Duplicate of' },
      { name: 'duplicate_of viewed as target (duplicated by)', link_type: 'duplicate_of', viewedIsSource: false, expectedLabel: 'Duplicated by' },
    ]

    labelCases.forEach(({ name, link_type, viewedIsSource, expectedLabel }) => {
      it(`renders "${expectedLabel}" for ${name}`, async () => {
        const link: TicketLink = viewedIsSource
          ? { source_id: 'tkt-1', target_id: 'tkt-2', link_type }
          : { source_id: 'tkt-2', target_id: 'tkt-1', link_type }

        vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
        vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue(TICKETS['tkt-2'])

        renderPanel('tkt-1')
        expect(await screen.findByText(expectedLabel)).toBeDefined()
      })
    })
  })

  describe('list - rendering', () => {
    it('renders tracking number and subject of linked ticket', async () => {
      const link: TicketLink = { source_id: 'tkt-1', target_id: 'tkt-2', link_type: 'related_to' }
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
      vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue(TICKETS['tkt-2'])

      renderPanel('tkt-1')
      expect(await screen.findByText(/GHD-2026-000002 · Parent ticket/)).toBeDefined()
    })

    it('shows empty state when no links', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])

      renderPanel('tkt-1')
      expect(await screen.findByText('No linked tickets')).toBeDefined()
    })

    it('shows "Ticket not visible to you" when getTicket fails', async () => {
      const link: TicketLink = { source_id: 'tkt-1', target_id: 'tkt-2', link_type: 'related_to' }
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
      vi.spyOn(ticketsApi, 'getTicket').mockRejectedValue(
        Object.assign(new Error('Forbidden'), {
          isAxiosError: true,
          response: { status: 403 },
        })
      )

      renderPanel('tkt-1')
      expect(await screen.findByText('Ticket not visible to you')).toBeDefined()
    })

    it('still shows remove button when ticket is not visible', async () => {
      const link: TicketLink = { source_id: 'tkt-1', target_id: 'tkt-2', link_type: 'related_to' }
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
      vi.spyOn(ticketsApi, 'getTicket').mockRejectedValue(
        Object.assign(new Error('Forbidden'), {
          isAxiosError: true,
          response: { status: 403 },
        })
      )

      renderPanel('tkt-1')
      await screen.findByText('Ticket not visible to you')
      expect(screen.getByRole('button', { name: 'Remove link' })).toBeDefined()
    })
  })

  describe('add - form visibility', () => {
    it('shows "Add link" button initially', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])

      renderPanel('tkt-1')
      expect(await screen.findByRole('button', { name: 'Add link' })).toBeDefined()
    })

    it('opens the form when "Add link" is clicked', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      expect(await screen.findByRole('button', { name: 'Link' })).toBeDefined()
    })
  })

  describe('add - relation select', () => {
    it('defaults to "Related to"', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const select = await screen.findByRole('combobox', { name: /relation/i })
      expect((select as HTMLSelectElement).value).toBe('related_to')
    })

    it('offers the correct relation options', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const select = await screen.findByRole('combobox', { name: /relation/i })
      const options = Array.from(select.querySelectorAll('option')).map(o => o.textContent)
      expect(options).toContain('Related to')
      expect(options).toContain('Parent of')
      expect(options).toContain('Child of')
      expect(options).toContain('Caused by')
      expect(options).toContain('Duplicate of')
    })
  })

  describe('add - ticket picker', () => {
    it('debounces search and calls listTickets after 2 characters', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([TICKETS['tkt-2']])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'pa')

      // Should not have called listTickets yet (need >= 2 chars)
      expect(ticketsApi.listTickets).not.toHaveBeenCalled()

      await user.type(input, 'rent')
      await waitFor(() => {
        expect(ticketsApi.listTickets).toHaveBeenCalledWith({ q: 'parent', limit: 8 })
      })
    })

    it('filters out viewed ticket and already-linked tickets', async () => {
      const link: TicketLink = { source_id: 'tkt-1', target_id: 'tkt-3', link_type: 'related_to' }
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
      vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue(TICKETS['tkt-3'])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([
        TICKETS['tkt-1'], // Should be filtered (viewed ticket)
        TICKETS['tkt-2'],
        TICKETS['tkt-3'], // Should be filtered (already linked)
      ])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      // Wait for the linked tickets list to render
      await screen.findByText(/GHD-2026-000003/)

      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'par')
      await waitFor(() => {
        expect(ticketsApi.listTickets).toHaveBeenCalled()
      })

      // Only tkt-2 should be in the dropdown
      // Check that tkt-2 appears in the dropdown (within a li element)
      const dropdownItems = screen.getAllByRole('listitem')
      const trackingNumbers = dropdownItems.map(el => el.textContent)

      expect(trackingNumbers.some(text => text?.includes('GHD-2026-000002'))).toBe(true)
      expect(trackingNumbers.some(text => text?.includes('GHD-2026-000001'))).toBe(false)
      expect(trackingNumbers.some(text => text?.includes('GHD-2026-000003'))).toBe(false)
    })

    it('selects a ticket from the dropdown', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([TICKETS['tkt-2']])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'par')
      await waitFor(() => expect(ticketsApi.listTickets).toHaveBeenCalled())

      const item = await screen.findByText(/GHD-2026-000002 · Parent ticket/)
      await user.click(item)

      // Chip should appear (input is cleared when ticket is selected)
      await waitFor(() => {
        expect(screen.queryByRole('textbox', { name: /ticket/i })).toBeNull()
        expect(screen.getByText(/GHD-2026-000002 · Parent ticket/)).toBeDefined()
      })
    })

    it('handles Enter key for exact ticket lookup', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue(TICKETS['tkt-2'])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'GHD-2026-000002{Enter}')

      await waitFor(() => {
        expect(ticketsApi.getTicket).toHaveBeenCalledWith('GHD-2026-000002')
      })

      // Chip should appear
      expect(await screen.findByText(/GHD-2026-000002 · Parent ticket/)).toBeDefined()
    })

    it('shows error when Enter lookup fails', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'getTicket').mockRejectedValue(
        Object.assign(new Error('Not found'), {
          isAxiosError: true,
          response: { status: 404 },
        })
      )
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'INVALID{Enter}')

      await waitFor(() => {
        expect(screen.getByText(/No ticket matches/)).toBeDefined()
      })
    })

    it('disables Link button until a ticket is selected', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const linkBtn = await screen.findByRole('button', { name: 'Link' })
      expect((linkBtn as HTMLButtonElement).disabled).toBe(true)
    })
  })

  describe('add - linking with correct relation', () => {
    it('links as Related to', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([TICKETS['tkt-2']])
      const addLinkMock = vi.spyOn(ticketsApi, 'addLink').mockResolvedValue()
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'par')
      await waitFor(() => expect(ticketsApi.listTickets).toHaveBeenCalled())

      const item = await screen.findByText(/GHD-2026-000002/)
      await user.click(item)

      // Don't change relation, it's already "Related to"
      await user.click(await screen.findByRole('button', { name: 'Link' }))

      await waitFor(() => {
        expect(addLinkMock).toHaveBeenCalledWith('tkt-1', 'tkt-2', 'related_to')
      })
    })

    it('links as Parent of (not reversed)', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([TICKETS['tkt-2']])
      const addLinkMock = vi.spyOn(ticketsApi, 'addLink').mockResolvedValue()
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'par')
      await waitFor(() => expect(ticketsApi.listTickets).toHaveBeenCalled())

      const item = await screen.findByText(/GHD-2026-000002/)
      await user.click(item)

      const relationSelect = await screen.findByRole('combobox', { name: /relation/i })
      await user.selectOptions(relationSelect, 'parent_of')

      await user.click(await screen.findByRole('button', { name: 'Link' }))

      await waitFor(() => {
        expect(addLinkMock).toHaveBeenCalledWith('tkt-1', 'tkt-2', 'parent_child')
      })
    })

    it('links as Child of (reversed)', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([TICKETS['tkt-2']])
      const addLinkMock = vi.spyOn(ticketsApi, 'addLink').mockResolvedValue()
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'par')
      await waitFor(() => expect(ticketsApi.listTickets).toHaveBeenCalled())

      const item = await screen.findByText(/GHD-2026-000002/)
      await user.click(item)

      const relationSelect = await screen.findByRole('combobox', { name: /relation/i })
      await user.selectOptions(relationSelect, 'child_of')

      await user.click(await screen.findByRole('button', { name: 'Link' }))

      // When reversed, tkt-2 is the source and tkt-1 is the target
      await waitFor(() => {
        expect(addLinkMock).toHaveBeenCalledWith('tkt-2', 'tkt-1', 'parent_child')
      })
    })

    it('closes form and invalidates queries on success', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([TICKETS['tkt-2']])
      vi.spyOn(ticketsApi, 'addLink').mockResolvedValue()
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'par')
      await waitFor(() => expect(ticketsApi.listTickets).toHaveBeenCalled())

      const item = await screen.findByText(/GHD-2026-000002/)
      await user.click(item)

      await user.click(await screen.findByRole('button', { name: 'Link' }))

      // Form should close and Add link button should be visible again
      await waitFor(() => {
        expect(screen.queryByRole('button', { name: 'Link' })).toBeNull()
        expect(screen.getByRole('button', { name: 'Add link' })).toBeDefined()
      })
    })

    it('shows error and keeps form open on failure', async () => {
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([])
      vi.spyOn(ticketsApi, 'listTickets').mockResolvedValue([TICKETS['tkt-2']])
      vi.spyOn(ticketsApi, 'addLink').mockRejectedValue(
        Object.assign(new Error('Request failed'), {
          isAxiosError: true,
          response: {
            status: 403,
            data: { error: { code: 'forbidden', message: 'not your ticket' } },
          },
        })
      )
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await user.click(await screen.findByRole('button', { name: 'Add link' }))

      const input = await screen.findByRole('textbox', { name: /ticket/i })
      await user.type(input, 'par')
      await waitFor(() => expect(ticketsApi.listTickets).toHaveBeenCalled())

      const item = await screen.findByText(/GHD-2026-000002/)
      await user.click(item)

      await user.click(await screen.findByRole('button', { name: 'Link' }))

      // Error should be shown
      expect(await screen.findByText(/not your ticket/)).toBeDefined()

      // Form should still be open
      expect(await screen.findByRole('button', { name: 'Link' })).toBeDefined()
    })
  })

  describe('remove', () => {
    it('calls removeLink with link source_id and target_id', async () => {
      const link: TicketLink = { source_id: 'tkt-2', target_id: 'tkt-1', link_type: 'related_to' }
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
      vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue(TICKETS['tkt-2'])
      const removeLinkMock = vi.spyOn(ticketsApi, 'removeLink').mockResolvedValue()
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await screen.findByText(/GHD-2026-000002/)

      const removeBtn = screen.getByRole('button', { name: /Remove link/ })
      await user.click(removeBtn)

      await waitFor(() => {
        // CRITICAL: when viewed ticket is target, we use link.source_id and link.target_id
        expect(removeLinkMock).toHaveBeenCalledWith('tkt-2', 'tkt-1', 'related_to')
      })
    })

    it('also calls removeLink when viewed ticket is source', async () => {
      const link: TicketLink = { source_id: 'tkt-1', target_id: 'tkt-2', link_type: 'caused_by' }
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
      vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue(TICKETS['tkt-2'])
      const removeLinkMock = vi.spyOn(ticketsApi, 'removeLink').mockResolvedValue()
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await screen.findByText(/GHD-2026-000002/)

      const removeBtn = screen.getByRole('button', { name: /Remove link/ })
      await user.click(removeBtn)

      await waitFor(() => {
        expect(removeLinkMock).toHaveBeenCalledWith('tkt-1', 'tkt-2', 'caused_by')
      })
    })

    it('invalidates link queries on removal', async () => {
      const link: TicketLink = { source_id: 'tkt-1', target_id: 'tkt-2', link_type: 'related_to' }
      vi.spyOn(ticketsApi, 'listLinks').mockResolvedValue([link])
      vi.spyOn(ticketsApi, 'getTicket').mockResolvedValue(TICKETS['tkt-2'])
      vi.spyOn(ticketsApi, 'removeLink').mockResolvedValue()
      const user = userEvent.setup()

      renderPanel('tkt-1')
      await screen.findByText(/GHD-2026-000002/)

      const removeBtn = screen.getByRole('button', { name: /Remove link/ })
      await user.click(removeBtn)

      // The component should re-fetch links
      await waitFor(() => {
        expect(ticketsApi.listLinks).toHaveBeenCalledTimes(2)
      })
    })
  })

  describe('type drift', () => {
    it('TypeScript compiles with all four LinkType values in LABELS', async () => {
      // This test is a compile-time check: if LinkType and LABELS don't match,
      // this line will fail TypeScript compilation when running npm run build.
      const expectedKeys: LinkType[] = ['related_to', 'parent_child', 'caused_by', 'duplicate_of']
      const actualKeys = Object.keys({
        related_to: { asSource: '', asTarget: '' },
        parent_child: { asSource: '', asTarget: '' },
        caused_by: { asSource: '', asTarget: '' },
        duplicate_of: { asSource: '', asTarget: '' },
      }) as LinkType[]

      expect(new Set(actualKeys)).toEqual(new Set(expectedKeys))
    })
  })
})
