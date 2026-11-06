import { describe, expect, it, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithQuery } from '@/test/render'
import { useAuthStore } from '@/store/auth'
import * as ticketsApi from '@/api/tickets'

// The Categories page caches one level's assignments under ['ctiFields', ...]
// and the new-ticket form needs the whole resolved list, so the form reads it
// under its own key. Sharing the key would hand the form a partial list, and no
// other test notices: the mocked resolver answers either way.

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))
vi.mock('@/components/Layout', () => ({
  Layout: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { NewTicketPage } from './NewTicketPage'

beforeEach(() => {
  vi.restoreAllMocks()
  useAuthStore.setState({
    user: {
      id: 'u-1',
      email: 'staff@example.com',
      display_name: 'Sam',
      role: 'staff',
      mfa_enabled: false,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    },
  })
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  vi.spyOn(ticketsApi, 'listPublicCategories').mockResolvedValue([{ id: 'cat-1', name: 'Network', active: true, sort_order: 1 }] as any)
  vi.spyOn(ticketsApi, 'listPublicTypes').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'listPublicItems').mockResolvedValue([])
  vi.spyOn(ticketsApi, 'resolveFieldsForCTI').mockResolvedValue([
    {
      id: 'a-1',
      field_def_id: 'f-1',
      scope_type: 'category',
      scope_id: 'cat-1',
      sort_order: 1,
      visible_on_new: true,
      required_on_new: false,
      field_def: { id: 'f-1', name: 'Asset tag', field_type: 'text', options: [], active: true, sort_order: 1 },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
  ])
})

describe('the new-ticket form and the query cache', () => {
  it("reads the resolved fields under ['ctiFieldsResolved', ...], not the Categories page's key", async () => {
    const user = userEvent.setup()
    const { queryClient } = renderWithQuery(<NewTicketPage />)
    await screen.findByRole('option', { name: 'Network' })
    await user.selectOptions(screen.getByLabelText(/category/i), 'cat-1')
    await screen.findByLabelText('Asset tag')

    const keys = queryClient.getQueryCache().getAll().map((q) => q.queryKey[0])
    expect(keys).toContain('ctiFieldsResolved')
    expect(keys).not.toContain('ctiFields')
  })
})
