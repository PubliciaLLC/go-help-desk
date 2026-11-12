import { useQuery } from '@tanstack/react-query'
import { lookupRequesterById } from '@/api/tickets'

// A picked account. Its name is not in the address, only its id, so a reload
// has to ask for it. data is null when the account cannot be shown.
export function useRequesterById(id: string | undefined) {
  return useQuery({
    queryKey: ['requester', id],
    queryFn: () => lookupRequesterById(id!),
    enabled: Boolean(id),
    // An account does not change its name under a page that is open.
    staleTime: 5 * 60_000,
  })
}
