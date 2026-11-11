import { useQuery } from '@tanstack/react-query'
import { listAssignableStaff } from '@/api/admin'
import { listPublicCategories, listPublicItems, listPublicTypes } from '@/api/tickets'
import { api } from '@/api/client'
import type { Category, Group, TicketItem, TicketType } from '@/api/types'
import type { AssignableStaff } from '@/api/admin'
import type { TicketSearch } from '@/lib/ticketSearch'

// The lists the filters offer and the chips read their names from. React Query
// caches by key, so the page (for its chips) and the open panel (for its
// selects) share one request each.
//
// Staff and groups are fetched only when a field or a chip needs them, so a
// page that never opens the panel does not ask for two lists it will not show.
export function useFilterLookups(search: TicketSearch, opts: { staff: boolean; wantPeople: boolean }) {
  const { data: categories = [] } = useQuery({
    queryKey: ['filter-categories'],
    queryFn: listPublicCategories,
    staleTime: 5 * 60_000,
  })
  const { data: types = [] } = useQuery({
    queryKey: ['filter-types', search.category],
    queryFn: () => listPublicTypes(search.category!),
    enabled: Boolean(search.category),
    staleTime: 5 * 60_000,
  })
  const { data: items = [] } = useQuery({
    queryKey: ['filter-items', search.category, search.type],
    queryFn: () => listPublicItems(search.category!, search.type!),
    enabled: Boolean(search.category && search.type),
    staleTime: 5 * 60_000,
  })
  const people = opts.staff && (opts.wantPeople || Boolean(search.assignee) || Boolean(search.group))
  const { data: staff = [] } = useQuery({
    queryKey: ['filter-staff'],
    queryFn: listAssignableStaff,
    enabled: people,
    staleTime: 5 * 60_000,
  })
  const { data: groups = [] } = useQuery({
    queryKey: ['filter-groups'],
    queryFn: async () => (await api.get<Group[]>('/groups')).data ?? [],
    enabled: people,
    staleTime: 5 * 60_000,
  })
  return { categories, types, items, staff, groups } as {
    categories: Category[]
    types: TicketType[]
    items: TicketItem[]
    staff: AssignableStaff[]
    groups: Group[]
  }
}
