import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { lookupRequesters } from '@/api/tickets'
import { ME, MIN_QUERY, queryLength, type Requester } from './requester'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export function RequesterPicker({
  value,
  onChange,
  disabled,
}: {
  value: Requester
  onChange: (r: Requester) => void
  disabled?: boolean
}) {
  const [search, setSearch] = useState('')
  const [debounced, setDebounced] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(search.trim()), 300)
    return () => clearTimeout(timer)
  }, [search])

  const searching = value.mode === 'user' && !value.picked && queryLength(debounced) >= MIN_QUERY
  const { data: hits = [], isFetching } = useQuery({
    queryKey: ['requester-lookup', debounced],
    queryFn: () => lookupRequesters(debounced),
    enabled: searching,
  })

  function choose(mode: Requester['mode']) {
    setSearch('')
    setDebounced('')
    if (mode === 'me') onChange(ME)
    else if (mode === 'user') onChange({ mode: 'user', picked: null })
    else onChange({ mode: 'email', email: '', name: '', phone: '' })
  }

  return (
    <fieldset className="space-y-3">
      <legend className="text-sm font-medium text-gray-700">Requester</legend>
      <div className="flex flex-wrap items-center gap-x-6 gap-y-2 text-sm text-gray-700">
        <label className="flex items-center gap-1.5">
          <input
            type="radio"
            name="requester-mode"
            checked={value.mode === 'me'}
            onChange={() => choose('me')}
            disabled={disabled}
          />
          Me
        </label>
        <label className="flex items-center gap-1.5">
          <input
            type="radio"
            name="requester-mode"
            checked={value.mode === 'user'}
            onChange={() => choose('user')}
            disabled={disabled}
          />
          Another user
        </label>
        <label className="flex items-center gap-1.5">
          <input
            type="radio"
            name="requester-mode"
            checked={value.mode === 'email'}
            onChange={() => choose('email')}
            disabled={disabled}
          />
          Email address
        </label>
      </div>

      {value.mode === 'user' && value.picked && (
        <p className="text-sm text-gray-700 wrap-anywhere">
          Filing for <strong>{value.picked.display_name}</strong> ({value.picked.email}){' '}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => choose('user')}
            disabled={disabled}
          >
            Change
          </Button>
        </p>
      )}

      {value.mode === 'user' && !value.picked && (
        <div className="space-y-2">
          <div className="space-y-1">
            <Label htmlFor="requester-search">Search users</Label>
            <Input
              id="requester-search"
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Name or email, at least 2 characters"
              autoComplete="off"
              disabled={disabled}
            />
          </div>
          {hits.length > 0 && searching && (
            <ul aria-label="Matching users" className="divide-y divide-gray-100 rounded-md border border-gray-200">
              {hits.map((h) => (
                <li key={h.id}>
                  <button
                    type="button"
                    className="flex w-full flex-col px-3 py-2 text-left text-sm hover:bg-gray-50 focus-visible:bg-gray-50 focus-visible:outline-none sm:flex-row sm:items-baseline sm:gap-2"
                    onClick={() => onChange({ mode: 'user', picked: h })}
                  >
                    <span className="font-medium text-gray-900 wrap-anywhere">{h.display_name}</span>
                    <span className="text-gray-500 wrap-anywhere">{h.email}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          <p role="status" className="text-sm text-gray-500">
            {searching && !isFetching && hits.length === 0 ? 'No users match.' : ''}
          </p>
        </div>
      )}

      {value.mode === 'email' && (
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="requester-email">Requester email</Label>
            <Input
              id="requester-email"
              type="email"
              value={value.email}
              onChange={(e) => onChange({ ...value, email: e.target.value })}
              placeholder="someone@example.com"
              autoComplete="off"
              disabled={disabled}
            />
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="space-y-1">
              <Label htmlFor="requester-name">Requester name (required)</Label>
              <Input
                id="requester-name"
                value={value.name}
                onChange={(e) => onChange({ ...value, name: e.target.value })}
                autoComplete="off"
                disabled={disabled}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="requester-phone">Requester phone (optional)</Label>
              <Input
                id="requester-phone"
                type="tel"
                value={value.phone}
                onChange={(e) => onChange({ ...value, phone: e.target.value })}
                autoComplete="off"
                disabled={disabled}
              />
            </div>
          </div>
          <p className="text-sm text-gray-500">
            If this address has an account the ticket is filed for that account, and the name and
            phone are not used. Otherwise they are sent a link to follow it, as for any guest
            ticket.
          </p>
        </div>
      )}
    </fieldset>
  )
}
