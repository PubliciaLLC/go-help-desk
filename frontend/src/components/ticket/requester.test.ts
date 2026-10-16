import { describe, expect, it } from 'vitest'
import { ME, reporterFields, type Requester } from './requester'

describe('reporterFields', () => {
  const ada = { id: 'u-ada', display_name: 'Ada Lovelace', email: 'ada@example.com' }

  const cases: { name: string; who: Requester; want: unknown }[] = [
    { name: 'me sends nothing', who: ME, want: { fields: {} } },
    {
      name: 'a picked user sends their id and nothing else',
      who: { mode: 'user', picked: ada },
      want: { fields: { reporter_user_id: 'u-ada' } },
    },
    {
      name: 'another user with nobody picked cannot be sent',
      who: { mode: 'user', picked: null },
      want: { error: 'Pick a requester from the search results, or choose Me.' },
    },
    {
      name: 'an address is trimmed',
      who: { mode: 'email', email: '  new.person@example.com ' },
      want: { fields: { reporter_email: 'new.person@example.com' } },
    },
    {
      name: 'a blank address cannot be sent',
      who: { mode: 'email', email: '   ' },
      want: { error: "Type the requester's email address, or choose Me." },
    },
  ]

  it.each(cases)('$name', ({ who, want }) => {
    expect(reporterFields(who)).toEqual(want)
  })
})
